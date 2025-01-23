use std::{
    cmp::min,
    fs::File,
    io::Write,
    mem::MaybeUninit,
    os::fd::{AsRawFd, FromRawFd, OwnedFd},
    path::Path,
};

use anyhow::anyhow;
use bytemuck::{Pod, Zeroable};
use nix::{
    errno::Errno,
    fcntl::{openat, OFlag},
    sys::stat::Mode,
};
use numtoa::NumToA;
use smallvec::{SmallVec, ToSmallVec};
use starry::{
    buffer_stack::BufferStack,
    interrogate::InterrogatedFile,
    path_stack::PathStack,
    sys::{
        file::fstat,
        getdents::{for_each_getdents, FileType},
        inode_flags::InodeFlags,
    },
};
use zstd::Encoder;

const READ_BUF_SIZE: usize = 65536;

const TAR_PADDING: [u8; 1024] = [0; 1024];
const PAX_HEADER_NAME: &str = "@PaxHeader";

#[repr(C)]
#[derive(Pod, Zeroable, Clone, Copy)]
struct UstarHeaderSerialized {
    // https://pubs.opengroup.org/onlinepubs/007904975/utilities/pax.html#tag_04_100_13_06
    name: [u8; 100],
    mode: [u8; 8],
    uid: [u8; 8],
    gid: [u8; 8],
    size: [u8; 12],
    mtime: [u8; 12],
    chksum: [u8; 8],
    typeflag: [u8; 1],
    linkname: [u8; 100],
    magic: [u8; 6],
    version: [u8; 2],
    uname: [u8; 32],
    gname: [u8; 32],
    devmajor: [u8; 8],
    devminor: [u8; 8],
    prefix: [u8; 155],

    // up to 512 bytes
    _padding: [u8; 12],
}

#[repr(u8)]
pub enum TypeFlag {
    Regular = b'0', // or '\0' for legacy reasons
    HardLink = b'1',
    Symlink = b'2',
    Char = b'3',
    Block = b'4',
    Directory = b'5',
    Fifo = b'6',
    HighPerformance = b'7', // = Regular
    PaxExtendedHeader = b'x',
    PaxGlobalHeader = b'g',
}

#[derive(Debug)]
pub struct OverflowError {}

impl std::error::Error for OverflowError {}

impl std::fmt::Display for OverflowError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "value too large for field")
    }
}

pub struct UstarHeader {
    data: UstarHeaderSerialized,
}

impl Default for UstarHeader {
    fn default() -> Self {
        let mut header = Self {
            data: UstarHeaderSerialized::zeroed(),
        };

        header.data.magic = *b"ustar\0";
        header.data.version = [b'0'; 2];
        header
    }
}

impl UstarHeader {
    pub fn set_entry_type(&mut self, typ: TypeFlag) {
        self.data.typeflag = [typ as u8; 1];
    }

    pub fn set_path(&mut self, path: &[u8]) -> Result<(), OverflowError> {
        match path.len() {
            // standard tar: up to 100 bytes
            0..=100 => {
                self.data.name[..path.len()].copy_from_slice(path);
            }
            // ustar prefix: 155 + 100 bytes
            101..=255 => {
                // final path = prefix + '/' + path, so we have to find a / to split on

                // special case: if path[155] = '/', then it's already split
                if path.len() > 155 && path[155] == b'/' {
                    self.data.prefix.copy_from_slice(&path[..155]);
                    let rem = &path[156..];
                    self.data.name[..rem.len()].copy_from_slice(rem);
                    return Ok(());
                }

                // get the prefix part of the string
                let prefix_path = &path[..min(path.len(), 155)];
                // split at last / in prefix
                let mut split_iter = prefix_path.rsplitn(2, |&c| c == b'/');
                let name = split_iter.next().ok_or(OverflowError {})?;
                let prefix = split_iter.next().ok_or(OverflowError {})?;
                if prefix.len() > 155 || name.len() > 100 {
                    // not splittable: path component is too long
                    return Err(OverflowError {});
                }
                // copy prefix
                self.data.prefix[..prefix.len()].copy_from_slice(prefix);
                // copy path
                self.data.name[..name.len()].copy_from_slice(name);
            }
            _ => return Err(OverflowError {}),
        }
        Ok(())
    }

