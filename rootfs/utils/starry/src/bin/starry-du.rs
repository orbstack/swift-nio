use std::{
    collections::BTreeSet, os::fd::{AsRawFd, FromRawFd, OwnedFd}, path::Path
};

use anyhow::anyhow;
use nix::{
    fcntl::{openat, OFlag},
    sys::stat::Mode,
};
use starry::{
    buffer_stack::BufferStack,
    sys::{
        file::fstatat,
        getdents::{for_each_getdents, DirEntry, FileType},
    },
};

struct OwnedDuContext {
    buffer_stack: BufferStack,
}

impl OwnedDuContext {
    pub fn new() -> anyhow::Result<Self> {
        Ok(Self {
            buffer_stack: BufferStack::new()?,
        })
    }
}

struct DuContext<'a> {
    total_st_blocks: u64,
    seen_inodes: BTreeSet<u64>,
    buffer_stack: &'a BufferStack,
}

impl<'a> DuContext<'a> {
    fn new(buffer_stack: &'a BufferStack) -> Self {
        Self {
            total_st_blocks: 0,
            seen_inodes: BTreeSet::new(),
            buffer_stack,
        }
    }

    fn do_one_entry(&mut self, dirfd: &OwnedFd, entry: &DirEntry) -> anyhow::Result<()> {
        // don't double-count hard links
        // we can avoid statting directories entirely if we assume st_dev never changes
        if !self.seen_inodes.insert(entry.inode) {
            return Ok(());
        }

        if entry.file_type != FileType::Directory {
            let st = fstatat(dirfd, entry.name, libc::AT_SYMLINK_NOFOLLOW)?;
            self.total_st_blocks += st.st_blocks as u64;

            if st.st_mode & libc::S_IFMT != libc::S_IFDIR {
                return Ok(());
            }
        }

        // optimization: directories always have st_blocks=0?
        let child_dirfd = unsafe {
            OwnedFd::from_raw_fd(openat(
                Some(dirfd.as_raw_fd()),
                entry.name,
                OFlag::O_RDONLY | OFlag::O_DIRECTORY | OFlag::O_CLOEXEC | OFlag::O_NOFOLLOW,
                Mode::empty(),
            )?)
        };
        self.walk_dir(&child_dirfd)?;

        Ok(())
    }

    fn walk_dir(&mut self, dirfd: &OwnedFd) -> anyhow::Result<()> {
        for_each_getdents(dirfd, None, self.buffer_stack, |entry| {
            self.do_one_entry(dirfd, &entry).map_err(|e| {
                if e.is::<nix::Error>() {
                    // nix::Error = root cause
                    // start chain: "PATH: ERROR"
                    anyhow!("{}: {}", entry.name.to_string_lossy(), e)
                } else {
                    // as we unwind the directory stack, prepend dirs to error
                    // chain: "DIR/CHILD: ERROR"
                    anyhow!("{}/{}", entry.name.to_string_lossy(), e)
                }
            })
        })?;

        Ok(())
    }
}

fn main() -> anyhow::Result<()> {
    // open root dir
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

    // walk dirs
    let owned_ctx = OwnedDuContext::new()?;
    let mut ctx = DuContext::new(&owned_ctx.buffer_stack);
    ctx.walk_dir(&root_dir)
        .map_err(|e| anyhow!("{}/{}", src_dir, e))?;

    // report in KiB
    println!("{} {}", ctx.total_st_blocks * 512 / 1024, src_dir);
    Ok(())
}
