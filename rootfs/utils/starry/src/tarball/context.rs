use std::collections::btree_map::Entry;
use std::ffi::OsStr;
use std::os::fd::AsRawFd;
use std::os::unix::ffi::OsStrExt;
use std::path::PathBuf;
use std::{collections::BTreeMap, io::Write, mem::MaybeUninit, os::fd::OwnedFd};

use bumpalo::Bump;
use nix::errno::Errno;
use nix::unistd::{lseek, Whence};
use smallvec::{SmallVec, ToSmallVec};

use crate::interrogate::InterrogatedFile;
use crate::sys::getdents::{for_each_getdents, FileType};
use crate::{buffer_stack::BufferStack, interrogate::DevIno, path_stack::PathStack};

use super::headers::Headers;
use super::inode_flags::InodeFlagsExt;
use super::sparse::SparseFileMap;
use super::ustar::TypeFlag;

const READ_BUF_SIZE: usize = 65536;

pub const TAR_PADDING: [u8; 1024] = [0; 1024];

pub struct OwnedTarContext {
    buffer_stack: BufferStack,
    path_stack: PathStack,
    bump: Bump,
}

impl OwnedTarContext {
    pub fn new() -> anyhow::Result<Self> {
        Ok(Self {
            buffer_stack: BufferStack::new()?,
            path_stack: PathStack::default(),
            bump: Bump::new(),
        })
    }
}

pub struct TarContext<'a, W: Write> {
    writer: W,

    // we use [u8] for all paths instead of String because Linux paths technically don't have to be UTF-8. (it also means we can avoid UTF-8 validation overhead)
    hardlink_paths: BTreeMap<DevIno, &'a [u8]>,
    bump: &'a Bump,

    // this owned/ref split allows &mut self (for Write) without preventing these from being borrowed
    buffer_stack: &'a BufferStack,
    path_stack: &'a PathStack,
}

impl<'a, W: Write> TarContext<'a, W> {
    pub fn new(writer: W, owned: &'a OwnedTarContext) -> Self {
        Self {
            writer,
            hardlink_paths: BTreeMap::new(),
            bump: &owned.bump,
            buffer_stack: &owned.buffer_stack,
            path_stack: &owned.path_stack,
        }
    }

    // if we've committed to a certain size (in tar header or sparse file map), then we must write exactly that many bytes in order to avoid corrupting the archive
    // so this stops reading early if we reach rem, and pads with zeros if the file got smaller
    fn write_from_fd(&mut self, fd: &OwnedFd, mut off: i64, mut rem: usize) -> anyhow::Result<()> {
        let mut buf: MaybeUninit<[u8; READ_BUF_SIZE]> = MaybeUninit::uninit();
        while rem > 0 {
            // pread lets us save lseek calls in sparse files
            let limit = std::cmp::min(rem, READ_BUF_SIZE);
            let ret =
                unsafe { libc::pread(fd.as_raw_fd(), buf.as_mut_ptr() as *mut _, limit, off) };
            let n = Errno::result(ret)? as usize;
            if n == 0 {
                break;
            }

            let data = unsafe { std::slice::from_raw_parts(buf.as_mut_ptr() as *const u8, n) };
            self.writer.write_all(data)?;
            rem -= n;

            off += n as i64;
        }

        // pad with zeros if we didn't write enough (file size truncated)
        if rem > 0 {
            eprintln!("file truncated; padding with {} bytes", rem);
            loop {
                let limit = std::cmp::min(rem, TAR_PADDING.len());
                self.writer.write_all(&TAR_PADDING[..limit])?;
                rem -= limit;
                if rem == 0 {
                    break;
                }
            }
        }

        Ok(())
    }

