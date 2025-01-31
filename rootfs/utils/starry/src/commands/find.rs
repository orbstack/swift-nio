/*
 * starry find
 * similar to `find`
 *
 * there's nothing special about this one; please don't use it.
 * it exists as a benchmark to compare against `find`, Rust `walkdir`, and other implementations.
 */

use std::ffi::CString;
use std::io::BufWriter;
use std::{
    io::Write,
    os::fd::{AsRawFd, FromRawFd, OwnedFd},
    path::Path,
};

use crate::recurse::Recurser;
use crate::{
    path_stack::PathStack,
    sys::getdents::{DirEntry, FileType},
};
use nix::{
    errno::Errno,
    fcntl::{openat, OFlag},
    sys::stat::Mode,
};

struct OwnedFindContext {
    recurser: Recurser,
    path_stack: PathStack,
}

impl OwnedFindContext {
    fn new() -> anyhow::Result<Self> {
        Ok(Self {
            recurser: Recurser::new()?,
            path_stack: PathStack::default(),
        })
    }
}

struct FindContext<'a, W: Write> {
    writer: &'a mut W,
    path_stack: &'a PathStack,
    recurser: &'a Recurser,
}

impl<'a, W: Write> FindContext<'a, W> {
    fn new(writer: &'a mut W, owned: &'a OwnedFindContext) -> Self {
        Self {
            writer,
            path_stack: &owned.path_stack,
            recurser: &owned.recurser,
        }
    }

    fn do_one_entry(&mut self, dirfd: &OwnedFd, entry: &DirEntry) -> anyhow::Result<()> {
        let path = self.path_stack.push(entry.name.to_bytes());
        self.writer.write_all(&path.get())?;
        self.writer.write_all(b"\n")?;

        // optimization: don't bother to open it if we know for sure that it's not a dir
        if entry.file_type != FileType::Directory {
            return Ok(());
        }

        // happy path: open as a dir. if fails, then it's not a dir
        let child_dirfd = match openat(
            Some(dirfd.as_raw_fd()),
            entry.name,
            OFlag::O_RDONLY | OFlag::O_DIRECTORY | OFlag::O_CLOEXEC,
            Mode::empty(),
        ) {
            Ok(fd) => fd,
            Err(Errno::ENOTDIR) => return Ok(()),
            Err(e) => return Err(e.into()),
        };
        let child_dirfd = unsafe { OwnedFd::from_raw_fd(child_dirfd) };
        self.walk_dir(&child_dirfd)?;
        Ok(())
    }

    fn walk_dir(&mut self, dirfd: &OwnedFd) -> anyhow::Result<()> {
        self.recurser
            .walk_dir(dirfd, None, |entry| self.do_one_entry(dirfd, entry))?;
        Ok(())
    }
}

pub fn main(src_dir: &str) -> anyhow::Result<()> {
    // open root dir
    let root_dir = unsafe {
        OwnedFd::from_raw_fd(openat(
            None,
            Path::new(&src_dir),
            OFlag::O_RDONLY | OFlag::O_DIRECTORY | OFlag::O_CLOEXEC,
            Mode::empty(),
        )?)
    };

    // walk dirs
    let owned_ctx = OwnedFindContext::new()?;
    let _guard = owned_ctx.path_stack.push(src_dir.as_bytes());
    let mut writer = BufWriter::new(std::io::stdout());
    let mut ctx = FindContext::new(&mut writer, &owned_ctx);
    let src_dir_cstr = CString::new(src_dir.as_bytes())?;
    ctx.recurser
        .walk_dir_root(&root_dir, &src_dir_cstr, None, |entry| {
            ctx.do_one_entry(&root_dir, entry)
        })?;

    Ok(())
}