    pub fn set_link_path(&mut self, path: &[u8]) -> Result<(), OverflowError> {
        if path.len() > 100 {
            return Err(OverflowError {});
        }

        self.data.linkname[..path.len()].copy_from_slice(path);
        Ok(())
    }

    pub fn set_mode(&mut self, mode: u32) -> Result<(), OverflowError> {
        write_left_padded(&mut self.data.mode, mode, 8, 8)
    }

    pub fn set_uid(&mut self, uid: u32) -> Result<(), OverflowError> {
        write_left_padded(&mut self.data.uid, uid, 8, 8)
    }

    pub fn set_gid(&mut self, gid: u32) -> Result<(), OverflowError> {
        write_left_padded(&mut self.data.gid, gid, 8, 8)
    }

    pub fn set_size(&mut self, size: u64) -> Result<(), OverflowError> {
        write_left_padded(&mut self.data.size, size, 8, 12)
    }

    pub fn set_mtime(&mut self, mtime: u64) -> Result<(), OverflowError> {
        write_left_padded(&mut self.data.mtime, mtime, 8, 12)
    }

    pub fn set_device_major(&mut self, major: u32) -> Result<(), OverflowError> {
        write_left_padded(&mut self.data.devmajor, major, 8, 8)
    }

    pub fn set_device_minor(&mut self, minor: u32) -> Result<(), OverflowError> {
        write_left_padded(&mut self.data.devminor, minor, 8, 8)
    }

    fn set_checksum(&mut self) {
        // checksum = sum of all octets, with checksum field set to spaces
        self.data.chksum = [b' '; 8];

        // spec: must be at least 17 bits
        let mut sum: u32 = 0;
        for b in bytemuck::bytes_of(&self.data) {
            sum += *b as u32;
        }
        write_left_padded(&mut self.data.chksum, sum, 8, 8).unwrap();
    }

    pub fn as_bytes(&mut self) -> &[u8] {
        // calculate checksum
        self.set_checksum();

        bytemuck::bytes_of(&self.data)
    }
}

fn write_left_padded<T: NumToA<T>>(
    out_buf: &mut [u8],
    val: T,
    base: T,
    target_len: usize,
) -> Result<(), OverflowError> {
    // stack array for max possible length
    let mut unpadded_buf: [u8; 32] = [0; 32];
    let formatted = val.numtoa(base, &mut unpadded_buf);

    // fill leading space with zeros
    let target_buf = &mut out_buf[..target_len];
    if formatted.len() > target_len {
        return Err(OverflowError {});
    }
    let padding_len = target_len - formatted.len();
    target_buf[padding_len..].copy_from_slice(formatted);
    target_buf[..padding_len].fill(b'0');
    Ok(())
}

struct PaxHeader {
    header: UstarHeader,
    data: Vec<u8>,
}

impl PaxHeader {
    fn new() -> Self {
        let mut header = UstarHeader::default();
        header.set_entry_type(TypeFlag::PaxExtendedHeader);
        // name="@PaxHeader": doesn't match bsdtar or GNU tar behavior, but spec doesn't care and this is faster
        header.set_path(PAX_HEADER_NAME.as_bytes()).unwrap();

        Self {
            header,
            data: Vec::with_capacity(1024),
        }
    }

    fn add_field<K: AsRef<[u8]> + ?Sized>(&mut self, key: &K, value: &[u8]) {
        // +3: space, equals, newline
        let key = key.as_ref();
        let payload_len = key.len() + value.len() + 3;

        // how many digits are in the length?
        let payload_len_digits = (payload_len.ilog10() + 1) as usize;
        let mut total_len = payload_len + payload_len_digits;
        // if payload_len=99, this might add a digit
        let total_len_digits = (total_len.ilog10() + 1) as usize;
        if total_len_digits > payload_len_digits {
            // add space for one more digit
            total_len += 1;
        }

        let mut itoa_buf = itoa::Buffer::new();
        let len_str = itoa_buf.format(total_len);

        // {len_str} {key}={value}\n
        self.data.extend_from_slice(len_str.as_bytes());
        self.data.push(b' ');
        self.data.extend_from_slice(key);
        self.data.push(b'=');
        self.data.extend_from_slice(value);
        self.data.push(b'\n');
    }

