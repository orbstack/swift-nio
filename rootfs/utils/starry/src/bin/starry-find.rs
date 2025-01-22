use std::io::BufWriter;
use std::{
    io::Write,
    os::fd::{AsRawFd, FromRawFd, OwnedFd},
    path::Path,
};

use anyhow::anyhow;
use nix::{
    errno::Errno,
    fcntl::{openat, OFlag},
    sys::stat::Mode,
};
use starry::buffer_stack::BufferStack;
use starry::{
    path_stack::PathStack,
    sys::getdents::{for_each_getdents, DirEntry, FileType},
};

fn do_one_entry(
    dirfd: &OwnedFd,
    entry: &DirEntry,
    buffer_stack: &BufferStack,
    path_stack: &PathStack,
    writer: &mut impl Write,
) -> anyhow::Result<()> {
    let path = path_stack.push(entry.name.to_bytes());

    writer.write_all(&path.get())?;
    writer.write_all(b"\n")?;

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
    walk_dir(&child_dirfd, buffer_stack, path_stack, writer)?;
    drop(child_dirfd);

    Ok(())
}

fn walk_dir(
    dirfd: &OwnedFd,
    buffer_stack: &BufferStack,
    path_stack: &PathStack,
    writer: &mut impl Write,
) -> anyhow::Result<()> {
    for_each_getdents(dirfd, None, buffer_stack, |entry| {
        do_one_entry(dirfd, &entry, buffer_stack, path_stack, writer).map_err(|e| {
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
    let buffer_stack = BufferStack::new()?;
    let path_stack = PathStack::default();
    let _guard = path_stack.push(src_dir.as_bytes());
    let mut writer = BufWriter::new(std::io::stdout());
    walk_dir(&root_dir, &buffer_stack, &path_stack, &mut writer)
        .map_err(|e| anyhow!("{}/{}", src_dir, e))?;

    Ok(())
}
