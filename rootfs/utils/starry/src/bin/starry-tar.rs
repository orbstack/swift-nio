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
    path_stack::PathStack,
    sys::{
        file::{fstat, fstatat},
        getdents::for_each_getdents,
        inode_flags::InodeFlags,
        link::with_readlinkat,
        xattr::{for_each_flistxattr, with_fgetxattr},
    },
};
use zstd::Encoder;

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

// TODO: extension for sockets

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

fn add_regular_file(w: &mut impl Write, file: &OwnedFd, st: &libc::stat) -> anyhow::Result<()> {
    if st.st_blocks < st.st_size / 512 {
        // TODO: sparse file
    }

    // we must never write more than the size written to the header
    let mut buf: MaybeUninit<[u8; 65536]> = MaybeUninit::uninit();
    let mut rem = st.st_size as usize;
    loop {
        let limit = std::cmp::min(rem, 65536);
        let ret = unsafe { libc::read(file.as_raw_fd(), buf.as_mut_ptr() as *mut _, limit) };
        let n = Errno::result(ret)? as usize;
        if n == 0 {
            break;
        }

        let data = unsafe { std::slice::from_raw_parts(buf.as_mut_ptr() as *const u8, n) };
        w.write_all(data)?;
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
            w.write_all(&TAR_PADDING[..limit])?;
            rem -= limit;
            if rem == 0 {
                break;
            }
        }
    }

    // pad tar to 512 byte block
    let pad = 512 - (st.st_size % 512);
    if pad != 512 {
        w.write_all(&TAR_PADDING[..pad as usize])?;
    }

    Ok(())
}

fn walk_dir(w: &mut impl Write, dirfd: &OwnedFd, path_stack: &PathStack) -> anyhow::Result<()> {
    // TODO: error handling on a per-entry basis?
    for_each_getdents(dirfd, |entry| {
        let path = path_stack.push(entry.name.to_bytes());

        // TODO: minor optimization: we will open regular files and dirs anyway, so can fstat after open, instead of using a string here
        let st = fstatat(dirfd, entry.name, libc::AT_SYMLINK_NOFOLLOW)?;
        let typ = st.st_mode & libc::S_IFMT;
        if typ == libc::S_IFSOCK {
            // skip sockets
            return Ok(());
        }

        // PAX and normal header
        let mut header = header_from_stat(&st);
        let mut pax_header = PaxHeader::new();

        // nsecs mtime (skip invalid nsecs)
        if st.st_mtime_nsec != 0 && st.st_mtime_nsec < 1_000_000_000 {
            // "18446744073709551616.000000000" (u64::MAX + 9 digits for nanoseconds)
            let mut time_buf = SmallVec::<[u8; 30]>::new();
            let mut dec_buf = itoa::Buffer::new();
            let seconds = dec_buf.format(st.st_mtime);
            time_buf.extend_from_slice(seconds.as_bytes());
            time_buf.push(b'.');

            let nanos_start = time_buf.len();
            time_buf.resize(nanos_start + 9, 0);
            // can't overflow: we checked that nsec < 1e9
            write_left_padded(&mut time_buf[nanos_start..], st.st_mtime_nsec as u64, 10, 9)
                .unwrap();

            pax_header.add_field("mtime", &time_buf);
        }

        if header.set_path(path.get().as_slice()).is_err() {
            // PAX long name extension
            pax_header.add_field("path", path.get().as_slice());
        }

        if typ == libc::S_IFLNK {
            with_readlinkat(dirfd, entry.name, |link_name| {
                if header.set_link_path(link_name).is_err() {
                    // PAX long name extension
                    pax_header.add_field("linkpath", link_name);
                }
            })?;
        }

        let open_flags = match typ {
            libc::S_IFREG => OFlag::empty(),
            libc::S_IFDIR => OFlag::O_DIRECTORY,
            _ => OFlag::O_PATH,
        };

        // O_NONBLOCK: avoid hang if we race and end up opening a fifo
        // O_NOCTTY: avoid kill if we race and end up opening a tty
        let fd = unsafe {
            OwnedFd::from_raw_fd(openat(
                Some(dirfd.as_raw_fd()),
                entry.name,
                OFlag::O_RDONLY
                    | OFlag::O_CLOEXEC
                    | OFlag::O_NOFOLLOW
                    | OFlag::O_NONBLOCK
                    | OFlag::O_NOCTTY
                    | open_flags,
                Mode::empty(),
            )?)
        };

        // fflags
        if typ == libc::S_IFREG || typ == libc::S_IFDIR {
            let flags = InodeFlags::from_file(&fd)?;
            let flag_names = flags.names();
            match flag_names.len() {
                0 => {}
                // fastpath for common 1-flag case
                1 => pax_header.add_field("SCHILY.fflags", flag_names[0].as_bytes()),
                // multiple flags are rare
                _ => pax_header.add_field("SCHILY.fflags", flag_names.join(",").as_bytes()),
            }
        }

        // xattrs
        // TODO: /proc/self/fd/ llistxattr for other types
        if typ == libc::S_IFREG || typ == libc::S_IFDIR {
            for_each_flistxattr(&fd, |name| {
                with_fgetxattr(&fd, name, |value| {
                    // SCHILY.xattr is a violation of the PAX spec: PAX headers must be UTF-8
                    // LIBARCHIVE.xattr uses base64 to fix that, but it's not supported by GNU tar
                    // we use SCHILY: it works fine because PAX fields are length-prefixed
                    let mut pax_key: SmallVec<[u8; 64]> = b"SCHILY.xattr.".to_smallvec();
                    pax_key.extend_from_slice(name.to_bytes());
                    pax_header.add_field(&pax_key, value);
                })?;
                Ok(())
            })?;
        }

        if !pax_header.is_empty() {
            pax_header.write_to(w)?;
        }
        w.write_all(header.as_bytes())?;

        if typ == libc::S_IFDIR {
            walk_dir(w, &fd, path_stack)?;
        } else if typ == libc::S_IFREG {
            add_regular_file(w, &fd, &st)?;
        }

        Ok(())
    })?;

    Ok(())
}

