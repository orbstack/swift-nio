use std::{
    fs::remove_dir,
    os::fd::{AsRawFd, FromRawFd, OwnedFd},
};

use criterion::{ criterion_group, criterion_main, Criterion};
use nix::{
    fcntl::{open, OFlag},
    sys::{
        stat::{lstat, Mode},
        uio::pwrite,
    },
    unistd::{mkdir, unlink},
};

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
    c.bench_function("write file", |b| {
        b.iter(|| {
            let buf = [0u8; 1024];
            pwrite(file.as_raw_fd(), &buf, 0).unwrap();
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