    fn add_integer_field<T: itoa::Integer>(&mut self, key: &str, val: T) {
        let mut buf = itoa::Buffer::new();
        self.add_field(key, buf.format(val).as_bytes());
    }

    fn is_empty(&self) -> bool {
        self.data.is_empty()
    }

    fn write_to(mut self, w: &mut impl Write) -> anyhow::Result<()> {
        self.header.set_size(self.data.len() as u64).unwrap();
        w.write_all(self.header.as_bytes())?;
        w.write_all(&self.data)?;

        // pad tar to 512 byte block
        let pad = 512 - (self.data.len() % 512);
        if pad != 512 {
            w.write_all(&TAR_PADDING[..pad])?;
        }

        Ok(())
    }
}

struct OwnedTarContext {
    buffer_stack: BufferStack,
    path_stack: PathStack,
}

impl OwnedTarContext {
    fn new() -> anyhow::Result<Self> {
        Ok(Self {
            buffer_stack: BufferStack::new()?,
            path_stack: PathStack::default(),
        })
    }
}

struct TarContext<'a, W: Write> {
    writer: W,
    // this owned/ref split allows &mut self (for Write) without preventing these from being borrowed
    buffer_stack: &'a BufferStack,
    path_stack: &'a PathStack,
}

impl<'a, W: Write> TarContext<'a, W> {
    fn new(writer: W, buffer_stack: &'a BufferStack, path_stack: &'a PathStack) -> Self {
        Self {
            writer,
            buffer_stack,
            path_stack,
        }
    }

    fn add_regular_file_contents(&mut self, file: &OwnedFd, st: &libc::stat) -> anyhow::Result<()> {
        if st.st_blocks < st.st_size / 512 {
            // TODO: sparse file
        }

        // we must never write more than the size written to the header
        let mut buf: MaybeUninit<[u8; READ_BUF_SIZE]> = MaybeUninit::uninit();
        let mut rem = st.st_size as usize;
        loop {
            let limit = std::cmp::min(rem, READ_BUF_SIZE);
            let ret = unsafe { libc::read(file.as_raw_fd(), buf.as_mut_ptr() as *mut _, limit) };
            let n = Errno::result(ret)? as usize;
            if n == 0 {
                break;
            }

            let data = unsafe { std::slice::from_raw_parts(buf.as_mut_ptr() as *const u8, n) };
            self.writer.write_all(data)?;
            rem -= n;
            if rem == 0 {
                break;
            }
        }

        // pad with zeros if we didn't write enough (file size truncated)
        if rem > 0 {
            eprintln!("file truncated; padding with {} bytes", rem);
            loop {
                let limit = std::cmp::min(rem, 512);
                self.writer.write_all(&TAR_PADDING[..limit])?;
                rem -= limit;
                if rem == 0 {
                    break;
                }
            }
        }

        // pad tar to 512 byte block
        let pad = 512 - (st.st_size % 512);
        if pad != 512 {
            self.writer.write_all(&TAR_PADDING[..pad as usize])?;
        }

        Ok(())
    }

