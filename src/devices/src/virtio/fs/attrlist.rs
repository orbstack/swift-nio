use std::{
    io,
    mem::{size_of, MaybeUninit},
    os::fd::AsRawFd,
};

use libc::{
    attrlist, getattrlistbulk, ATTR_BIT_MAP_COUNT, ATTR_CMNEXT_EXT_FLAGS, ATTR_CMN_ACCESSMASK,
    ATTR_CMN_ACCTIME, ATTR_CMN_CHGTIME, ATTR_CMN_DEVID, ATTR_CMN_FILEID, ATTR_CMN_FLAGS,
    ATTR_CMN_GRPID, ATTR_CMN_MODTIME, ATTR_CMN_NAME, ATTR_CMN_OBJTYPE, ATTR_CMN_OWNERID,
    ATTR_CMN_RETURNED_ATTRS, ATTR_DIR_ALLOCSIZE, ATTR_DIR_DATALENGTH, ATTR_DIR_ENTRYCOUNT,
    ATTR_DIR_IOBLOCKSIZE, ATTR_DIR_MOUNTSTATUS, ATTR_FILE_DATAALLOCSIZE, ATTR_FILE_DATALENGTH,
    ATTR_FILE_DEVTYPE, ATTR_FILE_IOBLOCKSIZE, ATTR_FILE_LINKCOUNT, DIR_MNTSTATUS_MNTPOINT,
    FSOPT_ATTR_CMN_EXTENDED,
};
use nix::errno::Errno;
use smallvec::SmallVec;
use tracing::{error, trace};

// __DARWIN_C_LEVEL >= __DARWIN_C_FULL
const ATTR_CMN_ERROR: u32 = 0x20000000;

const EF_NO_XATTRS: u64 = 0x00000002;

pub const INLINE_ENTRIES: usize = 16;

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

