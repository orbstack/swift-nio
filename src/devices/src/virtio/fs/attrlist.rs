use std::{
    ffi::CStr,
    io,
    mem::{size_of, size_of_val, MaybeUninit},
    os::fd::AsRawFd,
};

use libc::{
    attrlist, getattrlistbulk, ATTR_BIT_MAP_COUNT, ATTR_CMNEXT_EXT_FLAGS, ATTR_CMN_ACCESSMASK,
    ATTR_CMN_ACCTIME, ATTR_CMN_CHGTIME, ATTR_CMN_CRTIME, ATTR_CMN_DEVID, ATTR_CMN_FILEID,
    ATTR_CMN_FLAGS, ATTR_CMN_GRPID, ATTR_CMN_MODTIME, ATTR_CMN_NAME, ATTR_CMN_OBJTYPE,
    ATTR_CMN_OWNERID, ATTR_CMN_RETURNED_ATTRS, ATTR_DIR_ALLOCSIZE, ATTR_DIR_DATALENGTH,
    ATTR_DIR_ENTRYCOUNT, ATTR_DIR_IOBLOCKSIZE, ATTR_DIR_MOUNTSTATUS, ATTR_FILE_DATAALLOCSIZE,
    ATTR_FILE_DATALENGTH, ATTR_FILE_DEVTYPE, ATTR_FILE_IOBLOCKSIZE, ATTR_FILE_LINKCOUNT,
    DIR_MNTSTATUS_MNTPOINT, FSOPT_ATTR_CMN_EXTENDED,
};
use nix::errno::Errno;
use tracing::{error, trace};

// __DARWIN_C_LEVEL >= __DARWIN_C_FULL
const ATTR_CMN_ERROR: u32 = 0x20000000;

const SF_FIRMLINK: u32 = 0x00800000;

const EF_NO_XATTRS: u64 = 0x00000002;

#[allow(dead_code)]
mod vtype {
    pub const VNON: u32 = 0;
    pub const VREG: u32 = 1;
    pub const VDIR: u32 = 2;
    pub const VBLK: u32 = 3;
    pub const VCHR: u32 = 4;
    pub const VLNK: u32 = 5;
    pub const VSOCK: u32 = 6;
    pub const VFIFO: u32 = 7;
    pub const VBAD: u32 = 8;
    pub const VSTR: u32 = 9;
    pub const VCPLX: u32 = 10;
}

#[derive(Debug)]
pub struct AttrlistEntry {
    pub name: String,
    pub is_mountpoint: bool,
    pub st: Option<libc::stat>,
}

unsafe fn read_and_advance<T: Copy>(p: &mut *const u8) -> T {
    let val = unsafe { (*p as *const T).read_unaligned() };
    // min alignment = 4 bytes (according to man page)
    *p = unsafe { p.add(std::cmp::max(size_of::<T>(), 4)) };
    val
}

