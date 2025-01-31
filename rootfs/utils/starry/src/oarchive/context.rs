/*
 * starry oar: "Orb ARchive"
 *
 * same feature set as starry tar, but with a custom format that tries to be more space-efficient and thus hopefully less taxing on the compressor, since we're bottlenecked on zstd.
 *
 * this is a good starting point for thinking about good archive format extensions, but currently, it's not meaningfully better than tar. *very* slightly smaller and faster, but barely.
 */

use std::collections::btree_map::Entry;
use std::ffi::CStr;
use std::os::fd::AsRawFd;
use std::{collections::BTreeMap, io::Write, mem::MaybeUninit, os::fd::OwnedFd};

use bumpalo::Bump;
use nix::errno::Errno;
use nix::unistd::{lseek, Whence};

use crate::interrogate::InterrogatedFile;
use crate::recurse::Recurser;
use crate::sys::getdents::{DirEntry, FileType};
use crate::sys::inode_flags::InodeFlags;
use crate::tarball::sparse::SparseFileMap;
use crate::{interrogate::DevIno, path_stack::PathStack};

use super::wire::{self, EntryHeader, EntryType};

const READ_BUF_SIZE: usize = 65536;

pub const ZERO_PADDING: [u8; 1024] = [0; 1024];

pub struct OwnedArchiveContext {
    path_stack: PathStack,
    bump: Bump,
    recurser: Recurser,
}

impl OwnedArchiveContext {
    pub fn new() -> anyhow::Result<Self> {
        Ok(Self {
            path_stack: PathStack::default(),
            bump: Bump::new(),
            recurser: Recurser::new()?,
        })
    }
}

pub struct ArchiveContext<'a, W: Write> {
    writer: W,

    // we use [u8] for all paths instead of String because Linux paths technically don't have to be UTF-8. (it also means we can avoid UTF-8 validation overhead)
    hardlink_paths: BTreeMap<DevIno, &'a [u8]>,
    bump: &'a Bump,

    // this owned/ref split allows &mut self (for Write) without preventing these from being borrowed
    recurser: &'a Recurser,
    path_stack: &'a PathStack,
}