pub fn list_dir<T: AsRawFd>(
    dirfd: T,
    reserve_capacity: usize,
) -> io::Result<SmallVec<[AttrlistEntry; INLINE_ENTRIES]>> {
    // safe: we only use the part of buf that was read
    // 16384 = avg 128 bytes * 128 entries
    // to avoid compiler-inserted probe frame, don't exceed page size
    let mut buf: MaybeUninit<[u8; 16384]> = MaybeUninit::uninit();
    let buf = unsafe { buf.assume_init_mut() };

    let mut entries = SmallVec::new();
    entries.reserve_exact(reserve_capacity);

    let attrlist = attrlist {
        bitmapcount: ATTR_BIT_MAP_COUNT,
        reserved: 0,
        commonattr: ATTR_CMN_RETURNED_ATTRS
            | ATTR_CMN_NAME
            | ATTR_CMN_DEVID
            | ATTR_CMN_OBJTYPE
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
        let n = unsafe {
            getattrlistbulk(
                dirfd.as_raw_fd(),
                &attrlist as *const attrlist as *mut libc::c_void,
                buf.as_mut_ptr() as *mut libc::c_void,
                buf.len(),
                (FSOPT_ATTR_CMN_EXTENDED) as u64,
            )
        };
        if n < 0 {
            return Err(io::Error::last_os_error());
        }
        if n == 0 {
            break;
        }

        let mut p = buf.as_ptr();
        for i in 0..n {
            let entry_len = unsafe { (p as *const u32).read_unaligned() };
            trace!(n, i, entry_len, rem = buf.len(), "advance entry");
            // entry_len includes u32 size
            let mut entry_p = unsafe { p.add(size_of::<u32>()) };

            let returned = unsafe { *(entry_p as *const libc::attribute_set_t) };
            entry_p = unsafe { entry_p.add(size_of::<libc::attribute_set_t>()) };

            let mut entry = AttrlistEntry {
                name: String::new(),
                is_mountpoint: false,
                // must zero because fields could be left empty
                st: Some(unsafe { std::mem::zeroed() }),
            };
            let st = entry.st.as_mut().unwrap();

            let mut error: Option<Errno> = None;
            if returned.commonattr & ATTR_CMN_ERROR != 0 {
                let errno = unsafe { *(entry_p as *const u32) };
                error = Some(nix::errno::from_i32(errno as i32).into());
                entry_p = unsafe { entry_p.add(size_of::<u32>()) };
            }

            if returned.commonattr & ATTR_CMN_NAME != 0 {
                let name_ref = unsafe { *(entry_p as *const libc::attrreference_t) };
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
                st.st_dev = unsafe { (entry_p as *const libc::dev_t).read_unaligned() };
                entry_p = unsafe { entry_p.add(size_of::<libc::dev_t>()) };
            }

            if returned.commonattr & ATTR_CMN_OBJTYPE != 0 {
                let typ = unsafe { (entry_p as *const u32).read_unaligned() };
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
                entry_p = unsafe { entry_p.add(size_of::<u32>()) };
            }

            if returned.commonattr & ATTR_CMN_MODTIME != 0 {
                let time = unsafe { (entry_p as *const libc::timespec).read_unaligned() };
                st.st_mtime = time.tv_sec;
                st.st_mtime_nsec = time.tv_nsec;
                entry_p = unsafe { entry_p.add(size_of::<libc::timespec>()) };
            }

            if returned.commonattr & ATTR_CMN_CHGTIME != 0 {
                let time = unsafe { (entry_p as *const libc::timespec).read_unaligned() };
                st.st_ctime = time.tv_sec;
                st.st_ctime_nsec = time.tv_nsec;
                entry_p = unsafe { entry_p.add(size_of::<libc::timespec>()) };
            } else {
                // TODO substitute with mtime for unsupported FS?
            }

            if returned.commonattr & ATTR_CMN_ACCTIME != 0 {
                let time = unsafe { (entry_p as *const libc::timespec).read_unaligned() };
                st.st_atime = time.tv_sec;
                st.st_atime_nsec = time.tv_nsec;
                entry_p = unsafe { entry_p.add(size_of::<libc::timespec>()) };
            }

            if returned.commonattr & ATTR_CMN_OWNERID != 0 {
                st.st_uid = unsafe { (entry_p as *const libc::uid_t).read_unaligned() };
                entry_p = unsafe { entry_p.add(size_of::<libc::uid_t>()) };
            }

            if returned.commonattr & ATTR_CMN_GRPID != 0 {
                st.st_gid = unsafe { (entry_p as *const libc::gid_t).read_unaligned() };
                entry_p = unsafe { entry_p.add(size_of::<libc::gid_t>()) };
            }

            if returned.commonattr & ATTR_CMN_ACCESSMASK != 0 {
                st.st_mode |= unsafe { (entry_p as *const u16).read_unaligned() };
                entry_p = unsafe { entry_p.add(size_of::<u32>()) };
            }

            if returned.commonattr & ATTR_CMN_FLAGS != 0 {
                st.st_flags = unsafe { (entry_p as *const u32).read_unaligned() };
                entry_p = unsafe { entry_p.add(size_of::<u32>()) };
            }

            // getattrlist doesn't support st_gen
            // ATTR_CMN_GEN_COUNT = number of times file has been *modified*

            if returned.commonattr & ATTR_CMN_FILEID != 0 {
                let ino = unsafe { (entry_p as *const u64).read_unaligned() };
                st.st_ino = ino;
                entry_p = unsafe { entry_p.add(size_of::<u64>()) };
            }

            if returned.dirattr & ATTR_DIR_ENTRYCOUNT != 0 {
                // add 2 for "." and "..", like st_nlink on most filesystems
                st.st_nlink = 2 + unsafe { (entry_p as *const u16).read_unaligned() };
                entry_p = unsafe { entry_p.add(size_of::<u32>()) };
            }

            if returned.dirattr & ATTR_DIR_MOUNTSTATUS != 0 {
                let flags = unsafe { (entry_p as *const u32).read_unaligned() };
                if flags & DIR_MNTSTATUS_MNTPOINT != 0 {
                    entry.is_mountpoint = true;
                }
                entry_p = unsafe { entry_p.add(size_of::<u32>()) };
            }

            if returned.dirattr & ATTR_DIR_ALLOCSIZE != 0 {
                // always 512-blocks, regardless of st_blksize
                st.st_blocks = unsafe { (entry_p as *const i64).read_unaligned() } / 512;
                entry_p = unsafe { entry_p.add(size_of::<u64>()) };
            }

            if returned.dirattr & ATTR_DIR_IOBLOCKSIZE != 0 {
                st.st_blksize = unsafe { (entry_p as *const i32).read_unaligned() };
                entry_p = unsafe { entry_p.add(size_of::<u32>()) };
            }

            if returned.dirattr & ATTR_DIR_DATALENGTH != 0 {
                st.st_size = unsafe { (entry_p as *const i64).read_unaligned() };
                entry_p = unsafe { entry_p.add(size_of::<u64>()) };
            }

            if returned.fileattr & ATTR_FILE_LINKCOUNT != 0 {
                st.st_nlink = unsafe { (entry_p as *const u16).read_unaligned() };
                entry_p = unsafe { entry_p.add(size_of::<u32>()) };
            }

            if returned.fileattr & ATTR_FILE_IOBLOCKSIZE != 0 {
                st.st_blksize = unsafe { (entry_p as *const i32).read_unaligned() };
                entry_p = unsafe { entry_p.add(size_of::<u32>()) };
            }

            if returned.fileattr & ATTR_FILE_DEVTYPE != 0 {
                st.st_rdev = unsafe { (entry_p as *const libc::dev_t).read_unaligned() };
                entry_p = unsafe { entry_p.add(size_of::<libc::dev_t>()) };
            }

            if returned.fileattr & ATTR_FILE_DATALENGTH != 0 {
                st.st_size = unsafe { (entry_p as *const libc::off_t).read_unaligned() };
                entry_p = unsafe { entry_p.add(size_of::<libc::off_t>()) };
            }

            if returned.fileattr & ATTR_FILE_DATAALLOCSIZE != 0 {
                // always 512-blocks, regardless of st_blksize
                st.st_blocks = unsafe { (entry_p as *const libc::off_t).read_unaligned() } / 512;
                entry_p = unsafe { entry_p.add(size_of::<libc::off_t>()) };
            }

            if returned.forkattr & ATTR_CMNEXT_EXT_FLAGS != 0 {
                let flags = unsafe { (entry_p as *const u64).read_unaligned() };
                if flags & EF_NO_XATTRS == 0 {
                    // TODO need to read xattrs for this entry
                }

                #[allow(unused_assignments)]
                {
                    entry_p = unsafe { entry_p.add(size_of::<u64>()) };
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
