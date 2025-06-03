use std::{
    ffi::CString, fs::File, os::fd::FromRawFd, path::Path
};

use crate::{
    interrogate::InterrogatedFile,
    oarchive::context::{ArchiveContext, OwnedArchiveContext}, sys::file::AT_FDCWD,
};
use nix::{
    fcntl::{openat, OFlag},
    sys::stat::Mode,
};
use zstd::Encoder;

const MAX_COMPRESSION_THREADS: usize = 4;

pub fn main(src_dir: &str) -> anyhow::Result<()> {
    let file = unsafe { File::from_raw_fd(libc::STDOUT_FILENO) };

    let mut writer = Encoder::new(file, 0)?;
    // tar is usually bottlenecked on zstd, but let's be conservative to avoid burning CPU
    let num_threads =
        (std::thread::available_parallelism()?.get() / 2).clamp(1, MAX_COMPRESSION_THREADS);
    writer.multithread(num_threads as u32)?;

    // add root dir
    let root_dir = openat(
        AT_FDCWD,
        Path::new(&src_dir),
        OFlag::O_RDONLY | OFlag::O_DIRECTORY | OFlag::O_CLOEXEC,
        Mode::empty(),
    )?;

    // only chdir after opening paths, in case they're relative
    InterrogatedFile::chdir_to_proc()?;

    let owned_ctx = OwnedArchiveContext::new()?;
    let mut ctx = ArchiveContext::new(&mut writer, &owned_ctx);

    // add entry for root dir
    let root_dir_file = InterrogatedFile::from_directory_fd(&root_dir)?;
    ctx.add_one_entry(&root_dir_file, b".")?;

    // walk dirs using walk_dir_root
    let src_dir_cstr = CString::new(src_dir.as_bytes())?;
    ctx.walk_dir_root(&root_dir, &src_dir_cstr)?;

    // end compressed stream
    writer.finish()?;

    Ok(())
}
