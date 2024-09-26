use std::{fs::File, io::Write, os::{fd::FromRawFd, unix::fs::FileTypeExt}, path::Path};

use tar::{Builder, EntryType, Header};
use zstd::Encoder;

fn add_dir(a: &mut Builder<impl Write>, base_path: &Path, path: &Path) -> anyhow::Result<()> {
    if base_path != path {
        a.append_dir(path.strip_prefix(base_path)?, path)?;
    }

    for entry in std::fs::read_dir(path)? {
        let entry = entry?;
        let meta = entry.metadata()?;
        let typ = meta.file_type();
        let entry_path = entry.path();
        if typ.is_file() {
            a.append_file(entry_path.strip_prefix(base_path)?, &mut File::open(&entry_path)?)?;
        } else if typ.is_dir() {
            add_dir(a, base_path, &entry_path)?;
        } else if typ.is_symlink() {
            let mut header = Header::new_gnu();
            header.set_entry_type(EntryType::Symlink);
            header.set_size(0);
            a.append_link(&mut header, entry_path.strip_prefix(base_path)?, &entry_path)?;
        } else if typ.is_block_device() || typ.is_char_device() || typ.is_fifo() {
            a.append_path_with_name(&entry_path, entry_path.strip_prefix(base_path)?)?;
        } else if typ.is_socket() {
            eprintln!("{}: socket can not be archived", entry_path.display());
        } else {
            panic!("{}: unknown file type", entry_path.display());
        }
    }

    Ok(())
}

fn main() {
    let file = unsafe { File::from_raw_fd(1) };
    let mut zstd = Encoder::new(file, 0).unwrap();
    zstd.multithread(1).unwrap();
    let mut a = Builder::new(zstd);

    // walk dirs
    let src_dir = std::env::args().nth(1).unwrap();
    add_dir(&mut a, Path::new(&src_dir), Path::new(&src_dir)).unwrap();
    a.into_inner().unwrap().finish().unwrap();
}
