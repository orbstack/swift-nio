use std::{ffi::CStr, os::fd::AsRawFd};

use libc::{DT_BLK, DT_CHR, DT_DIR, DT_FIFO, DT_LNK, DT_REG, DT_SOCK, DT_UNKNOWN};
use nix::errno::Errno;

use crate::buffer_stack::BufferStack;

#[repr(C, packed)]
#[derive(Debug, Clone, Copy)]
struct LinuxDirent64 {
    d_ino: u64,
    d_off: u64,
    d_reclen: u16,
    d_type: u8,
    // zero-sized array: d_reclen - sizeof(fields above)
    //d_name: [u8; ...],
}

#[derive(Debug, Clone, Copy)]
pub struct DirEntry<'a> {
    pub inode: u64,
    pub file_type: FileType,
    pub name: &'a CStr,
}

#[repr(u8)]
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum FileType {
    Unknown = DT_UNKNOWN,
    Fifo = DT_FIFO,
    Char = DT_CHR,
    Directory = DT_DIR,
    Block = DT_BLK,
    Regular = DT_REG,
    Symlink = DT_LNK,
    Socket = DT_SOCK,
}

impl FileType {
    pub fn from_stat_fmt(fmt: u32) -> Self {
        match fmt {
            libc::S_IFSOCK => FileType::Socket,
            libc::S_IFLNK => FileType::Symlink,
            libc::S_IFREG => FileType::Regular,
            libc::S_IFDIR => FileType::Directory,
            libc::S_IFBLK => FileType::Block,
            libc::S_IFCHR => FileType::Char,
            libc::S_IFIFO => FileType::Fifo,
            _ => panic!("unknown stat fmt: {}", fmt),
        }
    }
}

pub fn for_each_getdents<F: AsRawFd>(
    fd: &F,
    // if caller has stat() output, then it can provide an st_nlink hint to skip the redundant getdents()=0 call on EOF
    nents_hint: Option<usize>,
    buffer_stack: &BufferStack,
    mut f: impl FnMut(DirEntry<'_>) -> anyhow::Result<()>,
) -> anyhow::Result<()> {
    let max_nents = nents_hint.unwrap_or(usize::MAX);
    let mut total_nents = 0;

    let mut guard = buffer_stack.next();
    let buf = guard.get();

    while total_nents < max_nents {
        let n = unsafe {
            // glibc's wrapper is getdents64; musl's is getdents; neither is really public, so just call the raw syscall
            libc::syscall(
                libc::SYS_getdents64,
                fd.as_raw_fd(),
                buf.as_mut_ptr() as *mut _,
                BufferStack::BUF_SIZE,
            )
        };
        if n == 0 {
            break;
        } else if n == -1 {
            return Err(Errno::last().into());
        }

        let mut p = buf.as_ptr() as *const u8;
        let endp = unsafe { p.add(n as usize) };
        loop {
            if p >= endp {
                break;
            }

            total_nents += 1;

            let d = unsafe { (p as *const LinuxDirent64).read_unaligned() };
            let name_bytes = unsafe {
                std::slice::from_raw_parts(
                    p.add(size_of::<LinuxDirent64>()),
                    d.d_reclen as usize - size_of::<LinuxDirent64>(),
                )
            };
            let name = CStr::from_bytes_until_nul(name_bytes).unwrap();

            // safe: we trust kernel to return valid data
            p = unsafe { p.add(d.d_reclen as usize) };

            if name == c"." || name == c".." {
                continue;
            }

            let entry = DirEntry {
                inode: d.d_ino,
                // safe: we trust kernel to return valid data
                file_type: unsafe { std::mem::transmute::<u8, FileType>(d.d_type) },
                name,
            };
            f(entry)?;
        }
    }

    Ok(())
}
