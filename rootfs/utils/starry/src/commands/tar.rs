/*
 * starry tar
 * similar to `bsdtar --zstd -cf --xattrs`
 *
 * always uses PAX extensions. no GNU long name extensions. GNU PAX 1.0 for sparse files.
 * only supports archival. extraction would be a lot of work, because we have to handle arbitrary user inputs: GNU long names, legacy GNU sparse files, GNU binary extended mtimes, etc.
 * bsdtar is capable of extracting everything in the archives we produce.
 *
 * features:
 * - supports nanosecond mtimes
 * - supports xattrs, even on symlinks
 * - supports inode flags/attributes like immutable and append-only
 * - supports hard links
 * - supports sparse files
 * - supports fifos and char/block devices
 * - safe against symlink races (everything is dirfd/O_NOFOLLOW)
 *
 * assumptions:
 * - source is NOT modified concurrently. if this is violated, the command may fail or produce inconsistent results, but there is no security risk (in the case of symlink races). specifically, deletion races may cause the entire command to fail.
 * - should be run as root in order to read trusted.* xattrs and other xattrs on symlinks
 * - must be run with CAP_DAC_READ_SEARCH to correctly extract read-only dirs/files (because we set mode at creation time, not afterwards)
 */

use std::{
    ffi::CString,
    fs::File,
    io::Write,
    os::fd::{FromRawFd, OwnedFd},
    path::Path,
};

use crate::{
    interrogate::InterrogatedFile,
    tarball::context::{OwnedTarContext, TarContext, TAR_PADDING},
};
use nix::{
    fcntl::{openat, OFlag},
    sys::stat::Mode,
};
use zstd::Encoder;

const MAX_COMPRESSION_THREADS: usize = 4;

const CONFIG_JSON_PATH: &[u8] = b"_orbstack/v1/config.json";

pub fn main(src_dir: &str, config_json: Option<&str>) -> anyhow::Result<()> {
    InterrogatedFile::chdir_to_proc()?;

    let file = unsafe { File::from_raw_fd(libc::STDOUT_FILENO) };

    let mut writer = Encoder::new(file, 0)?;
    // tar is usually bottlenecked on zstd, but let's be conservative to avoid burning CPU
    let num_threads =
        (std::thread::available_parallelism()?.get() / 2).clamp(1, MAX_COMPRESSION_THREADS);
    writer.multithread(num_threads as u32)?;

    // add root dir
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

    // add entry for config
    if let Some(config_json) = config_json {
        ctx.add_synthetic_file(CONFIG_JSON_PATH, config_json.as_bytes())?;
    }

    // walk dirs using walk_dir_root
    let src_dir_cstr = CString::new(src_dir.as_bytes())?;
    ctx.walk_dir_root(&root_dir, &src_dir_cstr)?;

    // terminate with 1024 zero bytes (2 zero blocks)
    writer.write_all(&TAR_PADDING)?;

    // end compressed stream
    writer.finish()?;

    Ok(())
}
