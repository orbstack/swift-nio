use std::{io, mem::MaybeUninit, path::Path};

use criterion::{black_box, criterion_group, criterion_main, Criterion};
use nix::sys::stat::lstat;

const TEST_PATH: &str =
    "/Users/dragon/code/projects/macvirt/vendor/libkrun/src/vmm/src/vmm_config/boot_source.rs";

extern "C" {
    fn fsgetpath(
        restrict_buf: *mut libc::c_char,
        buflen: libc::size_t,
        fsid: *const libc::fsid_t,
        obj_id: u64,
    ) -> libc::c_int;
}

fn get_path_by_dev_ino(dev: i32, ino: u64) -> io::Result<String> {
    let mut path_buf: MaybeUninit<[u8; 1024]> = MaybeUninit::uninit();
    let fsid = [dev, 0];
    let len = unsafe {
        fsgetpath(
            path_buf.as_mut_ptr() as *mut libc::c_char,
            1024,
            &fsid as *const i32 as *const libc::fsid_t,
            ino as u64,
        )
    };
    if len == -1 {
        println!("get_path_by_dev_ino error: {}", io::Error::last_os_error());
        return Err(io::Error::last_os_error());
    }

    // safe: kernel guarantees UTF-8
    Ok(unsafe { String::from_utf8_unchecked(path_buf.assume_init()[..len as usize - 1].to_vec()) })
}

pub fn criterion_benchmark(c: &mut Criterion) {
    // initial stat to get dev,ino
    let st = lstat(TEST_PATH).unwrap();
    let dev = st.st_dev;
    let ino = st.st_ino;

    c.bench_function("lstat realpath", |b| b.iter(|| lstat(black_box(TEST_PATH))));

    let volfs_path = format!("/.vol/{dev}/{ino}");
    c.bench_function("lstat volfs", |b| {
        b.iter(|| lstat(black_box(Path::new(&volfs_path))))
    });

    c.bench_function("fsgetpath", |b| {
        b.iter(|| {
            black_box(get_path_by_dev_ino(dev, ino).unwrap());
        })
    });

    c.bench_function("lstat + fsgetpath", |b| {
        b.iter(|| {
            let path = get_path_by_dev_ino(dev, ino).unwrap();
            lstat(black_box(Path::new(&path))).unwrap();
        })
    });
}

criterion_group!(benches, criterion_benchmark);
criterion_main!(benches);