    fn add_one_entry(&mut self, file: &InterrogatedFile, path: &[u8]) -> anyhow::Result<()> {
        // skip archiving sockets; tar can't extract it
        // TODO: non-standard extension for this?
        if file.file_type == FileType::Socket {
            return Ok(());
        }

        // PAX and normal header
        let (mut header, mut pax_header) = header_from_stat(&file.st);

        // nsecs mtime (skip invalid nsecs)
        if file.st.st_mtime_nsec != 0 && file.st.st_mtime_nsec < 1_000_000_000 {
            // "18446744073709551616.000000000" (u64::MAX + 9 digits for nanoseconds)
            let mut time_buf = SmallVec::<[u8; 30]>::new();
            let mut dec_buf = itoa::Buffer::new();
            let seconds = dec_buf.format(file.st.st_mtime);
            time_buf.extend_from_slice(seconds.as_bytes());
            time_buf.push(b'.');

            let nanos_start = time_buf.len();
            time_buf.resize(nanos_start + 9, 0);
            // can't overflow: we checked that nsec < 1e9
            write_left_padded(
                &mut time_buf[nanos_start..],
                file.st.st_mtime_nsec as u64,
                10,
                9,
            )
            .unwrap();

            pax_header.add_field("mtime", &time_buf);
        }

        if header.set_path(path).is_err() {
            // PAX long name extension
            pax_header.add_field("path", path);
        }

        if file.file_type == FileType::Symlink {
            file.with_readlink(|link_name| {
                if header.set_link_path(link_name).is_err() {
                    // PAX long name extension
                    pax_header.add_field("linkpath", link_name);
                    header.set_link_path("././@LongSymLink".as_bytes()).unwrap();
                }
            })?;
        }

        // fflags
        if let Some(flags) = file.inode_flags()? {
            let flag_names = flags.pax_names();
            match flag_names.len() {
                0 => {}
                // fastpath for common 1-flag case
                1 => pax_header.add_field("SCHILY.fflags", flag_names[0].as_bytes()),
                // multiple flags are rare
                _ => pax_header.add_field("SCHILY.fflags", flag_names.join(",").as_bytes()),
            }
        }

        // xattrs
        file.for_each_xattr(|key, value| {
            // SCHILY.xattr is a violation of the PAX spec: PAX headers must be UTF-8
            // LIBARCHIVE.xattr uses base64 to fix that, but it's not supported by GNU tar
            // we use SCHILY: it works fine because PAX fields are length-prefixed
            let mut pax_key: SmallVec<[u8; 64]> = b"SCHILY.xattr.".to_smallvec();
            pax_key.extend_from_slice(key.to_bytes());
            pax_header.add_field(&pax_key, value);

            Ok(())
        })?;

        if !pax_header.is_empty() {
            pax_header.write_to(&mut self.writer)?;
        }
        self.writer.write_all(header.as_bytes())?;

        Ok(())
    }

    fn walk_dir(
        &mut self,
        dirfd: &OwnedFd,
        nents_hint: Option<usize>,
    ) -> anyhow::Result<()> {
        for_each_getdents(dirfd, nents_hint, self.buffer_stack, |entry| {
            let path = self.path_stack.push(entry.name.to_bytes());

            let file = InterrogatedFile::from_entry(dirfd, &entry)?;
            self.add_one_entry(&file, path.get().as_slice())?;

            if file.has_children() {
                self.walk_dir(
                    file.fd.as_ref().unwrap(),
                    file.nents_hint(),
                )?;
            } else if file.file_type == FileType::Regular {
                self.add_regular_file_contents(&file.fd.unwrap(), &file.st)?;
            }

            Ok(())
        })?;

        Ok(())
    }
}

fn header_from_stat(st: &libc::stat) -> (UstarHeader, PaxHeader) {
    let typ = st.st_mode & libc::S_IFMT;

    // PAX base is ustar format
    let mut header = UstarHeader::default();
    let mut pax_header = PaxHeader::new();
    header.set_mode(st.st_mode & !libc::S_IFMT).unwrap();
    if header.set_uid(st.st_uid).is_err() {
        pax_header.add_integer_field("uid", st.st_uid);
    }
    if header.set_gid(st.st_gid).is_err() {
        pax_header.add_integer_field("gid", st.st_gid);
    }
    header.set_entry_type(match typ {
        libc::S_IFDIR => TypeFlag::Directory,
        libc::S_IFREG => TypeFlag::Regular,
        libc::S_IFLNK => TypeFlag::Symlink,
        libc::S_IFCHR => TypeFlag::Char,
        libc::S_IFBLK => TypeFlag::Block,
        libc::S_IFIFO => TypeFlag::Fifo,
        _ => panic!("unsupported file type: {}", typ),
    });
    // ignore err: we always add mtime to PAX for nsec
    _ = header.set_mtime(st.st_mtime as u64);

    if typ == libc::S_IFBLK || typ == libc::S_IFCHR {
        let major = unsafe { libc::major(st.st_rdev) };
        let minor = unsafe { libc::minor(st.st_rdev) };
        if header.set_device_major(major).is_err() {
            pax_header.add_integer_field("SCHILY.devmajor", major);
        }
        if header.set_device_minor(minor).is_err() {
            pax_header.add_integer_field("SCHILY.devminor", minor);
        }
    } else if typ == libc::S_IFREG {
        // only regular files have a size
        if header.set_size(st.st_size as u64).is_err() {
            pax_header.add_integer_field("size", st.st_size as u64);
        }
    }

    (header, pax_header)
}

