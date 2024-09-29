use std::{ffi::CStr, fs::File, io::{Read, Write}, mem::MaybeUninit, os::{fd::{AsRawFd, FromRawFd, OwnedFd}, unix::ffi::OsStrExt}, path::Path};

use anyhow::anyhow;
use nix::{errno::Errno, fcntl::{openat, readlinkat, OFlag}, sys::stat::Mode};
use starry::sys::{for_each_dir_entry};
use tar::{EntryType, Header};
use zstd::Encoder;

const TAR_PADDING: [u8; 1024] = [0; 1024];

const PAX_HEADER_NAME: &str = "@PaxHeader";

// TODO: extension for sockets

struct PaxHeader {
    header: Header,
    data: Vec<u8>,
}

impl PaxHeader {
    fn new() -> Self {
        // more compressible?
        // TODO: zeros instead?
        let mut header = Header::new_ustar();
        header.set_entry_type(EntryType::XHeader);
        // name="@PaxHeader": doesn't match bsdtar or GNU tar behavior, but is compliant and faster
        header.as_ustar_mut().unwrap().name[..PAX_HEADER_NAME.len()].copy_from_slice(PAX_HEADER_NAME.as_bytes());

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

    fn write_to(mut self, w: &mut impl Write) -> anyhow::Result<()> {
        self.header.set_size(self.data.len() as u64);
        self.header.set_cksum();
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

fn add_dir_children(w: &mut impl Write, dirfd: &OwnedFd, path_prefix: &Path) -> anyhow::Result<()> {
    for_each_dir_entry(dirfd, |entry| {
        // TODO: this is slow
        let path = path_prefix.join(entry.name.to_str().unwrap());
        // TODO: minor optimization: we will open regular files and dirs anyway, so can fstat after open, instead of using a string here
        let st = fstatat(dirfd, entry.name, libc::AT_SYMLINK_NOFOLLOW)?;

        let mut header = header_from_stat(&st).unwrap();

        // PAX header
        let mut pax_header = PaxHeader::new();

        // nsecs mtime
        pax_header.add_field("mtime", format!("{}.{:0>9}", st.st_mtime, st.st_mtime_nsec).as_bytes());

        // TODO: set_path is slow internally
        if header.set_path(&path).is_err() {
            // PAX long name extension
            pax_header.add_field("path", path.as_os_str().as_bytes());
        }

        let typ = st.st_mode & libc::S_IFMT;
        if typ == libc::S_IFLNK {
            let link_name = readlinkat(Some(dirfd.as_raw_fd()), entry.name)?;
            if header.set_link_name_literal(link_name.as_bytes()).is_err() {
                // PAX long name extension
                pax_header.add_field("linkpath", link_name.as_bytes());
            }
        }

        // TODO: sparse files

        // TODO: fflags

        // TODO: xattrs

        pax_header.write_to(w)?;

        header.set_cksum();
        w.write_all(header.as_bytes())?;

        if typ == libc::S_IFDIR {
            let child_dirfd = unsafe { OwnedFd::from_raw_fd(openat(Some(dirfd.as_raw_fd()), entry.name, OFlag::O_RDONLY | OFlag::O_DIRECTORY | OFlag::O_CLOEXEC | OFlag::O_NOFOLLOW | OFlag::O_NONBLOCK | OFlag::O_NOCTTY, Mode::empty())?) };
            add_dir_children(w, &child_dirfd, &path)?;
        } else if typ == libc::S_IFREG {
            // O_NONBLOCK: avoid hang if we race and end up opening a fifo
            // O_NOCTTY: avoid hang if we race and end up opening a tty
            let mut file = unsafe { File::from_raw_fd(openat(Some(dirfd.as_raw_fd()), entry.name, OFlag::O_RDONLY | OFlag::O_CLOEXEC | OFlag::O_NOFOLLOW | OFlag::O_NONBLOCK | OFlag::O_NOCTTY, Mode::empty())?) };

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

fn header_from_stat(st: &libc::stat) -> Option<Header> {
    let typ = st.st_mode & libc::S_IFMT;

    // PAX base is ustar format
    let mut header = Header::new_ustar();
    header.set_mode(st.st_mode);
    header.set_uid(st.st_uid as u64);
    header.set_gid(st.st_gid as u64);
    header.set_entry_type(match typ {
        libc::S_IFDIR => EntryType::Directory,
        libc::S_IFREG => EntryType::Regular,
        libc::S_IFLNK => EntryType::Symlink,
        libc::S_IFCHR => EntryType::Char,
        libc::S_IFBLK => EntryType::Block,
        libc::S_IFIFO => EntryType::Fifo,
        _ => return None,
    });
    header.set_mtime(st.st_mtime as u64);

    if typ == libc::S_IFBLK || typ == libc::S_IFCHR {
        // only fails if not supported by archive format
        header.set_device_major(unsafe { libc::major(st.st_rdev) }).unwrap();
        header.set_device_minor(unsafe { libc::minor(st.st_rdev) }).unwrap();
    } else if typ == libc::S_IFREG {
        // only regular files have a size
        header.set_size(st.st_size as u64);
    }

    Some(header)
}

fn main() -> anyhow::Result<()> {
    let file = unsafe { File::from_raw_fd(1) };
    let mut writer = Encoder::new(file, 0)?;
    writer.multithread(1)?;
    // let mut writer = file;

    // add root dir
    let src_dir = std::env::args().nth(1).ok_or_else(|| anyhow!("missing src dir"))?;
    let root_dir = unsafe { OwnedFd::from_raw_fd(openat(None, Path::new(&src_dir), OFlag::O_RDONLY | OFlag::O_DIRECTORY | OFlag::O_CLOEXEC | OFlag::O_NONBLOCK | OFlag::O_NOCTTY, Mode::empty())?) };
    let root_dir_st = fstat(&root_dir)?;

    // add entry for root dir
    let mut header = header_from_stat(&root_dir_st).unwrap();
    header.set_path(Path::new("."))?;
    header.set_cksum();
    writer.write_all(header.as_bytes())?;

    // walk dirs
    add_dir_children(&mut writer, &root_dir, Path::new(""))?;

    // terminate with 1024 zero bytes (2 zero blocks)
    writer.write_all(&TAR_PADDING)?;

    writer.finish()?;

    Ok(())
}