fn header_from_stat(st: &libc::stat) -> UstarHeader {
    let typ = st.st_mode & libc::S_IFMT;

    // PAX base is ustar format
    let mut header = UstarHeader::default();
    header.set_mode(st.st_mode & !libc::S_IFMT).unwrap();
    // TODO: large uid
    header.set_uid(st.st_uid).unwrap();
    // TODO: large gid
    header.set_gid(st.st_gid).unwrap();
    header.set_entry_type(match typ {
        libc::S_IFDIR => TypeFlag::Directory,
        libc::S_IFREG => TypeFlag::Regular,
        libc::S_IFLNK => TypeFlag::Symlink,
        libc::S_IFCHR => TypeFlag::Char,
        libc::S_IFBLK => TypeFlag::Block,
        libc::S_IFIFO => TypeFlag::Fifo,
        _ => panic!("unsupported file type: {}", typ),
    });
    header.set_mtime(st.st_mtime as u64).unwrap();

    if typ == libc::S_IFBLK || typ == libc::S_IFCHR {
        // only fails if not supported by archive format
        // TODO: large dev
        header
            .set_device_major(unsafe { libc::major(st.st_rdev) })
            .unwrap();
        header
            .set_device_minor(unsafe { libc::minor(st.st_rdev) })
            .unwrap();
    } else if typ == libc::S_IFREG {
        // only regular files have a size
        header.set_size(st.st_size as u64).unwrap();
    }

    header
}

trait InodeFlagsExt {
    fn add_name(
        &self,
        names: &mut SmallVec<[&'static str; 1]>,
        name: &'static str,
        flag: InodeFlags,
    );

    fn names(&self) -> SmallVec<[&'static str; 1]>;
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
    fn names(&self) -> SmallVec<[&'static str; 1]> {
        let mut names = SmallVec::<[&'static str; 1]>::new();

        // only include flags supported by bsdtar
        // https://github.com/libarchive/libarchive/blob/4b6dd229c6a931c641bc40ee6d59e99af15a9432/libarchive/archive_entry.c#L1885
        self.add_name(&mut names, "sappnd", InodeFlags::APPEND);
        self.add_name(&mut names, "noatime", InodeFlags::NOATIME);
        // btrfs flags that are usually enabled globally on a FS level, so almost every file will have them
        // self.add_name(&mut names, "compress", InodeFlags::COMPR);
        // self.add_name(&mut names, "nocow", InodeFlags::NOCOW);
        self.add_name(&mut names, "nodump", InodeFlags::NODUMP);
        self.add_name(&mut names, "dirsync", InodeFlags::DIRSYNC);
        self.add_name(&mut names, "schg", InodeFlags::IMMUTABLE);
        self.add_name(&mut names, "journal", InodeFlags::JOURNAL_DATA);
        self.add_name(&mut names, "projinherit", InodeFlags::PROJINHERIT);
        self.add_name(&mut names, "securedeletion", InodeFlags::SECRM);
        self.add_name(&mut names, "sync", InodeFlags::SYNC);
        self.add_name(&mut names, "tail", InodeFlags::NOTAIL);
        self.add_name(&mut names, "topdir", InodeFlags::TOPDIR);
        self.add_name(&mut names, "undel", InodeFlags::UNRM);

        names
    }
}

fn main() -> anyhow::Result<()> {
    let file = unsafe { File::from_raw_fd(1) };
    // let mut writer = Encoder::new(file, 0)?;
    // writer.multithread(2)?;
    let mut writer = file;

    // add root dir
    let src_dir = std::env::args()
        .nth(1)
        .ok_or_else(|| anyhow!("missing src dir"))?;
    let root_dir = unsafe {
        OwnedFd::from_raw_fd(openat(
            None,
            Path::new(&src_dir),
            OFlag::O_RDONLY
                | OFlag::O_DIRECTORY
                | OFlag::O_CLOEXEC
                | OFlag::O_NONBLOCK
                | OFlag::O_NOCTTY,
            Mode::empty(),
        )?)
    };
    let root_dir_st = fstat(&root_dir)?;

    // add entry for root dir
    let mut header = header_from_stat(&root_dir_st);
    header.set_path(".".as_bytes()).unwrap();
    writer.write_all(header.as_bytes())?;

    // walk dirs
    let path_stack = PathStack::default();
    walk_dir(&mut writer, &root_dir, &path_stack)?;

    // terminate with 1024 zero bytes (2 zero blocks)
    writer.write_all(&TAR_PADDING)?;

    // writer.finish()?;

    Ok(())
}
