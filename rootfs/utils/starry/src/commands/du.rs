/*
 * starry du
 * similar to `du -s`
 *
 * features:
 * - safe against symlink races (everything is dirfd/O_NOFOLLOW)
 * - doesn't fail the entire operation on deletion races
 * - easily parsable output when entire directories fail due to the source dir being deleted entirely
 *
 * assumptions:
 * - source MAY be modified concurrently. if so, results may be inconsistent, but there is no security risk, and the command will return a best-effort result.
 */

use std::{
    collections::BTreeSet,
    os::fd::{AsRawFd, FromRawFd, OwnedFd},
    path::Path,
};

use crate::{
    recurse::Recurser,
    sys::{
        file::fstatat,
        getdents::{DirEntry, FileType},
    },
};
use nix::{
    errno::Errno,
    fcntl::{openat, OFlag},
    sys::stat::Mode,
};

struct OwnedDuContext {
    recurser: Recurser,
}

impl OwnedDuContext {
    pub fn new() -> anyhow::Result<Self> {
        Ok(Self {
            recurser: Recurser::new()?,
        })
    }
}

struct DuContext<'a> {
    total_st_blocks: u64,
    seen_inodes: BTreeSet<u64>,
    recurser: &'a Recurser,
}

impl<'a> DuContext<'a> {
    fn new(owned: &'a OwnedDuContext) -> Self {
        Self {
            total_st_blocks: 0,
            seen_inodes: BTreeSet::new(),
            recurser: &owned.recurser,
        }
    }

    fn do_one_entry(&mut self, dirfd: &OwnedFd, entry: &DirEntry) -> anyhow::Result<()> {
        // don't double-count hard links
        // we can avoid statting directories entirely if we assume st_dev never changes
        if !self.seen_inodes.insert(entry.inode) {
            return Ok(());
        }

        if entry.file_type != FileType::Directory {
            let st = match fstatat(dirfd, entry.name, libc::AT_SYMLINK_NOFOLLOW) {
                Ok(st) => st,
                // ENOENT = race: file or dir was deleted
                Err(Errno::ENOENT) => return Ok(()),
                Err(e) => return Err(e.into()),
            };
            self.total_st_blocks += st.st_blocks as u64;

            if st.st_mode & libc::S_IFMT != libc::S_IFDIR {
                return Ok(());
            }
        }

        // optimization: directories always have st_blocks=0?
        let child_dirfd = match openat(
            Some(dirfd.as_raw_fd()),
            entry.name,
            OFlag::O_RDONLY | OFlag::O_DIRECTORY | OFlag::O_CLOEXEC | OFlag::O_NOFOLLOW,
            Mode::empty(),
        ) {
            Ok(fd) => fd,
            // ENOENT = race: file or dir was deleted
            Err(Errno::ENOENT) => return Ok(()),
            Err(e) => return Err(e.into()),
        };
        let child_dirfd = unsafe { OwnedFd::from_raw_fd(child_dirfd) };
        self.walk_dir(&child_dirfd)?;

        Ok(())
    }

    fn walk_dir(&mut self, dirfd: &OwnedFd) -> anyhow::Result<()> {
        self.recurser
            .walk_dir(dirfd, |entry| self.do_one_entry(dirfd, entry))?;
        Ok(())
    }
}

fn do_one_dir(src_dir: &str) -> anyhow::Result<()> {
    // open root dir
    let root_dirfd = match openat(
        None,
        Path::new(&src_dir),
        OFlag::O_RDONLY | OFlag::O_DIRECTORY | OFlag::O_CLOEXEC,
        Mode::empty(),
    ) {
        Ok(fd) => fd,
        // ENOENT = race: root dir was deleted
        Err(Errno::ENOENT) => {
            println!("0\t{}", src_dir);
            return Ok(());
        }
        Err(e) => return Err(e.into()),
    };
    let root_dirfd = unsafe { OwnedFd::from_raw_fd(root_dirfd) };

    // walk dirs
    let owned_ctx = OwnedDuContext::new()?;
    let mut ctx = DuContext::new(&owned_ctx);
    ctx.walk_dir(&root_dirfd)?;

    // report in KiB
    println!("{}\t{}", ctx.total_st_blocks * 512 / 1024, src_dir);
    Ok(())
}

pub fn main(src_dirs: &[&str]) -> anyhow::Result<()> {
    for src_dir in src_dirs {
        do_one_dir(src_dir)?;
    }

    Ok(())
}