    fn finish_sparse_file(
        &mut self,
        fd: &OwnedFd,
        path: &[u8],
        apparent_size: u64,
        headers: &mut Headers,
    ) -> anyhow::Result<bool> {
        // make a sparse map before altering any headers
        let mut map = SparseFileMap::default();

        // construct map by seeking
        // sadly, we have to do two passes: (1) map and (2) copy payload chunks
        let mut off: i64 = 0;
        while off < apparent_size as i64 {
            off = match lseek(fd.as_raw_fd(), off, Whence::SeekData) {
                Ok(off) => off,
                // EOF (raced and st_size has changed?)
                Err(Errno::ENXIO) => break,
                Err(e) => return Err(e.into()),
            };

            let end_off = match lseek(fd.as_raw_fd(), off, Whence::SeekHole) {
                Ok(off) => off,
                // EOF (raced and st_size has changed?)
                Err(Errno::ENXIO) => break,
                Err(e) => return Err(e.into()),
            };
            map.add(off as u64, (end_off - off) as u64);

            off = end_off;
        }

        // bail if the file actually ended up being contiguous
        if map.is_contiguous(apparent_size) {
            return Ok(false);
        }

        // with SEEK_HOLE, there's an empty hole at the end of the file. GNU tar doesn't extract properly without this
        if off < apparent_size as i64 {
            map.add(apparent_size, 0);
        }

        // set GNU PAX fields
        headers.pax.add_field("GNU.sparse.major", b"1");
        headers.pax.add_field("GNU.sparse.minor", b"0");
        headers.pax.add_field("GNU.sparse.name", path);
        headers
            .pax
            .add_integer_field("GNU.sparse.realsize", apparent_size);

        // path = $DIR/GNUSparseFile.0/$FILE
        let mut path = PathBuf::from(OsStr::from_bytes(path));
        let name = path.file_name().unwrap().to_os_string();
        path.pop();
        path.push("GNUSparseFile.0");
        path.push(name);
        headers.set_path(path.as_os_str().as_bytes());

        // serialize map and set payload size, including map
        let serialized_map = map.serialize();
        let total_entry_size = serialized_map.len().align_up(512) as u64 + map.payload_bytes();
        headers.set_size(total_entry_size);

        // commit headers
        headers.write_to(&mut self.writer)?;

        // write map
        self.writer.write_all(serialized_map.as_bytes())?;
        // pad to 512 byte block
        let pad = 512 - (serialized_map.len() % 512);
        if pad != 512 {
            self.writer.write_all(&TAR_PADDING[..pad])?;
        }

        // follow the map and write out payload chunks
        for entry in map.iter_entries() {
            self.write_from_fd(fd, entry.offset as i64, entry.len as usize)?;
        }

        // pad to 512 byte block
        let pad = 512 - (total_entry_size % 512);
        if pad != 512 {
            self.writer.write_all(&TAR_PADDING[..pad as usize])?;
        }

        Ok(true)
    }

    fn finish_regular_file(
        &mut self,
        fd: &OwnedFd,
        path: &[u8],
        file: &InterrogatedFile,
        headers: &mut Headers,
    ) -> anyhow::Result<()> {
        // file that uses fewer blocks than it should is either sparse or compressed
        if file.is_maybe_sparse() {
            // if not sparse (e.g. compressed), continue to normal path
            let is_sparse = self.finish_sparse_file(fd, path, file.apparent_size(), headers)?;
            if is_sparse {
                return Ok(());
            }
        }

        // commit headers
        headers.set_path(path);
        headers.set_size(file.apparent_size());
        headers.write_to(&mut self.writer)?;

        // we must never write more than the expected size in the header
        self.write_from_fd(fd, 0, file.apparent_size() as usize)?;

        // pad tar to 512 byte block
        let pad = 512 - (file.apparent_size() % 512);
        if pad != 512 {
            self.writer.write_all(&TAR_PADDING[..pad as usize])?;
        }

        Ok(())
    }

