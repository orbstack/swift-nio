use std::{cell::{Ref, RefCell}, cmp::min, ffi::CStr, fs::File, io::{Read, Write}, mem::MaybeUninit, os::{fd::{AsRawFd, FromRawFd, OwnedFd}, unix::ffi::OsStrExt}, path::Path};

use anyhow::anyhow;
use bytemuck::{Pod, Zeroable};
use nix::{errno::Errno, fcntl::{openat, readlinkat, OFlag}, sys::stat::Mode};
use starry::sys::for_each_dir_entry;
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

#[derive(Debug, thiserror::Error)]
pub enum TarError {
    #[error("path too long")]
    PathTooLong,
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
        header.data.version = *b"00";
        header
    }
}

impl UstarHeader {
    pub fn set_entry_type(&mut self, typ: TypeFlag) {
        self.data.typeflag = [typ as u8; 1];
    }

    pub fn set_path(&mut self, path: &[u8]) -> Result<(), TarError> {
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
                    // TODO
                }

                // get the prefix part of the string
                let prefix_path = &path[..min(path.len(), 155)];
                // split at last / in prefix
                let mut split_iter = prefix_path.rsplitn(2, |&c| c == b'/');
                let name = split_iter.next().ok_or(TarError::PathTooLong)?;
                let prefix = split_iter.next().ok_or(TarError::PathTooLong)?;
                if prefix.len() > 155 || name.len() > 100 {
                    // not splittable: path component is too long
                    return Err(TarError::PathTooLong);
                }
                // copy prefix
                self.data.prefix[..prefix.len()].copy_from_slice(prefix);
                // copy path
                self.data.name[..name.len()].copy_from_slice(name);
            }
            _ => return Err(TarError::PathTooLong),
        }
        Ok(())
    }

    pub fn set_link_path(&mut self, path: &[u8]) -> Result<(), TarError> {
        if path.len() > 100 {
            return Err(TarError::PathTooLong);
        }

        self.data.linkname[..path.len()].copy_from_slice(path);
        Ok(())
    }

    pub fn set_mode(&mut self, mode: u32) {
        write!(SliceBuf::new(&mut self.data.mode), "{:08o}", mode).unwrap();
    }

    pub fn set_uid(&mut self, uid: u32) {
        write!(SliceBuf::new(&mut self.data.uid), "{:08o}", uid).unwrap();
    }

    pub fn set_gid(&mut self, gid: u32) {
        write!(SliceBuf::new(&mut self.data.gid), "{:08o}", gid).unwrap();
    }

    pub fn set_size(&mut self, size: u64) {
        write!(SliceBuf::new(&mut self.data.size), "{:012o}", size).unwrap();
    }

    pub fn set_mtime(&mut self, mtime: u64) {
        write!(SliceBuf::new(&mut self.data.mtime), "{:012o}", mtime).unwrap();
    }

    pub fn set_device_major(&mut self, major: u32) {
        write!(SliceBuf::new(&mut self.data.devmajor), "{:08o}", major).unwrap();
    }

    pub fn set_device_minor(&mut self, minor: u32) {
        write!(SliceBuf::new(&mut self.data.devminor), "{:08o}", minor).unwrap();
    }

    fn set_checksum(&mut self) {
        // checksum = sum of all octets, with checksum field set to spaces
        self.data.chksum = [b' '; 8];

        // spec: must be at least 17 bits
        let mut sum: u32 = 0;
        for b in bytemuck::bytes_of(&self.data) {
            sum += *b as u32;
        }
        write!(SliceBuf::new(&mut self.data.chksum), "{:08o}", sum).unwrap();
    }

    pub fn as_bytes(&mut self) -> &[u8] {
        // calculate checksum
        self.set_checksum();

        bytemuck::bytes_of(&self.data)
    }
}

struct SliceBuf<'a> {
    buf: &'a mut [u8],
    off: usize,
}

impl<'a> SliceBuf<'a> {
    fn new(buf: &'a mut [u8]) -> Self {
        Self { buf, off: 0 }
    }

    pub fn get_used(&self) -> &[u8] {
        &self.buf[..self.off]
    }
}

impl<'a> Write for SliceBuf<'a> {
    fn write(&mut self, data: &[u8]) -> std::io::Result<usize> {
        let len = data.len();
        let p = &mut self.buf[self.off..];
        if p.len() < len {
            return Err(std::io::Error::new(std::io::ErrorKind::UnexpectedEof, "SliceBuf overflow"));
        }
        p[..len].copy_from_slice(data);
        self.off += len;
        Ok(len)
    }

