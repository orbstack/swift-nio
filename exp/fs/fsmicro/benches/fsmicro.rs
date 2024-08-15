use std::{
    ffi::CString, fs::remove_dir, io::{IoSlice, IoSliceMut}, os::fd::{AsRawFd, FromRawFd, OwnedFd}
};

use criterion::{ criterion_group, criterion_main, Criterion};
use nix::{
    errno::Errno, fcntl::{open, OFlag}, sys::{
        stat::{fstat, lstat, Mode},
        uio::{pread, preadv, pwrite, pwritev},
    }, unistd::{access, linkat, mkdir, unlink, AccessFlags, LinkatFlags}
};

#[cfg(target_os = "macos")]
const CLONE_NOFOLLOW: u32 = 0x0001;

extern "C" {
    fn fsgetpath(
        restrict_buf: *mut libc::c_char,
        buflen: libc::size_t,
        fsid: *const libc::fsid_t,
        obj_id: u64,
    ) -> libc::c_int;
}

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

    let mut buf = [0u8; 1024];
    c.bench_function("pwrite file 1024b", |b| {
        b.iter(|| {
            pwrite(file.as_raw_fd(), &buf, 0).unwrap();
        })
    });
    c.bench_function("pwritev file 1024b", |b| {
        b.iter(|| {
            pwritev(file.as_raw_fd(), &[
                IoSlice::new(&buf),
            ], 0).unwrap();
        })
    });

    c.bench_function("pread file 1024b", |b| {
        b.iter(|| {
            pread(file.as_raw_fd(), &mut buf, 0).unwrap();
        })
    });
    c.bench_function("preadv file 1024b", |b| {
        b.iter(|| {
            preadv(file.as_raw_fd(), &mut [
                IoSliceMut::new(&mut buf),
            ], 0).unwrap();
        })
    });

    let mut large_buf = [0u8; 16384];
    c.bench_function("pwrite file 16384b", |b| {
        b.iter(|| {
            pwrite(file.as_raw_fd(), &large_buf, 0).unwrap();
        })
    });

    c.bench_function("pread file 16384b", |b| {
        b.iter(|| {
            pread(file.as_raw_fd(), &mut large_buf, 0).unwrap();
        })
    });

    c.bench_function("write+F_BARRIERFSYNC file", |b| {
        b.iter(|| {
            pwrite(file.as_raw_fd(), &buf, 0).unwrap();
            let ret = unsafe {
                libc::fcntl(file.as_raw_fd(), libc::F_BARRIERFSYNC, 0)
            };
            assert_eq!(ret, 0);
        })
    });

    c.bench_function("write+F_FULLFSYNC file", |b| {
        b.iter(|| {
            pwrite(file.as_raw_fd(), &buf, 0).unwrap();
            let ret = unsafe {
                libc::fcntl(file.as_raw_fd(), libc::F_FULLFSYNC, 0)
            };
            assert_eq!(ret, 0);
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

    #[cfg(target_os = "macos")]
    {
        let existing_st = lstat(".").unwrap();
        c.bench_function("fsgetpath ENOENT check", |b| {
            b.iter(|| {
                let fsid = [existing_st.st_dev+9, 0];
                let ret = unsafe { fsgetpath(std::ptr::null_mut(), 1, &fsid as *const i32 as *const libc::fsid_t, existing_st.st_ino) };
                assert_eq!(ret, -1);
                assert_eq!(Errno::last(), Errno::ENOENT);
            })
        });

        c.bench_function("access ENOENT check", |b| {
            b.iter(|| {
                let path = CString::new(format!("/.vol/{}/{}", existing_st.st_dev + 9, existing_st.st_ino)).unwrap();
                let res = access(path.as_ref(), AccessFlags::F_OK);
                assert_eq!(res, Err(Errno::ENOENT));
            })
        });
    }

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