pub fn list_dir<T: AsRawFd>(dirfd: T, reserve_capacity: usize) -> io::Result<Vec<AttrlistEntry>> {
    let mut entries = Vec::with_capacity(reserve_capacity);

    let attrlist = attrlist {
        bitmapcount: ATTR_BIT_MAP_COUNT,
        reserved: 0,
        commonattr: ATTR_CMN_RETURNED_ATTRS
            | ATTR_CMN_NAME
            | ATTR_CMN_DEVID
            | ATTR_CMN_OBJTYPE
            //| ATTR_CMN_CRTIME
            | ATTR_CMN_MODTIME
            | ATTR_CMN_CHGTIME
            | ATTR_CMN_ACCTIME
            | ATTR_CMN_OWNERID
            | ATTR_CMN_GRPID
            | ATTR_CMN_ACCESSMASK
            | ATTR_CMN_FLAGS
            | ATTR_CMN_FILEID
            | ATTR_CMN_ERROR,
        volattr: 0,
        dirattr: ATTR_DIR_ENTRYCOUNT
            | ATTR_DIR_MOUNTSTATUS
            | ATTR_DIR_ALLOCSIZE
            | ATTR_DIR_IOBLOCKSIZE
            | ATTR_DIR_DATALENGTH,
        fileattr: ATTR_FILE_LINKCOUNT
            | ATTR_FILE_IOBLOCKSIZE
            | ATTR_FILE_DEVTYPE
            | ATTR_FILE_DATALENGTH
            | ATTR_FILE_DATAALLOCSIZE, // st_nlink
        forkattr: ATTR_CMNEXT_EXT_FLAGS, // E_NO_XATTRS
    };

    loop {
        // safe: we only use the part of buf that was read
        // 16384 = avg 128 bytes * 128 entries
        // to avoid compiler-inserted probe frame, don't exceed page size
        let mut buf: MaybeUninit<[u8; 16384]> = MaybeUninit::uninit();
        let n = unsafe {
            getattrlistbulk(
                dirfd.as_raw_fd(),
                &attrlist as *const attrlist as *mut libc::c_void,
                buf.as_mut_ptr() as *mut libc::c_void,
                size_of_val(&buf),
                (FSOPT_ATTR_CMN_EXTENDED) as u64,
            )
        };
        if n < 0 {
            return Err(io::Error::last_os_error());
        }
        if n == 0 {
            break;
        }

        let mut p = buf.as_ptr() as *const u8;
        for i in 0..n {
            let mut entry_p = p;
            // entry_len includes u32 size
            let entry_len: u32 = unsafe { read_and_advance(&mut entry_p) };
            trace!(n, i, entry_len, "advance entry");

            let returned: libc::attribute_set_t = unsafe { read_and_advance(&mut entry_p) };

            let mut entry = AttrlistEntry {
                name: String::new(),
                is_mountpoint: false,
                // must zero because fields could be left empty
                st: Some(unsafe { std::mem::zeroed() }),
            };
            let st = entry.st.as_mut().unwrap();

            let mut error: Option<Errno> = None;
            if returned.commonattr & ATTR_CMN_ERROR != 0 {
                let errno = unsafe { read_and_advance::<i32>(&mut entry_p) };
                error = Some(Errno::from_raw(errno));
            }

            if returned.commonattr & ATTR_CMN_NAME != 0 {
                let name_ref =
                    unsafe { (entry_p as *const libc::attrreference_t).read_unaligned() };
                let name_ptr = unsafe { entry_p.add(name_ref.attr_dataoffset as usize) };
                let name_len = name_ref.attr_length as usize - 1;
                // we trust kernel to return valid utf-8 names
                entry.name = unsafe {
                    std::str::from_utf8_unchecked(std::slice::from_raw_parts(name_ptr, name_len))
                }
                .to_string();
                entry_p = unsafe { entry_p.add(size_of::<libc::attrreference_t>()) };
            }

            if returned.commonattr & ATTR_CMN_DEVID != 0 {
                st.st_dev = unsafe { read_and_advance::<libc::dev_t>(&mut entry_p) };
            }

            if returned.commonattr & ATTR_CMN_OBJTYPE != 0 {
                let typ = unsafe { read_and_advance::<u32>(&mut entry_p) };
                st.st_mode = match typ {
                    vtype::VREG => libc::S_IFREG,
                    vtype::VDIR => libc::S_IFDIR,
                    vtype::VBLK => libc::S_IFBLK,
                    vtype::VCHR => libc::S_IFCHR,
                    vtype::VLNK => libc::S_IFLNK,
                    vtype::VSOCK => libc::S_IFSOCK,
                    vtype::VFIFO => libc::S_IFIFO,
                    // skip VBAD, VSTR, VCPLX
                    _ => {
                        error!(typ, "unknown file type");
                        error = Some(Errno::EINVAL);
                        0
                    }
                };
            }

            if returned.commonattr & ATTR_CMN_CRTIME != 0 {
                let time = unsafe { read_and_advance::<libc::timespec>(&mut entry_p) };
                st.st_birthtime = time.tv_sec;
                st.st_birthtime_nsec = time.tv_nsec;
            }

            if returned.commonattr & ATTR_CMN_MODTIME != 0 {
                let time = unsafe { read_and_advance::<libc::timespec>(&mut entry_p) };
                st.st_mtime = time.tv_sec;
                st.st_mtime_nsec = time.tv_nsec;
            }

            if returned.commonattr & ATTR_CMN_CHGTIME != 0 {
                let time = unsafe { read_and_advance::<libc::timespec>(&mut entry_p) };
                st.st_ctime = time.tv_sec;
                st.st_ctime_nsec = time.tv_nsec;
            } else {
                // TODO substitute with mtime for unsupported FS?
            }

            if returned.commonattr & ATTR_CMN_ACCTIME != 0 {
                let time = unsafe { read_and_advance::<libc::timespec>(&mut entry_p) };
                st.st_atime = time.tv_sec;
                st.st_atime_nsec = time.tv_nsec;
            }

            if returned.commonattr & ATTR_CMN_OWNERID != 0 {
                st.st_uid = unsafe { read_and_advance::<libc::uid_t>(&mut entry_p) };
            }

            if returned.commonattr & ATTR_CMN_GRPID != 0 {
                st.st_gid = unsafe { read_and_advance::<libc::gid_t>(&mut entry_p) };
            }

            if returned.commonattr & ATTR_CMN_ACCESSMASK != 0 {
                st.st_mode |= unsafe { read_and_advance::<u16>(&mut entry_p) };
            }

            if returned.commonattr & ATTR_CMN_FLAGS != 0 {
                st.st_flags = unsafe { read_and_advance::<u32>(&mut entry_p) };
                // firmlinks are bind mounts, but with no mountpoint
                // they require the same special treatment as mountpoints:
                // readdir inode != inode on stat (but device is the same, because it's on the same FS)
                if st.st_flags & SF_FIRMLINK != 0 {
                    entry.is_mountpoint = true;
                }
            }

            // getattrlist doesn't support st_gen
            // ATTR_CMN_GEN_COUNT = number of times file has been *modified*

            if returned.commonattr & ATTR_CMN_FILEID != 0 {
                st.st_ino = unsafe { read_and_advance::<u64>(&mut entry_p) };
            }

            if returned.dirattr & ATTR_DIR_ENTRYCOUNT != 0 {
                // add 2 for "." and "..", like st_nlink on most filesystems
                st.st_nlink = 2 + unsafe { read_and_advance::<u16>(&mut entry_p) };
            }

            if returned.dirattr & ATTR_DIR_MOUNTSTATUS != 0 {
                let flags = unsafe { read_and_advance::<u32>(&mut entry_p) };
                if flags & DIR_MNTSTATUS_MNTPOINT != 0 {
                    entry.is_mountpoint = true;
                }
            }

            if returned.dirattr & ATTR_DIR_ALLOCSIZE != 0 {
                // always 512-blocks, regardless of st_blksize
                st.st_blocks = unsafe { read_and_advance::<i64>(&mut entry_p) } / 512;
            }

            if returned.dirattr & ATTR_DIR_IOBLOCKSIZE != 0 {
                st.st_blksize = unsafe { read_and_advance::<i32>(&mut entry_p) };
            }

            if returned.dirattr & ATTR_DIR_DATALENGTH != 0 {
                st.st_size = unsafe { read_and_advance::<i64>(&mut entry_p) };
            }

            if returned.fileattr & ATTR_FILE_LINKCOUNT != 0 {
                st.st_nlink = unsafe { read_and_advance::<u16>(&mut entry_p) };
            }

            if returned.fileattr & ATTR_FILE_IOBLOCKSIZE != 0 {
                st.st_blksize = unsafe { read_and_advance::<i32>(&mut entry_p) };
            }

            if returned.fileattr & ATTR_FILE_DEVTYPE != 0 {
                st.st_rdev = unsafe { read_and_advance::<libc::dev_t>(&mut entry_p) };
            }

            if returned.fileattr & ATTR_FILE_DATALENGTH != 0 {
                st.st_size = unsafe { read_and_advance::<libc::off_t>(&mut entry_p) };
            }

            if returned.fileattr & ATTR_FILE_DATAALLOCSIZE != 0 {
                // always 512-blocks, regardless of st_blksize
                st.st_blocks = unsafe { read_and_advance::<libc::off_t>(&mut entry_p) } / 512;
            }

            if returned.forkattr & ATTR_CMNEXT_EXT_FLAGS != 0 {
                let flags = unsafe { read_and_advance::<u64>(&mut entry_p) };
                if flags & EF_NO_XATTRS == 0 {
                    // TODO need to read xattrs for this entry
                }
            }

            // on error, skip to next entry
            if let Some(error) = error {
                debug!(?error, name = &entry.name, "failed to read entry");
                entry.st = None;
                p = unsafe { p.add(entry_len as usize) };
                continue;
            }

            trace!(?entry, "entry");
            entries.push(entry);
            p = unsafe { p.add(entry_len as usize) };
        }
    }

    Ok(entries)
}

