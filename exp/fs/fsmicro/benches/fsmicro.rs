use std::{
    ffi::CString, fs::remove_dir, os::fd::{AsRawFd, FromRawFd, OwnedFd}
};

use criterion::{ criterion_group, criterion_main, Criterion};
use nix::{
    fcntl::{open, OFlag}, sys::{
        stat::{fstat, lstat, Mode},
        uio::pwrite,
    }, unistd::{linkat, mkdir, unlink, LinkatFlags}
};

#[cfg(target_os = "macos")]
const CLONE_NOFOLLOW: u32 = 0x0001;

pub fn criterion_benchmark(c: &mut Criterion) {
    let _ = unlink("a");

    c.bench_function("create+delete file", |b| {
        b.iter(|| {
            let fd = unsafe {
                OwnedFd::from_raw_fd(
                    open("a", OFlag::O_CREAT, Mode::from_bits_truncate(0o644)).unwrap(),
                )
            };
            drop(fd);
            unlink("a").unwrap();
        })
    });

    c.bench_function("open file", |b| {
        b.iter(|| {
            let fd = unsafe {
                OwnedFd::from_raw_fd(
                    open("a", OFlag::O_CREAT, Mode::from_bits_truncate(0o644)).unwrap(),
                )
            };
            drop(fd);
        })
    });

    c.bench_function("lstat file", |b| {
        b.iter(|| {
            let _ = lstat("a").unwrap();
        })
    });

    let file = unsafe {
        OwnedFd::from_raw_fd(
            open(
                "a",
                OFlag::O_RDWR | OFlag::O_CREAT | OFlag::O_TRUNC,
                Mode::from_bits_truncate(0o644),
            )
            .unwrap(),
        )
    };

    c.bench_function("fstat file", |b| {
        b.iter(|| {
            let _ = fstat(file.as_raw_fd()).unwrap();
        })
    });

    c.bench_function("write file", |b| {
        b.iter(|| {
            let buf = [0u8; 1024];
            pwrite(file.as_raw_fd(), &buf, 0).unwrap();
        })
    });

    let _ = unlink("c");
    c.bench_function("link+unlink file", |b| {
        b.iter(|| {
            linkat(None, "a", None, "c", LinkatFlags::NoSymlinkFollow).unwrap();
            unlink("c").unwrap();
        })
    });

    #[cfg(target_os = "macos")]
    c.bench_function("clone+unlink file", |b| {
        use nix::libc::clonefile;

        let str_a = CString::new("a").unwrap();
        let str_c = CString::new("c").unwrap();
        b.iter(|| {
            let ret = unsafe { clonefile(str_a.as_ptr(), str_c.as_ptr(), CLONE_NOFOLLOW) };
            assert_eq!(ret, 0);
            unlink("c").unwrap();
        })
    });

    let _ = remove_dir("b");
    c.bench_function("create+delete dir", |b| {
        b.iter(|| {
            mkdir("b", Mode::from_bits_truncate(0o755)).unwrap();
            remove_dir("b").unwrap();
        })
    });
}

criterion_group!(benches, criterion_benchmark);
criterion_main!(benches);
