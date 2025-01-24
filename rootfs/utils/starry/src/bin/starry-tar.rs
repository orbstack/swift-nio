use std::{
    fs::File,
    io::Write,
    os::fd::{FromRawFd, OwnedFd},
    path::Path,
};

use anyhow::anyhow;
use nix::{
    fcntl::{openat, OFlag},
    sys::stat::Mode,
};
use starry::{
    interrogate::InterrogatedFile,
    tarball::context::{OwnedTarContext, TarContext, TAR_PADDING},
};
use zstd::Encoder;

const MAX_COMPRESSION_THREADS: usize = 2;

fn main() -> anyhow::Result<()> {
    let file = unsafe { File::from_raw_fd(libc::STDOUT_FILENO) };

    let mut writer = Encoder::new(file, 0)?;
    // tar is usually bottlenecked on zstd, but let's be conservative to avoid burning CPU
    let num_threads = std::cmp::min(
        MAX_COMPRESSION_THREADS,
        std::thread::available_parallelism()?.get(),
    );
    writer.multithread(num_threads as u32)?;

    // add root dir
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

    let owned_ctx = OwnedTarContext::new()?;
    let mut ctx = TarContext::new(&mut writer, &owned_ctx);

    // add entry for root dir
    let root_dir_file = InterrogatedFile::from_directory_fd(&root_dir)?;
    ctx.add_one_entry(&root_dir_file, b".")?;

    // walk dirs
    ctx.walk_dir(&root_dir, None)?;

    // terminate with 1024 zero bytes (2 zero blocks)
    writer.write_all(&TAR_PADDING)?;

    // end compressed stream
    writer.finish()?;

    Ok(())
}