pub fn list_dir_legacy<F>(
    stream: *mut libc::DIR,
    reserve_capacity: usize,
    stat_fn: F,
) -> io::Result<Vec<AttrlistEntry>>
where
    F: Fn(&str) -> io::Result<libc::stat>,
{
    let mut entries = Vec::with_capacity(reserve_capacity);

    loop {
        let dentry = unsafe { libc::readdir(stream) };
        if dentry.is_null() {
            break;
        }

        let dt_ino = unsafe { (*dentry).d_ino };
        let name = unsafe {
            CStr::from_bytes_until_nul(&*std::ptr::slice_from_raw_parts(
                (*dentry).d_name.as_ptr() as *const u8,
                (*dentry).d_name.len(),
            ))
            .unwrap()
        };

        // match getattrlistbulk behavior: skip "." and ".."
        let name_bytes = name.to_bytes();
        if name_bytes == b"." || name_bytes == b".." {
            continue;
        }

        // no need to replace ino based on nfs mountpoint: we call common lookup functions and never return dt_ino
        let name_str = name.to_str().unwrap();
        let st = match stat_fn(name_str) {
            Ok(st) => Some(st),
            // on error, fall back to normal readdir response for this entry
            Err(_) => None,
        };

        entries.push(AttrlistEntry {
            // kernel guarantees valid UTF-8
            name: name_str.to_string(),
            is_mountpoint: if let Some(st) = st {
                st.st_ino != dt_ino
            } else {
                false
            },
            st,
        });
    }

    Ok(entries)
}