    fn flush(&mut self) -> std::io::Result<()> {
        Ok(())
    }
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

    fn add_field(&mut self, key: &str, value: &[u8]) {
        // +3: space, equals, newline
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

        write!(self.data, "{} {}=", total_len, key).unwrap();
        self.data.extend_from_slice(value);
        self.data.push(b'\n');
    }

    fn is_empty(&self) -> bool {
        self.data.is_empty()
    }

    fn write_to(mut self, w: &mut impl Write) -> anyhow::Result<()> {
        self.header.set_size(self.data.len() as u64);
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

struct PathStack {
    inner: RefCell<Vec<u8>>,
}

impl PathStack {
    fn new() -> Self {
        Self {
            inner: RefCell::new(Vec::with_capacity(1024)),
        }
    }

    fn push(&self, segment: &[u8]) -> PathStackGuard {
        let mut buf = self.inner.borrow_mut();
        let old_len = buf.len();
        if !buf.is_empty() {
            buf.push(b'/');
        }
        buf.extend_from_slice(segment);
        PathStackGuard { stack: self, old_len }
    }
}

struct PathStackGuard<'a> {
    stack: &'a PathStack,
    old_len: usize,
}

impl<'a> PathStackGuard<'a> {
    pub fn get(&self) -> Ref<'_, Vec<u8>> {
        self.stack.inner.borrow()
    }
}

impl<'a> Drop for PathStackGuard<'a> {
    fn drop(&mut self) {
        self.stack.inner.borrow_mut().truncate(self.old_len);
    }
}

fn add_regular_file(w: &mut impl Write, file: &mut File, st: &libc::stat) -> anyhow::Result<()> {
    // we must never write more than the size written to the header
    let buf: MaybeUninit<[u8; 65536]> = MaybeUninit::uninit();
    let mut buf = unsafe { buf.assume_init() };
    let mut rem = st.st_size as usize;
    loop {
        let n = file.read(&mut buf[..std::cmp::min(rem, 65536)])?;
        if n == 0 {
            break;
        }
        w.write_all(&buf[..n])?;
        rem -= n;
        if rem == 0 {
            break;
        }
    }

    // pad tar to 512 byte block
    let pad = 512 - (st.st_size % 512);
    if pad != 512 {
        w.write_all(&TAR_PADDING[..pad as usize])?;
    }

    Ok(())
}

fn add_dir_children(w: &mut impl Write, dirfd: &OwnedFd, path_stack: &PathStack) -> anyhow::Result<()> {
    for_each_dir_entry(dirfd, |entry| {
        let path = path_stack.push(entry.name.to_bytes());

        // TODO: minor optimization: we will open regular files and dirs anyway, so can fstat after open, instead of using a string here
        let st = fstatat(dirfd, entry.name, libc::AT_SYMLINK_NOFOLLOW)?;

        let mut header = header_from_stat(&st).unwrap();

        // PAX header
        let mut pax_header = PaxHeader::new();

        // nsecs mtime (skip invalid nsecs)
        if st.st_mtime_nsec != 0 && st.st_mtime_nsec < 1_000_000_000 {
            // "18446744073709551616.000000000" (u64::MAX + 9 digits for nanoseconds)
            let mut _time_buf: [u8; 30] = [0; 30];
            let mut time_buf = SliceBuf::new(&mut _time_buf);
            write!(time_buf, "{}.{:0>9}", st.st_mtime, st.st_mtime_nsec).unwrap();
            pax_header.add_field("mtime", time_buf.get_used());
        }

        if header.set_path(path.get().as_slice()).is_err() {
            // PAX long name extension
            pax_header.add_field("path", path.get().as_slice());
        }

        let typ = st.st_mode & libc::S_IFMT;
        if typ == libc::S_IFLNK {
            // TODO: remove malloc
            let link_name = readlinkat(Some(dirfd.as_raw_fd()), entry.name)?;
            if header.set_link_path(link_name.as_bytes()).is_err() {
                // PAX long name extension
                pax_header.add_field("linkpath", link_name.as_bytes());
            }
        }

        // TODO: sparse files

        // TODO: fflags

        // TODO: xattrs

        if !pax_header.is_empty() {
            pax_header.write_to(w)?;
        }

        w.write_all(header.as_bytes())?;

        if typ == libc::S_IFDIR {
            let child_dirfd = unsafe { OwnedFd::from_raw_fd(openat(Some(dirfd.as_raw_fd()), entry.name, OFlag::O_RDONLY | OFlag::O_DIRECTORY | OFlag::O_CLOEXEC | OFlag::O_NOFOLLOW | OFlag::O_NONBLOCK | OFlag::O_NOCTTY, Mode::empty())?) };
            add_dir_children(w, &child_dirfd, path_stack)?;
        } else if typ == libc::S_IFREG {
            // O_NONBLOCK: avoid hang if we race and end up opening a fifo
            // O_NOCTTY: avoid hang if we race and end up opening a tty
            let mut file = unsafe { File::from_raw_fd(openat(Some(dirfd.as_raw_fd()), entry.name, OFlag::O_RDONLY | OFlag::O_CLOEXEC | OFlag::O_NOFOLLOW | OFlag::O_NONBLOCK | OFlag::O_NOCTTY, Mode::empty())?) };
            add_regular_file(w, &mut file, &st)?;
        }

        Ok(())
    })?;

    Ok(())
}