trait InodeFlagsExt {
    fn add_name(
        &self,
        names: &mut SmallVec<[&'static str; 1]>,
        name: &'static str,
        flag: InodeFlags,
    );

    fn pax_names(&self) -> SmallVec<[&'static str; 1]>;
}

impl InodeFlagsExt for InodeFlags {
    #[inline]
    fn add_name(
        &self,
        names: &mut SmallVec<[&'static str; 1]>,
        name: &'static str,
        flag: InodeFlags,
    ) {
        if self.contains(flag) {
            names.push(name);
        }
    }

    // returning a SmallVec is more efficient for the common 1-flag case: no string joining/allocation required
    fn pax_names(&self) -> SmallVec<[&'static str; 1]> {
        let mut names = SmallVec::<[&'static str; 1]>::new();

        // filter to flags that should be included in archives
        let fl = self.intersection(InodeFlags::ARCHIVE_FLAGS);

        // only include flags supported by bsdtar
        // https://github.com/libarchive/libarchive/blob/4b6dd229c6a931c641bc40ee6d59e99af15a9432/libarchive/archive_entry.c#L1885
        fl.add_name(&mut names, "sappnd", InodeFlags::APPEND);
        fl.add_name(&mut names, "noatime", InodeFlags::NOATIME);
        fl.add_name(&mut names, "compress", InodeFlags::COMPR);
        fl.add_name(&mut names, "nocow", InodeFlags::NOCOW);
        fl.add_name(&mut names, "nodump", InodeFlags::NODUMP);
        fl.add_name(&mut names, "dirsync", InodeFlags::DIRSYNC);
        fl.add_name(&mut names, "schg", InodeFlags::IMMUTABLE);
        fl.add_name(&mut names, "journal", InodeFlags::JOURNAL_DATA);
        fl.add_name(&mut names, "projinherit", InodeFlags::PROJINHERIT);
        fl.add_name(&mut names, "securedeletion", InodeFlags::SECRM);
        fl.add_name(&mut names, "sync", InodeFlags::SYNC);
        fl.add_name(&mut names, "tail", InodeFlags::NOTAIL);
        fl.add_name(&mut names, "topdir", InodeFlags::TOPDIR);
        fl.add_name(&mut names, "undel", InodeFlags::UNRM);

        names
    }
}

fn main() -> anyhow::Result<()> {
    let file = unsafe { File::from_raw_fd(1) };
    let mut writer = Encoder::new(file, 0)?;
    writer.multithread(2)?;
    // let mut writer = file;

    // add root dir
    let src_dir = std::env::args()
        .nth(1)
        .ok_or_else(|| anyhow!("missing src dir"))?;
    let root_dir = unsafe {
        OwnedFd::from_raw_fd(openat(
            None,
            Path::new(&src_dir),
            OFlag::O_RDONLY | OFlag::O_DIRECTORY | OFlag::O_CLOEXEC,
            Mode::empty(),
        )?)
    };
    let root_dir_st = fstat(&root_dir)?;

    // add entry for root dir
    let (mut header, pax_header) = header_from_stat(&root_dir_st);
    header.set_path(".".as_bytes()).unwrap();
    // TODO: mtime, fflags, xattrs
    if !pax_header.is_empty() {
        pax_header.write_to(&mut writer)?;
    }
    writer.write_all(header.as_bytes())?;

    // walk dirs
    let owned_ctx = OwnedTarContext::new()?;
    let mut ctx = TarContext::new(&mut writer, &owned_ctx.buffer_stack, &owned_ctx.path_stack);
    ctx.walk_dir(&root_dir, None)?;

    // terminate with 1024 zero bytes (2 zero blocks)
    writer.write_all(&TAR_PADDING)?;

    writer.finish()?;

    Ok(())
}