    fn populate_inode_entry(
        &mut self,
        file: &InterrogatedFile,
        path: &[u8],
        headers: &mut Headers,
    ) -> anyhow::Result<()> {
        // record hard link?
        // preserving hardlinks to char/block/fifo/symlink files seems useless (doesn't really save space in the tar since it still occupies a full header), but it is actually required for correctness: despite the major/minor/linkpath being immutable, the st_nlink/ctime/mtime/atime/xattrs/mode/uid/gid should still be synced
        if file.is_hardlink() {
            match self.hardlink_paths.entry(file.dev_ino()) {
                Entry::Vacant(v) => {
                    // this is the first time we've seen this dev/ino
                    // add current path to hardlink map and continue adding file contents to the archive
                    // this (sadly) allocates, but it's a slowpath for st_nlink>1: hardlinks are rare, and we optimize it with bump allocation when we do need it
                    v.insert(self.bump.alloc_slice_copy(path));
                }
                Entry::Occupied(o) => {
                    // not the first time! record this as a link
                    headers.set_entry_type(TypeFlag::HardLink);

                    // set linkpath
                    let old_path = o.get();
                    headers.set_link_path(old_path);

                    // skip adding rest of inode data
                    return Ok(());
                }
            }
        }

        // block/char devices: add major/minor
        if file.file_type == FileType::Block || file.file_type == FileType::Char {
            let (major, minor) = file.device_major_minor();
            headers.set_device_major(major);
            headers.set_device_minor(minor);
        }

        if file.file_type == FileType::Symlink {
            file.with_readlink(|link_name| {
                headers.set_link_path(link_name);
            })?;
        }

        // fflags
        let flags = file.inode_flags()?;
        if !flags.is_empty() {
            let flag_names = flags.pax_names();
            match flag_names.len() {
                0 => {}
                // fastpath for common 1-flag case
                1 => headers
                    .pax
                    .add_field("SCHILY.fflags", flag_names[0].as_bytes()),
                // multiple flags are rare
                _ => headers
                    .pax
                    .add_field("SCHILY.fflags", flag_names.join(",").as_bytes()),
            }
        }

        // xattrs
        file.for_each_xattr(|key, value| {
            // SCHILY.xattr is a violation of the PAX spec: PAX headers must be UTF-8
            // LIBARCHIVE.xattr uses base64 to fix that, but it's not supported by GNU tar
            // we use SCHILY: it works fine because PAX fields are length-prefixed
            let mut pax_key: SmallVec<[u8; 128]> = b"SCHILY.xattr.".to_smallvec();
            pax_key.extend_from_slice(key.to_bytes());
            headers.pax.add_field(&pax_key, value);
            Ok(())
        })?;

        Ok(())
    }

    pub fn add_one_entry(&mut self, file: &InterrogatedFile, path: &[u8]) -> anyhow::Result<()> {
        // make PAX and normal header with basic stat info
        // PAX base is ustar format
        let mut headers = Headers::default();
        headers.set_mode(file.permissions().bits()).unwrap();
        headers.set_uid(file.uid());
        headers.set_gid(file.gid());
        headers.set_entry_type(match file.file_type {
            FileType::Directory => TypeFlag::Directory,
            FileType::Regular => TypeFlag::Regular,
            FileType::Symlink => TypeFlag::Symlink,
            FileType::Char => TypeFlag::Char,
            FileType::Block => TypeFlag::Block,
            FileType::Fifo => TypeFlag::Fifo,
            // skip archiving sockets; tar can't extract them
            // TODO: non-standard extension for this?
            FileType::Socket => return Ok(()),
            FileType::Unknown => unreachable!(),
        });
        let (sec, nsec) = file.mtime();
        headers.set_mtime(sec, nsec);

        // everything else only needs to be set on actual inodes, so hardlinks don't need them
        self.populate_inode_entry(file, path, &mut headers)?;

        if file.file_type == FileType::Regular {
            self.finish_regular_file(file.fd.as_ref().unwrap(), path, file, &mut headers)?;
        } else {
            headers.set_path(path);
            headers.write_to(&mut self.writer)?;
        }

        Ok(())
    }

    pub fn walk_dir(&mut self, dirfd: &OwnedFd, nents_hint: Option<usize>) -> anyhow::Result<()> {
        for_each_getdents(dirfd, nents_hint, self.buffer_stack, |entry| {
            let path = self.path_stack.push(entry.name.to_bytes());

            let file = InterrogatedFile::from_entry(dirfd, &entry)?;
            self.add_one_entry(&file, path.get().as_slice())?;

            if file.has_children() {
                self.walk_dir(file.fd.as_ref().unwrap(), file.nents_hint())?;
            }

            Ok(())
        })
    }
}

trait IntegerExt {
    fn align_up(self, align: Self) -> Self;
}

impl IntegerExt for usize {
    fn align_up(self, align: Self) -> Self {
        self.div_ceil(align) * align
    }
}