fn fstat<F: AsRawFd>(fd: &F) -> nix::Result<libc::stat> {
    let fd = fd.as_raw_fd();
    let mut st = MaybeUninit::uninit();
    let ret = unsafe { libc::fstat(fd, st.as_mut_ptr()) };
    Errno::result(ret).map(|_| unsafe { st.assume_init() })
}

fn fstatat<F: AsRawFd>(dirfd: &F, path: &CStr, flags: i32) -> nix::Result<libc::stat> {
    let dirfd = dirfd.as_raw_fd();
    let mut st = MaybeUninit::uninit();
    let ret = unsafe { libc::fstatat(dirfd, path.as_ptr(), st.as_mut_ptr(), flags) };
    Errno::result(ret).map(|_| unsafe { st.assume_init() })
}

fn header_from_stat(st: &libc::stat) -> Option<UstarHeader> {
    let typ = st.st_mode & libc::S_IFMT;

    // PAX base is ustar format
    let mut header = UstarHeader::default();
    // TODO: not supposed to include S_IFMT
    header.set_mode(st.st_mode);
    // TODO: large uid
    header.set_uid(st.st_uid);
    // TODO: large gid
    header.set_gid(st.st_gid);
    header.set_entry_type(match typ {
        libc::S_IFDIR => TypeFlag::Directory,
        libc::S_IFREG => TypeFlag::Regular,
        libc::S_IFLNK => TypeFlag::Symlink,
        libc::S_IFCHR => TypeFlag::Char,
        libc::S_IFBLK => TypeFlag::Block,
        libc::S_IFIFO => TypeFlag::Fifo,
        _ => return None,
    });
    header.set_mtime(st.st_mtime as u64);

    if typ == libc::S_IFBLK || typ == libc::S_IFCHR {
        // only fails if not supported by archive format
        // TODO: large dev
        header.set_device_major(unsafe { libc::major(st.st_rdev) });
        header.set_device_minor(unsafe { libc::minor(st.st_rdev) });
    } else if typ == libc::S_IFREG {
        // only regular files have a size
        header.set_size(st.st_size as u64);
    }

    Some(header)
}

fn main() -> anyhow::Result<()> {
    let file = unsafe { File::from_raw_fd(1) };
    // let mut writer = Encoder::new(file, 0)?;
    // writer.multithread(1)?;
    let mut writer = file;

    // add root dir
    let src_dir = std::env::args().nth(1).ok_or_else(|| anyhow!("missing src dir"))?;
    let root_dir = unsafe { OwnedFd::from_raw_fd(openat(None, Path::new(&src_dir), OFlag::O_RDONLY | OFlag::O_DIRECTORY | OFlag::O_CLOEXEC | OFlag::O_NONBLOCK | OFlag::O_NOCTTY, Mode::empty())?) };
    let root_dir_st = fstat(&root_dir)?;

    // add entry for root dir
    let mut header = header_from_stat(&root_dir_st).unwrap();
    header.set_path(".".as_bytes()).unwrap();
    writer.write_all(header.as_bytes())?;

    // walk dirs
    let path_stack = PathStack::new();
    add_dir_children(&mut writer, &root_dir, &path_stack)?;

    // terminate with 1024 zero bytes (2 zero blocks)
    writer.write_all(&TAR_PADDING)?;

    // writer.finish()?;

    Ok(())
}