impl<'a, W: Write> ArchiveContext<'a, W> {
    pub fn new(writer: W, owned: &'a OwnedArchiveContext) -> Self {
        Self {
            writer,
            hardlink_paths: BTreeMap::new(),
            bump: &owned.bump,
            path_stack: &owned.path_stack,
            recurser: &owned.recurser,
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
                let limit = std::cmp::min(rem, ZERO_PADDING.len());
                self.writer.write_all(&ZERO_PADDING[..limit])?;
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
        apparent_size: u64,
        header: &mut EntryHeader,
    ) -> anyhow::Result<bool> {
        // make a sparse map before altering any header
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

        // update type in header
        header.file_type = EntryType::RegularSparse;
        header.data_size = apparent_size;

        // commit header
        header.write_to(&mut self.writer)?;

        let mut off: i64 = 0;
        while off < apparent_size as i64 {
            let data_start = match lseek(fd.as_raw_fd(), off, Whence::SeekData) {
                Ok(data_start) => data_start,
                // file has no (more) data
                Err(Errno::ENXIO) => break,
                Err(e) => return Err(e.into()),
            };
            let hole_start = match lseek(fd.as_raw_fd(), data_start, Whence::SeekHole) {
                Ok(hole_start) => hole_start,
                // file got smaller
                Err(Errno::ENXIO) => break,
                Err(e) => return Err(e.into()),
            };
            let data_len = hole_start - data_start;

            // write header
            let sparse_header = wire::SparseHeader {
                offset: data_start as u64,
                length: data_len as u64,
            };
            sparse_header.write_to(&mut self.writer)?;

            self.write_from_fd(fd, data_start, data_len as usize)?;

            off = hole_start;
        }

        Ok(true)
    }

    fn finish_regular_file(
        &mut self,
        fd: &OwnedFd,
        file: &InterrogatedFile,
        header: &mut EntryHeader,
    ) -> anyhow::Result<()> {
        // file that uses fewer blocks than it should is either sparse or compressed
        if file.is_maybe_sparse() {
            // if not sparse (e.g. compressed), continue to normal path
            let is_sparse = self.finish_sparse_file(fd, file.apparent_size(), header)?;
            if is_sparse {
                return Ok(());
            }
        }

        // commit header
        header.data_size = file.apparent_size();
        header.write_to(&mut self.writer)?;

        // we must never write more than the expected size in the header
        self.write_from_fd(fd, 0, file.apparent_size() as usize)?;

        Ok(())
    }

    fn populate_inode_entry(
        &mut self,
        file: &InterrogatedFile,
        path: &[u8],
        header: &mut EntryHeader,
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
                    header.file_type = EntryType::Hardlink;

                    // set linkpath
                    let old_path = o.get();
                    header.data_size = old_path.len() as u64;
                    header.write_to(&mut self.writer)?;
                    self.writer.write_all(old_path)?;

                    // skip adding rest of inode data
                    return Ok(());
                }
            }
        }

        // fflags
        let flags = file.inode_flags()?;
        if flags.contains(InodeFlags::IMMUTABLE) {
            header.flags.0 |= wire::FileFlags::INODE_IMMUTABLE;
        }
        if flags.contains(InodeFlags::APPEND) {
            header.flags.0 |= wire::FileFlags::INODE_APPEND_ONLY;
        }
        if flags.contains(InodeFlags::NODUMP) {
            header.flags.0 |= wire::FileFlags::INODE_NODUMP;
        }

        // xattrs
        file.for_each_xattr(|key, value| {
            header
                .xattrs
                .push((key.to_bytes().to_vec(), value.to_vec()));
            Ok(())
        })?;

        Ok(())
    }

    pub fn add_one_entry(&mut self, file: &InterrogatedFile, path: &[u8]) -> anyhow::Result<()> {
        // make PAX and normal header with basic stat info
        // PAX base is ustar format
        let mut header = EntryHeader {
            mode: file.permissions().bits() as u16,
            uid: file.uid(),
            gid: file.gid(),
            file_type: match file.file_type {
                FileType::Directory => EntryType::Directory,
                FileType::Regular => EntryType::Regular,
                FileType::Symlink => EntryType::Symlink,
                FileType::Char => EntryType::Char,
                FileType::Block => EntryType::Block,
                FileType::Fifo => EntryType::Fifo,
                FileType::Socket => EntryType::Socket,
                FileType::Unknown => unreachable!(),
            },
            ..Default::default()
        };

        let (sec, nsec) = file.atime();
        header.atime = wire::TimeSpec::new(sec, nsec);

        let (sec, nsec) = file.mtime();
        header.mtime = wire::TimeSpec::new(sec, nsec);

        // everything else only needs to be set on actual inodes, so hardlinks don't need them
        self.populate_inode_entry(file, path, &mut header)?;

        header.path = path.to_vec();
        match header.file_type {
            EntryType::Regular => {
                self.finish_regular_file(file.fd.as_ref().unwrap(), file, &mut header)?;
            }

            // block/char devices: add major/minor
            EntryType::Block | EntryType::Char => {
                let (major, minor) = file.device_major_minor();
                header.data_size = 8; // TODO
                header.write_to(&mut self.writer)?;

                let dev_info = wire::DeviceInfo { major, minor };
                dev_info.write_to(&mut self.writer)?;
            }

            EntryType::Symlink => {
                file.with_readlink(|link_name| {
                    header.data_size = link_name.len() as u64;
                    header.write_to(&mut self.writer)?;
                    self.writer.write_all(link_name)?;
                    Ok::<(), anyhow::Error>(())
                })??;
            }

            // already written out by populate_inode_entry
            EntryType::Hardlink => {}

            _ => {
                header.write_to(&mut self.writer)?;
            }
        }

        Ok(())
    }

    fn do_one_entry(&mut self, dirfd: &OwnedFd, entry: &DirEntry) -> anyhow::Result<()> {
        let path = self.path_stack.push(entry.name.to_bytes());

        let file = InterrogatedFile::from_entry(dirfd, entry)?;
        self.add_one_entry(&file, path.get().as_slice())?;

        if file.has_children() {
            self.walk_dir(file.fd.as_ref().unwrap(), file.nents_hint())?;
        }

        Ok(())
    }

    pub fn walk_dir(&mut self, dirfd: &OwnedFd, nents_hint: Option<usize>) -> anyhow::Result<()> {
        self.recurser
            .walk_dir(dirfd, nents_hint, |entry| self.do_one_entry(dirfd, entry))
    }

    pub fn walk_dir_root(
        &mut self,
        dirfd: &OwnedFd,
        path: &CStr,
        nents_hint: Option<usize>,
    ) -> anyhow::Result<()> {
        self.recurser
            .walk_dir_root(dirfd, path, nents_hint, |entry| {
                self.do_one_entry(dirfd, entry)
            })
    }
}
