// Copyright 2019 The Chromium OS Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

use std::ffi::{CStr, CString};
use std::fs::set_permissions;
use std::fs::File;
use std::fs::Permissions;
use std::io;
use std::mem::{self, ManuallyDrop};
use std::os::fd::{AsFd, BorrowedFd, OwnedFd};
use std::os::unix::fs::PermissionsExt;
use std::os::unix::io::{AsRawFd, FromRawFd, RawFd};
use std::path::Path;
use std::ptr::slice_from_raw_parts;
use std::str::FromStr;
use std::sync::atomic::{AtomicBool, AtomicU64, Ordering};
use std::sync::Arc;
use std::time::Duration;

use bitflags::bitflags;
use libc::AT_FDCWD;
use nix::errno::Errno;
use nix::fcntl::OFlag;
use nix::sys::stat::fchmod;
use nix::sys::stat::{futimens, utimensat, Mode, UtimensatFlags};
use nix::sys::statfs::{fstatfs, statfs};
use nix::sys::statvfs::statvfs;
use nix::sys::statvfs::Statvfs;
use nix::sys::time::TimeSpec;
use nix::unistd::AccessFlags;
use nix::unistd::{access, LinkatFlags};
use nix::unistd::{ftruncate, symlinkat};
use parking_lot::{Mutex, RwLock};
use smallvec::SmallVec;
use vm_memory::ByteValued;

use crate::virtio::fs::attrlist::{self, AttrlistEntry, INLINE_ENTRIES};
use crate::virtio::fs::filesystem::SecContext;
use crate::virtio::fs::multikey::{MultikeyFxDashMap, ToAltKey};
use crate::virtio::linux_errno::nix_linux_error;
use crate::virtio::rosetta::get_rosetta_data;
use crate::virtio::{FxDashMap, NfsInfo};

use super::super::super::linux_errno::{linux_error, LINUX_ERANGE};
use super::super::bindings;
use super::super::filesystem::{
    Context, DirEntry, Entry, Extensions, FileSystem, FsOptions, GetxattrReply, ListxattrReply,
    OpenOptions, SetattrValid, ZeroCopyReader, ZeroCopyWriter,
};
use super::super::fuse;

// _IOC(_IOC_READ, 0x61, 0x22, 0x45)
const IOCTL_ROSETTA: u32 = 0x8045_6122;

const STAT_XATTR_KEY: &[u8] = b"user.orbstack.override_stat\0";

// pnpm and uv prefer clone, then fall back to hardlinks
// hard links are very slow on APFS (~170us to link+unlink) vs. clone (~65us)
const LINK_AS_CLONE_DIR_JS: &str = "node_modules";
const LINK_AS_CLONE_DIR_PY: &str = "site-packages";

const UID_MAX: u32 = u32::MAX - 1;

// 2 hours
// we invalidate via krpc
const DEFAULT_CACHE_TTL: Duration = Duration::from_secs(2 * 60 * 60);

const NSEC_PER_SEC: u64 = 1_000_000_000;
// maxfilesperproc=10240 on 8 GB x86
// must keep our own fd limit to avoid breaking vmgr
const MAX_PATH_FDS: u64 = 8000;

const CLONE_NOFOLLOW: u32 = 0x0001;

type NodeId = u64;
type Handle = u64;

struct DirStream {
    stream: u64,
    offset: i64,
    // OK because this is only for opened files
    entries: Option<SmallVec<[AttrlistEntry; INLINE_ENTRIES]>>,
}

struct HandleData {
    nodeid: NodeId,
    file: RwLock<ManuallyDrop<File>>,
    dirstream: Mutex<DirStream>,
}

impl Drop for HandleData {
    fn drop(&mut self) {
        let ds = self.dirstream.lock();
        if ds.stream != 0 {
            // this is a dir, and it had a stream open
            // closedir *closes* the fd passed to fdopendir (which is the fd that File holds)
            // so this invalidates the OwnedFd ownership
            unsafe { libc::closedir(ds.stream as *mut libc::DIR) };
        } else {
            // this is a file, or a dir with no stream open
            // manually drop File to close OwnedFd
            unsafe { ManuallyDrop::drop(&mut self.file.write()) };
        }
    }
}

#[repr(C, packed)]
#[derive(Clone, Copy, Debug, Default)]
struct LinuxDirent64 {
    d_ino: bindings::ino64_t,
    d_off: bindings::off64_t,
    d_reclen: libc::c_ushort,
    d_ty: libc::c_uchar,
}
unsafe impl ByteValued for LinuxDirent64 {}

fn ebadf() -> io::Error {
    linux_error(io::Error::from_raw_os_error(libc::EBADF))
}

fn einval() -> io::Error {
    linux_error(io::Error::from_raw_os_error(libc::EINVAL))
}

#[derive(Clone, Debug)]
enum FileRef<'a> {
    Path(&'a CStr),
    Fd(BorrowedFd<'a>),
}

fn item_to_value(item: &[u8], radix: u32) -> Option<u32> {
    match std::str::from_utf8(item) {
        Ok(val) => match u32::from_str_radix(val, radix) {
            Ok(i) => Some(i),
            Err(e) => {
                debug!("invalid value: {} err={}", radix, e);
                None
            }
        },
        Err(_) => None,
    }
}

fn get_xattr_stat(_file: FileRef) -> io::Result<Option<(u32, u32, u32)>> {
    /*
    let mut buf: Vec<u8> = vec![0; 32];
    let res = match file {
        StatFile::Path(path) => unsafe {
            let st = lstat(path, true)?;
            let options = if (st.st_mode & libc::S_IFMT) == libc::S_IFLNK {
                libc::XATTR_NOFOLLOW
            } else {
                0
            };
            libc::getxattr(
                path.as_ptr(),
                XATTR_KEY.as_ptr() as *const i8,
                buf.as_mut_ptr() as *mut libc::c_void,
                32,
                0,
                options,
            )
        },
        StatFile::Fd(fd) => unsafe {
            let st = fstat(fd, true)?;
            let options = if (st.st_mode & libc::S_IFMT) == libc::S_IFLNK {
                libc::XATTR_NOFOLLOW
            } else {
                0
            };
            libc::fgetxattr(
                fd,
                XATTR_KEY.as_ptr() as *const i8,
                buf.as_mut_ptr() as *mut libc::c_void,
                64,
                0,
                options,
            )
        },
    };
    if res == -1 {
        debug!("fget_xattr error: {}", res);
        return Ok(None);
    }

    buf.resize(res as usize, 0);

    let mut items = buf.split(|c| *c == b':');

    let uid = match items.next() {
        Some(item) => match item_to_value(item, 10) {
            Some(item) => item,
            None => return Ok(None),
        },
        None => return Ok(None),
    };
    let gid = match items.next() {
        Some(item) => match item_to_value(item, 10) {
            Some(item) => item,
            None => return Ok(None),
        },
        None => return Ok(None),
    };
    let mode = match items.next() {
        Some(item) => match item_to_value(item, 8) {
            Some(item) => item,
            None => return Ok(None),
        },
        None => return Ok(None),
    };

    Ok(Some((uid, gid, mode)))
    */
    Ok(None)
}

fn is_valid_owner(owner: Option<(u32, u32)>) -> bool {
    if let Some(owner) = owner {
        if owner.0 < UID_MAX && owner.1 < UID_MAX {
            return true;
        }
    }

    false
}

// We won't need this once expressions like "if let ... &&" are allowed.
#[allow(clippy::unnecessary_unwrap)]
fn set_xattr_stat(
    _file: FileRef,
    _owner: Option<(u32, u32)>,
    _mode: Option<u32>,
) -> io::Result<()> {
    /*
    let (new_owner, new_mode) = if is_valid_owner(owner) && mode.is_some() {
        (owner.unwrap(), mode.unwrap())
    } else {
        let (orig_owner, orig_mode) =
            if let Some((xuid, xgid, xmode)) = get_xattr_stat(file.clone())? {
                ((xuid, xgid), xmode)
            } else {
                ((0, 0), 0o0777)
            };

        let new_owner = match owner {
            Some(o) => {
                let uid = if o.0 < UID_MAX { o.0 } else { orig_owner.0 };
                let gid = if o.1 < UID_MAX { o.1 } else { orig_owner.1 };
                (uid, gid)
            }
            None => orig_owner,
        };

        (new_owner, mode.unwrap_or(orig_mode))
    };

    let buf = format!("{}:{}:0{:o}", new_owner.0, new_owner.1, new_mode);

    let res = match file {
        StatFile::Path(path) => unsafe {
            let st = lstat(path, true)?;
            let options = if (st.st_mode & libc::S_IFMT) == libc::S_IFLNK {
                libc::XATTR_NOFOLLOW
            } else {
                0
            };
            libc::setxattr(
                path.as_ptr(),
                XATTR_KEY.as_ptr() as *const i8,
                buf.as_ptr() as *mut libc::c_void,
                buf.len() as libc::size_t,
                0,
                options,
            )
        },
        StatFile::Fd(fd) => unsafe {
            let st = fstat(fd, true)?;
            let options = if (st.st_mode & libc::S_IFMT) == libc::S_IFLNK {
                libc::XATTR_NOFOLLOW
            } else {
                0
            };
            libc::fsetxattr(
                fd,
                XATTR_KEY.as_ptr() as *const i8,
                buf.as_ptr() as *mut libc::c_void,
                buf.len() as libc::size_t,
                0,
                options,
            )
        },
    };

    if res == -1 {
        Err(linux_error(io::Error::last_os_error()))
    } else {
        Ok(())
    }
    */
    Ok(())
}

fn fstat<T: AsFd>(fd: T, host: bool) -> io::Result<bindings::stat64> {
    let mut st = nix::sys::stat::fstat(fd.as_fd().as_raw_fd()).map_err(nix_linux_error)?;

    if !host {
        if let Some((uid, gid, mode)) = get_xattr_stat(FileRef::Fd(fd.as_fd()))? {
            st.st_uid = uid;
            st.st_gid = gid;
            if mode as u16 & libc::S_IFMT == 0 {
                st.st_mode = (st.st_mode & libc::S_IFMT) | mode as u16;
            } else {
                st.st_mode = mode as u16;
            }
        }
    }

    Ok(st)
}

fn lstat(c_path: &CStr, host: bool) -> io::Result<bindings::stat64> {
    let mut st = nix::sys::stat::lstat(c_path.as_ref()).map_err(nix_linux_error)?;

    if !host {
        if let Some((uid, gid, mode)) = get_xattr_stat(FileRef::Path(c_path))? {
            st.st_uid = uid;
            st.st_gid = gid;
            if mode as u16 & libc::S_IFMT == 0 {
                st.st_mode = (st.st_mode & libc::S_IFMT) | mode as u16;
            } else {
                st.st_mode = mode as u16;
            }
        }
    }

    Ok(st)
}

fn get_path_by_fd<T: AsRawFd>(fd: T) -> io::Result<String> {
    // allocate it on stack
    debug!("get_path_by_fd: fd={}", fd.as_raw_fd());
    let mut path_buf: [u8; 1024] = [0; 1024];
    let ret = unsafe { libc::fcntl(fd.as_raw_fd(), libc::F_GETPATH, &mut path_buf) };
    if ret == -1 {
        return Err(io::Error::last_os_error());
    }

    // cstr to find length
    let cstr = CStr::from_bytes_until_nul(&path_buf).map_err(|_| einval())?;
    // safe: kernel guarantees UTF-8
    Ok(unsafe { String::from_utf8_unchecked(cstr.to_bytes().to_vec()) })
}

/// The caching policy that the file system should report to the FUSE client. By default the FUSE
/// protocol uses close-to-open consistency. This means that any cached contents of the file are
/// invalidated the next time that file is opened.
#[derive(Debug, Default, Clone)]
pub enum CachePolicy {
    /// The client should never cache file data and all I/O should be directly forwarded to the
    /// server. This policy must be selected when file contents may change without the knowledge of
    /// the FUSE client (i.e., the file system does not have exclusive access to the directory).
    Never,

    /// The client is free to choose when and how to cache file data. This is the default policy and
    /// uses close-to-open consistency as described in the enum documentation.
    #[default]
    Auto,

    /// The client should always cache file data. This means that the FUSE client will not
    /// invalidate any cached data that was returned by the file system the last time the file was
    /// opened. This policy should only be selected when the file system has exclusive access to the
    /// directory.
    Always,
}

impl FromStr for CachePolicy {
    type Err = &'static str;

    fn from_str(s: &str) -> Result<Self, Self::Err> {
        match s {
            "never" | "Never" | "NEVER" => Ok(CachePolicy::Never),
            "auto" | "Auto" | "AUTO" => Ok(CachePolicy::Auto),
            "always" | "Always" | "ALWAYS" => Ok(CachePolicy::Always),
            _ => Err("invalid cache policy"),
        }
    }
}

/// Options that configure the behavior of the file system.
#[derive(Debug, Clone)]
pub struct Config {
    /// How long the FUSE client should consider directory entries to be valid. If the contents of a
    /// directory can only be modified by the FUSE client (i.e., the file system has exclusive
    /// access), then this should be a large value.
    ///
    /// The default value for this option is 5 seconds.
    pub entry_timeout: Duration,

    /// How long the FUSE client should consider file and directory attributes to be valid. If the
    /// attributes of a file or directory can only be modified by the FUSE client (i.e., the file
    /// system has exclusive access), then this should be set to a large value.
    ///
    /// The default value for this option is 5 seconds.
    pub attr_timeout: Duration,

    /// The caching policy the file system should use. See the documentation of `CachePolicy` for
    /// more details.
    pub cache_policy: CachePolicy,

    /// Whether the file system should enabled writeback caching. This can improve performance as it
    /// allows the FUSE client to cache and coalesce multiple writes before sending them to the file
    /// system. However, enabling this option can increase the risk of data corruption if the file
    /// contents can change without the knowledge of the FUSE client (i.e., the server does **NOT**
    /// have exclusive access). Additionally, the file system should have read access to all files
    /// in the directory it is serving as the FUSE client may send read requests even for files
    /// opened with `O_WRONLY`.
    ///
    /// Therefore callers should only enable this option when they can guarantee that: 1) the file
    /// system has exclusive access to the directory and 2) the file system has read permissions for
    /// all files in that directory.
    ///
    /// The default value for this option is `false`.
    pub writeback: bool,

    /// The path of the root directory.
    ///
    /// The default is `/`.
    pub root_dir: String,

    /// Whether the file system should support Extended Attributes (xattr). Enabling this feature may
    /// have a significant impact on performance, especially on write parallelism. This is the result
    /// of FUSE attempting to remove the special file privileges after each write request.
    ///
    /// The default value for this options is `false`.
    pub xattr: bool,

    /// Optional file descriptor for /proc/self/fd. Callers can obtain a file descriptor and pass it
    /// here, so there's no need to open it in PassthroughFs::new(). This is specially useful for
    /// sandboxing.
    ///
    /// The default is `None`.
    pub proc_sfd_rawfd: Option<RawFd>,

    pub allow_rosetta_ioctl: bool,
    pub nfs_info: Option<NfsInfo>,
}

impl Default for Config {
    fn default() -> Self {
        Config {
            entry_timeout: DEFAULT_CACHE_TTL,
            attr_timeout: DEFAULT_CACHE_TTL,
            cache_policy: Default::default(),
            writeback: false,
            root_dir: String::from("/"),
            // OK for perf because we block security.capability in kernel
            xattr: true,
            proc_sfd_rawfd: None,
            allow_rosetta_ioctl: false,
            nfs_info: None,
        }
    }
}

struct NodeData {
    nodeid: NodeId,
    dev_ino: DevIno,

    // state
    refcount: AtomicU64,
    last_ctime: AtomicU64,

    // cached stat info
    flags: NodeFlags,
    nlink: u16,

    // if volfs not supported
    fd: Option<OwnedFd>,
}

impl ToAltKey<DevIno> for NodeData {
    fn to_alt_key(&self) -> DevIno {
        self.dev_ino
    }
}

bitflags! {
    pub struct NodeFlags: u32 {
        const LINK_AS_CLONE = 1 << 0;
    }
}

type DevIno = (i32, u64);

fn st_ctime(st: &bindings::stat64) -> u64 {
    st.st_ctime as u64 * NSEC_PER_SEC + st.st_ctime_nsec as u64
}

/// A file system that simply "passes through" all requests it receives to the underlying file
/// system. To keep the implementation simple it servers the contents of its root directory. Users
/// that wish to serve only a specific directory should set up the environment so that that
/// directory ends up as the root of the file system process. One way to accomplish this is via a
/// combination of mount namespaces and the pivot_root system call.
pub struct PassthroughFs {
    // this is intentionally a bit racy, for performance
    // we get away with it because:
    // - nodeids are always unique and never reused or replaced, due to atomic u64 key
    // - reverse (DevIno) mappings are fallible and creating duplicate nodeids is OK
    nodeids: MultikeyFxDashMap<NodeId, DevIno, NodeData>,
    next_nodeid: AtomicU64,

    handles: FxDashMap<Handle, Arc<HandleData>>,
    next_handle: AtomicU64,

    // volfs supported?
    dev_info: FxDashMap<i32, bool>,
    num_open_fds: AtomicU64,

    // Whether writeback caching is enabled for this directory. This will only be true when
    // `cfg.writeback` is true and `init` was called with `FsOptions::WRITEBACK_CACHE`.
    writeback: AtomicBool,
    cfg: Config,
}

impl PassthroughFs {
    pub fn new(cfg: Config) -> io::Result<PassthroughFs> {
        // init with root nodeid
        let st = nix::sys::stat::stat(Path::new(&cfg.root_dir))?;
        let nodeids = MultikeyFxDashMap::new();
        nodeids.insert(
            fuse::ROOT_ID,
            (st.st_dev, st.st_ino),
            NodeData {
                nodeid: fuse::ROOT_ID,
                dev_ino: (st.st_dev, st.st_ino),
                // refcount 2 so it can never be dropped
                refcount: AtomicU64::new(2),
                last_ctime: AtomicU64::new(st_ctime(&st)),
                flags: NodeFlags::empty(),
                fd: None,
                nlink: st.st_nlink,
            },
        );

        let dev_info = FxDashMap::default();
        dev_info.insert(st.st_dev, true);

        Ok(PassthroughFs {
            nodeids,
            next_nodeid: AtomicU64::new(fuse::ROOT_ID + 1),

            handles: FxDashMap::default(),
            next_handle: AtomicU64::new(1),

            dev_info,
            num_open_fds: AtomicU64::new(0),

            writeback: AtomicBool::new(false),
            cfg,
        })
    }

    // TODO: fix possible race with fd close, without Arc
    fn get_nodeid(&self, nodeid: NodeId) -> io::Result<(DevIno, NodeFlags, Option<RawFd>)> {
        // race OK: primary key lookup only
        let node = self.nodeids.get(&nodeid).ok_or_else(ebadf)?;
        Ok((
            node.dev_ino,
            node.flags,
            node.fd.as_ref().map(|fd| fd.as_raw_fd()),
        ))
    }

    // note: /.vol (volfs) is undocumented and deprecated
    // but worst-case scenario: we can use public APIs (fsgetpath) to get the path,
    // and also cache O_EVTONLY fds and paths.
    // lstat realpath=681.85ns, volfs=895.88ns, fsgetpath=1.1478us, lstat+fsgetpath=1.8592us
    // TODO: unify with name_to_path(NodeId, Option<N>)
    fn nodeid_to_path(&self, nodeid: NodeId) -> io::Result<CString> {
        let ((device, inode), _, fd) = self.get_nodeid(nodeid)?;
        let path = if let Some(fd) = fd {
            // to minimize race window and support renames, get latest path from fd
            // this also allows minimal opens (EVTONLY | RDONLY)
            // TODO: all handlers should support Fd or Path. this is just lowest-effort impl
            get_path_by_fd(fd)?
        } else {
            format!("/.vol/{}/{}", device, inode)
        };

        let cstr = CString::new(path).map_err(|_| einval())?;
        Ok(cstr)
    }

    fn name_to_path_and_data(
        &self,
        parent: NodeId,
        name: &str,
    ) -> io::Result<(CString, DevIno, NodeFlags)> {
        let ((parent_device, parent_inode), parent_flags, fd) = self.get_nodeid(parent)?;
        let path = if let Some(fd) = fd {
            // to minimize race window and support renames, get latest path from fd
            // this also allows minimal opens (EVTONLY | RDONLY)
            // TODO: all handlers should support Fd or Path. this is just lowest-effort impl
            format!("{}/{}", get_path_by_fd(fd)?, name)
        } else {
            format!("/.vol/{}/{}/{}", parent_device, parent_inode, name)
        };

        let cstr = CString::new(path).map_err(|_| einval())?;
        Ok((cstr, (parent_device, parent_inode), parent_flags))
    }

    fn name_to_path(&self, parent: NodeId, name: &str) -> io::Result<CString> {
        Ok(self.name_to_path_and_data(parent, name)?.0)
    }

    fn devino_to_path(&self, devino: DevIno) -> io::Result<CString> {
        let (dev, ino) = devino;
        let path = format!("/.vol/{}/{}", dev, ino);
        let cstr = CString::new(path).map_err(|_| einval())?;
        Ok(cstr)
    }

    fn open_nodeid(&self, nodeid: NodeId, mut flags: OFlag) -> io::Result<File> {
        // When writeback caching is enabled, the kernel may send read requests even if the
        // userspace program opened the file write-only. So we need to ensure that we have opened
        // the file for reading as well as writing.
        let writeback = self.writeback.load(Ordering::Relaxed);
        if writeback && flags & OFlag::O_ACCMODE == OFlag::O_WRONLY {
            flags.remove(OFlag::O_ACCMODE);
            flags |= OFlag::O_RDWR;
        }

        // When writeback caching is enabled the kernel is responsible for handling `O_APPEND`.
        // However, this breaks atomicity as the file may have changed on disk, invalidating the
        // cached copy of the data in the kernel and the offset that the kernel thinks is the end of
        // the file. Just allow this for now as it is the user's responsibility to enable writeback
        // caching only for directories that are not shared. It also means that we need to clear the
        // `O_APPEND` flag.
        if writeback {
            flags.remove(OFlag::O_APPEND);
        }

        let c_path = self.nodeid_to_path(nodeid)?;

        flags |= OFlag::O_CLOEXEC;
        flags.remove(OFlag::O_NOFOLLOW | OFlag::O_EXLOCK);

        let fd =
            nix::fcntl::open(c_path.as_ref(), flags, Mode::empty()).map_err(nix_linux_error)?;

        // Safe because we just opened this fd.
        Ok(unsafe { File::from_raw_fd(fd) })
    }

    fn dev_supports_volfs(&self, dev: i32, file_ref: &FileRef) -> io::Result<bool> {
        if let Some(supported) = self.dev_info.get(&dev) {
            return Ok(*supported);
        }

        // not in cache: check it
        // statfs doesn't trigger TCC (only open does)
        let stf = match file_ref {
            FileRef::Path(c_path) => statfs(c_path.as_ref()),
            FileRef::Fd(fd) => fstatfs(fd),
        }
        .map_err(nix_linux_error)?;
        // transmute type (repr(transparent))
        let stf = unsafe { mem::transmute::<_, libc::statfs>(stf) };
        let supported = (stf.f_flags & libc::MNT_DOVOLFS as u32) != 0;

        debug!(
            "dev_supports_volfs: dev={} ref={:?} supported={}",
            dev, file_ref, supported
        );
        // race OK: will be the same result
        self.dev_info.insert(dev, supported);
        Ok(supported)
    }

    fn do_lookup(&self, parent: NodeId, name: &str, ctx: &Context) -> io::Result<Entry> {
        let (mut c_path, (parent_dev, parent_ino), parent_flags) =
            self.name_to_path_and_data(parent, &name)?;
        // looking up nfs mountpoint should return a dummy empty dir
        // for simplicity we can always just use /var/empty
        if let Some(nfs_info) = self.cfg.nfs_info.as_ref() {
            if nfs_info.parent_dir_dev == parent_dev
                && nfs_info.parent_dir_inode == parent_ino
                && nfs_info.dir_name == name
            {
                c_path = CString::new("/var/empty")?;
            }
        }

        let st = lstat(&c_path, false)?;
        self.finish_lookup(parent_flags, name, st, FileRef::Path(&c_path), ctx)
    }

    fn finish_lookup(
        &self,
        parent_flags: NodeFlags,
        name: &str,
        mut st: bindings::stat64,
        file_ref: FileRef,
        ctx: &Context,
    ) -> io::Result<Entry> {
        // TODO: remove on perms
        st.st_uid = ctx.uid;
        st.st_gid = ctx.gid;

        debug!(
            "finish_lookup: dev={} ino={} ref={:?}",
            st.st_dev, st.st_ino, file_ref
        );

        // race OK: if we fail to find a nodeid by (dev,ino), we'll just make a new one, and old one will gradually be forgotten
        let dev_ino = (st.st_dev, st.st_ino);
        let nodeid = if let Some(node) = self.nodeids.get_alt(&dev_ino) {
            // there is already a nodeid for this (dev, ino)
            // increment the refcount and return it
            node.refcount.fetch_add(1, Ordering::Relaxed);
            node.nodeid
        } else {
            // this (dev, ino) is new
            // create a new nodeid and return it
            let nodeid = self.next_nodeid.fetch_add(1, Ordering::Relaxed);

            // open fd if volfs is not supported
            // but DO NOT open char devs or block devs - could block, will likely fail, doesn't work thru virtiofs
            let typ = st.st_mode & libc::S_IFMT;
            let fd = if (typ != libc::S_IFCHR && typ != libc::S_IFBLK)
                && !self.dev_supports_volfs(st.st_dev, &file_ref)?
            {
                debug!("open fd");

                // TODO: evict fds and cache as paths
                if self.num_open_fds.fetch_add(1, Ordering::Relaxed) > MAX_PATH_FDS {
                    self.num_open_fds.fetch_sub(1, Ordering::Relaxed);
                    return Err(linux_error(io::Error::from_raw_os_error(libc::ENFILE)));
                }

                // OFlag::from_bits_truncate truncates O_SYMLINK
                let oflag = unsafe {
                    OFlag::from_bits_unchecked(
                        // SYMLINK implies NOFOLLOW, but NOFOLLOW prevents actually opening the symlink
                        libc::O_EVTONLY | libc::O_CLOEXEC | libc::O_SYMLINK,
                    )
                };

                // must reopen even if we have fd, to get O_EVTONLY. dup can't do that
                let fd = match file_ref {
                    FileRef::Path(c_path) => {
                        nix::fcntl::open(c_path.as_ref(), oflag, Mode::empty())
                    }
                    FileRef::Fd(fd) => {
                        // TODO: faster to ask caller for c_path here
                        nix::fcntl::open(Path::new(&get_path_by_fd(fd)?), oflag, Mode::empty())
                    }
                }
                .map_err(nix_linux_error)?;
                Some(unsafe { OwnedFd::from_raw_fd(fd) })
            } else {
                debug!("skip open");
                None
            };

            let mut node = NodeData {
                nodeid,
                dev_ino,
                refcount: AtomicU64::new(1),
                last_ctime: AtomicU64::new(st_ctime(&st)),
                flags: parent_flags,
                fd,
                nlink: st.st_nlink,
            };

            if name == LINK_AS_CLONE_DIR_JS || name == LINK_AS_CLONE_DIR_PY {
                node.flags |= NodeFlags::LINK_AS_CLONE;
            }

            // deadlock OK: we're not holding a ref, since lookup returned None
            self.nodeids.insert(nodeid, dev_ino, node);
            nodeid
        };

        Ok(Entry {
            nodeid,
            // root generation must be zero
            generation: if nodeid == fuse::ROOT_ID {
                0
            } else {
                st.st_gen as u64
            },
            attr: st,
            attr_timeout: self.cfg.attr_timeout,
            entry_timeout: self.cfg.entry_timeout,
        })
    }

    fn do_forget(&self, nodeid: NodeId, count: u64) {
        debug!("do_forget: nodeid={} count={}", nodeid, count);
        // race OK: primary key lookup only
        if let Some(node) = self.nodeids.get(&nodeid) {
            // decrement the refcount
            if node.refcount.fetch_sub(count, Ordering::Relaxed) == count {
                // count - count = 0
                // this nodeid is no longer in use

                // decrement open fds
                if let Some(_) = node.fd.as_ref() {
                    self.num_open_fds.fetch_sub(1, Ordering::Relaxed);
                }

                // remove from multikey alt mapping, so next lookup with (dev,ino) creates a new nodeid
                // race OK: we make sure K2 will never map to a missing K1
                self.nodeids.remove_alt(&node);

                // remove nodeid from map. nodeids are never reused
                // race OK: if there's a race and someone gets K2->K1 mapping, then finds that K1 is missing, it's OK because we'll just create a new nodeid for the same K2
                // deadlock OK: we drop node ref to release read lock for the shard. avoid .entry() because it takes write shard lock
                drop(node);
                self.nodeids.remove_main(&nodeid);
            }
        }
    }

    fn do_readdir<F>(
        &self,
        nodeid: NodeId,
        handle: Handle,
        size: u32,
        offset: u64,
        mut add_entry: F,
    ) -> io::Result<()>
    where
        F: FnMut(DirEntry) -> io::Result<usize>,
    {
        if size == 0 {
            return Ok(());
        }

        let data = self
            .handles
            .get(&handle)
            .filter(|hd| hd.nodeid == nodeid)
            .map(|v| v.clone())
            .ok_or_else(ebadf)?;
        // race OK: FUSE won't FORGET until all handles are closed
        let (dev, _) = self.nodeids.get(&nodeid).ok_or_else(ebadf)?.dev_ino;

        let mut ds = data.dirstream.lock();

        // dir stream is opened lazily in case client calls opendir() then releasedir() without ever reading entries
        let dir_stream = if ds.stream == 0 {
            let dir = unsafe { libc::fdopendir(data.file.write().as_raw_fd()) };
            if dir.is_null() {
                return Err(linux_error(io::Error::last_os_error()));
            }
            ds.stream = dir as u64;
            dir
        } else {
            ds.stream as *mut libc::DIR
        };

        if (offset as i64) != ds.offset {
            unsafe { libc::seekdir(dir_stream, offset as i64) };
        }

        loop {
            ds.offset = unsafe { libc::telldir(dir_stream) };

            let dentry = unsafe { libc::readdir(dir_stream) };
            if dentry.is_null() {
                break;
            }

            let name = unsafe {
                CStr::from_bytes_until_nul(&*slice_from_raw_parts(
                    (*dentry).d_name.as_ptr() as *const u8,
                    (*dentry).d_name.len(),
                ))
                .unwrap()
                .to_bytes()
            };

            if name == b"." || name == b".." {
                continue;
            }

            let mut ino = unsafe { (*dentry).d_ino };
            if let Some(nfs_info) = self.cfg.nfs_info.as_ref() {
                // replace nfs mountpoint ino with /var/empty - that's what lookup returns
                if dev == nfs_info.dir_dev && ino == nfs_info.dir_inode {
                    ino = nfs_info.empty_dir_inode;
                }
            }

            let res = unsafe {
                add_entry(DirEntry {
                    ino,
                    offset: (ds.offset + 1) as u64,
                    type_: u32::from((*dentry).d_type),
                    name,
                })
            };

            match res {
                Ok(size) => {
                    if size == 0 {
                        unsafe { libc::seekdir(dir_stream, ds.offset) };
                        break;
                    }
                }
                Err(e) => {
                    error!(
                        "failed to add entry {}: {:?}",
                        std::str::from_utf8(&name).unwrap(),
                        e
                    );
                    continue;
                }
            }
        }

        Ok(())
    }

    fn do_open(&self, nodeid: NodeId, flags: u32) -> io::Result<(Option<Handle>, OpenOptions)> {
        let flags = self.parse_open_flags(flags as i32);

        let file = self.open_nodeid(nodeid, flags)?;
        // early stat to avoid broken handle state if it fails
        let st = fstat(&file, false)?;

        let handle = self.next_handle.fetch_add(1, Ordering::Relaxed);
        let data = HandleData {
            nodeid,
            file: RwLock::new(ManuallyDrop::new(file)),
            dirstream: Mutex::new(DirStream {
                stream: 0,
                offset: 0,
                entries: None,
            }),
        };

        self.handles.insert(handle, Arc::new(data));

        let mut opts = OpenOptions::empty();
        match self.cfg.cache_policy {
            // We only set the direct I/O option on files.
            CachePolicy::Never => {
                opts.set(OpenOptions::DIRECT_IO, !flags.contains(OFlag::O_DIRECTORY))
            }
            CachePolicy::Auto => {
                if !flags.contains(OFlag::O_DIRECTORY) {
                    // file: provide CTO consistency
                    // check ctime, and invalidate cache if ctime has changed
                    // race OK: we'll just be missing cache for a file
                    // TODO: reuse from earlier lookup
                    if let Some(node) = self.nodeids.get(&nodeid) {
                        let ctime = st_ctime(&st);
                        if node.last_ctime.swap(ctime, Ordering::Relaxed) == ctime {
                            opts |= OpenOptions::KEEP_CACHE;
                        }
                    }
                } else {
                    // always cache directories (krpc invalidates)
                    // TODO: FUSE protocol is bad here. setting CACHE_DIR forces use of cache -- otherwise we could do ctime CTO invalidation. not settting it means that resulting dirents won't be cached for future calls.
                    opts |= OpenOptions::CACHE_DIR
                }
            }
            CachePolicy::Always => {
                if !flags.contains(OFlag::O_DIRECTORY) {
                    opts |= OpenOptions::KEEP_CACHE;
                } else {
                    opts |= OpenOptions::CACHE_DIR;
                }
            }
        };

        Ok((Some(handle), opts))
    }

    fn do_release(&self, nodeid: NodeId, handle: Handle) -> io::Result<()> {
        if let dashmap::mapref::entry::Entry::Occupied(e) = self.handles.entry(handle) {
            if e.get().nodeid == nodeid {
                // We don't need to close the file here because that will happen automatically when
                // the last `Arc` is dropped.
                e.remove();
                return Ok(());
            }
        }

        Err(ebadf())
    }

    fn do_getattr(&self, nodeid: NodeId, ctx: Context) -> io::Result<(bindings::stat64, Duration)> {
        let c_path = self.nodeid_to_path(nodeid)?;

        let mut st = lstat(&c_path, false)?;
        st.st_uid = ctx.uid;
        st.st_gid = ctx.gid;

        Ok((st, self.cfg.attr_timeout))
    }

    fn do_unlink(
        &self,
        _ctx: Context,
        parent: NodeId,
        name: &CStr,
        flags: libc::c_int,
    ) -> io::Result<()> {
        let c_path = self.name_to_path(parent, &name.to_string_lossy())?;

        // Safe because this doesn't modify any memory and we check the return value.
        let res = unsafe { libc::unlinkat(AT_FDCWD, c_path.as_ptr(), flags) };

        if res == 0 {
            Ok(())
        } else {
            Err(linux_error(io::Error::last_os_error()))
        }
    }

    fn parse_open_flags(&self, flags: i32) -> OFlag {
        let mut mflags: i32 = flags & 0b11;

        if (flags & bindings::LINUX_O_NONBLOCK) != 0 {
            mflags |= libc::O_NONBLOCK;
        }
        if (flags & bindings::LINUX_O_APPEND) != 0 {
            mflags |= libc::O_APPEND;
        }
        if (flags & bindings::LINUX_O_CREAT) != 0 {
            mflags |= libc::O_CREAT;
        }
        if (flags & bindings::LINUX_O_TRUNC) != 0 {
            mflags |= libc::O_TRUNC;
        }
        if (flags & bindings::LINUX_O_EXCL) != 0 {
            mflags |= libc::O_EXCL;
        }
        if (flags & bindings::LINUX_O_NOFOLLOW) != 0 {
            mflags |= libc::O_NOFOLLOW;
        }
        if (flags & bindings::LINUX_O_CLOEXEC) != 0 {
            mflags |= libc::O_CLOEXEC;
        }

        unsafe { OFlag::from_bits_unchecked(mflags) }
    }
}

fn set_secctx(file: FileRef, secctx: SecContext, symlink: bool) -> io::Result<()> {
    let options = if symlink { libc::XATTR_NOFOLLOW } else { 0 };
    let ret = match file {
        FileRef::Path(path) => unsafe {
            libc::setxattr(
                path.as_ptr(),
                secctx.name.as_ptr(),
                secctx.secctx.as_ptr() as *const libc::c_void,
                secctx.secctx.len(),
                0,
                options,
            )
        },
        FileRef::Fd(fd) => unsafe {
            libc::fsetxattr(
                fd.as_raw_fd(),
                secctx.name.as_ptr(),
                secctx.secctx.as_ptr() as *const libc::c_void,
                secctx.secctx.len(),
                0,
                options,
            )
        },
    };

    if ret != 0 {
        Err(io::Error::last_os_error())
    } else {
        Ok(())
    }
}

impl FileSystem for PassthroughFs {
    type NodeId = NodeId;
    type Handle = Handle;

    fn hvc_id(&self) -> Option<usize> {
        Some(if self.cfg.root_dir == "/" { 0 } else { 1 })
    }

    fn init(&self, capable: FsOptions) -> io::Result<FsOptions> {
        // Safe because this doesn't modify any memory and there is no need to check the return
        // value because this system call always succeeds. We need to clear the umask here because
        // we want the client to be able to set all the bits in the mode.
        unsafe { libc::umask(0o000) };

        // always use readdirplus. most readdir usages will lead to advise readdirplus, and it's almost always worth it from a syscall perspective
        let mut opts = FsOptions::DO_READDIRPLUS;
        if self.cfg.writeback && capable.contains(FsOptions::WRITEBACK_CACHE) {
            opts |= FsOptions::WRITEBACK_CACHE;
            self.writeback.store(true, Ordering::Relaxed);
        }
        Ok(opts)
    }

    fn destroy(&self) {
        self.handles.clear();
        self.nodeids.clear();
    }

    fn statfs(&self, _ctx: Context, nodeid: NodeId) -> io::Result<Statvfs> {
        let c_path = self.nodeid_to_path(nodeid)?;
        statvfs(c_path.as_ref()).map_err(nix_linux_error)
    }

    fn lookup(&self, _ctx: Context, parent: NodeId, name: &CStr) -> io::Result<Entry> {
        debug!("lookup: {:?}", name);
        self.do_lookup(parent, &name.to_string_lossy(), &_ctx)
    }

    fn forget(&self, _ctx: Context, _nodeid: NodeId, _count: u64) {
        self.do_forget(_nodeid, _count)
    }

    fn batch_forget(&self, _ctx: Context, _requests: Vec<(NodeId, u64)>) {
        for (nodeid, count) in _requests {
            self.do_forget(nodeid, count)
        }
    }

    fn opendir(
        &self,
        _ctx: Context,
        nodeid: NodeId,
        flags: u32,
    ) -> io::Result<(Option<Handle>, OpenOptions)> {
        self.do_open(nodeid, flags | libc::O_DIRECTORY as u32)
    }

    fn releasedir(
        &self,
        _ctx: Context,
        nodeid: NodeId,
        _flags: u32,
        handle: Handle,
    ) -> io::Result<()> {
        self.do_release(nodeid, handle)
    }

    fn mkdir(
        &self,
        ctx: Context,
        parent: NodeId,
        name: &CStr,
        mode: u32,
        umask: u32,
        extensions: Extensions,
    ) -> io::Result<Entry> {
        let name = &name.to_string_lossy();
        let c_path = self.name_to_path(parent, name)?;

        // Safe because this doesn't modify any memory and we check the return value.
        let res = unsafe { libc::mkdir(c_path.as_ptr(), (mode & !umask) as u16) };
        if res == 0 {
            // Set security context
            if let Some(secctx) = extensions.secctx {
                set_secctx(FileRef::Path(&c_path), secctx, false)?
            };

            set_xattr_stat(
                FileRef::Path(&c_path),
                Some((ctx.uid, ctx.gid)),
                Some(mode & !umask),
            )?;
            self.do_lookup(parent, name, &ctx)
        } else {
            Err(linux_error(io::Error::last_os_error()))
        }
    }

    fn rmdir(&self, ctx: Context, parent: NodeId, name: &CStr) -> io::Result<()> {
        self.do_unlink(ctx, parent, name, libc::AT_REMOVEDIR)
    }

    fn readdir<F>(
        &self,
        _ctx: Context,
        nodeid: NodeId,
        handle: Handle,
        size: u32,
        offset: u64,
        add_entry: F,
    ) -> io::Result<()>
    where
        F: FnMut(DirEntry) -> io::Result<usize>,
    {
        self.do_readdir(nodeid, handle, size, offset, add_entry)
    }

    fn readdirplus<F>(
        &self,
        ctx: Context,
        nodeid: NodeId,
        handle: Handle,
        size: u32,
        offset: u64,
        mut add_entry: F,
    ) -> io::Result<()>
    where
        F: FnMut(DirEntry, Entry) -> io::Result<usize>,
    {
        // race OK: FUSE won't FORGET until all handles are closed
        let node = self.nodeids.get(&nodeid).ok_or_else(ebadf)?;
        let parent_flags = node.flags;
        let nlink = node.nlink;
        let (dev, ino) = node.dev_ino;
        // TODO: race still OK here because of FORGET, but need to fix
        let parent_fd_path = match node.fd.as_ref() {
            Some(f) => Some(get_path_by_fd(f.as_fd())?),
            None => None,
        };
        drop(node);

        // for NFS loop prevention to work, use legacy impl on home dir
        // getattrlistbulk on home can sometimes stat on mount and cause deadlock
        if let Some(nfs_info) = self.cfg.nfs_info.as_ref() {
            if nfs_info.parent_dir_dev == dev && nfs_info.parent_dir_inode == ino {
                return self.do_readdir(nodeid, handle, size, offset, |dir_entry| {
                    // refcount doesn't get messed up on error:
                    // failed entries are skipped, but readdirplus still returns success
                    // (necessary because FUSE doesn't retry readdirplus)
                    let name = unsafe { std::str::from_utf8_unchecked(dir_entry.name) };
                    let entry = self.do_lookup(nodeid, name, &ctx)?;
                    let new_nodeid = entry.nodeid;

                    match add_entry(dir_entry, entry) {
                        Ok(0) => {
                            // out of space
                            // forget this entry
                            self.do_forget(new_nodeid, 1);
                            Ok(0)
                        }
                        Ok(size) => Ok(size),
                        Err(e) => Err(e),
                    }
                });
            }
        }

        debug!(
            "readdirplus: nodeid={}, handle={}, size={}, offset={}",
            nodeid, handle, size, offset
        );
        if size == 0 {
            return Ok(());
        }

        let data = self
            .handles
            .get(&handle)
            .filter(|hd| hd.nodeid == nodeid)
            .map(|v| v.clone())
            .ok_or_else(ebadf)?;

        let mut ds = data.dirstream.lock();

        // read entries if not already done
        let entries = if let Some(entries) = ds.entries.as_ref() {
            entries
        } else {
            // reserve # entries = nlink - 2 ("." and "..")
            let capacity = nlink.saturating_sub(2);
            let file = data.file.write();
            let entries = attrlist::list_dir(file.as_fd(), capacity as usize)?;
            ds.entries = Some(entries);
            ds.entries.as_ref().unwrap()
        };

        if offset >= entries.len() as u64 {
            return Ok(());
        }

        for (i, entry) in entries[offset as usize..].iter().enumerate() {
            // we trust kernel to return valid utf-8 names
            debug!(
                "list_dir: name={} dev={} ino={} offset={}",
                &entry.name,
                entry.st.st_dev,
                entry.st.st_ino,
                offset + 1 + (i as u64)
            );

            // mountpoints must be looked up again. getattrlistbulk returns the orig fs mountpoint dir
            let lookup_entry = match if entry.is_mountpoint {
                self.do_lookup(nodeid, &entry.name, &ctx)
            } else {
                // only do path lookup once
                let path = match if let Some(ref path) = parent_fd_path {
                    CString::new(format!("{}/{}", path, entry.name)).map_err(|_| einval())
                } else {
                    self.devino_to_path((entry.st.st_dev, entry.st.st_ino))
                } {
                    Ok(path) => path,
                    Err(e) => {
                        error!("failed to lookup entry: {e}");
                        continue;
                    }
                };

                self.finish_lookup(
                    parent_flags,
                    &entry.name,
                    entry.st,
                    FileRef::Path(&path),
                    &ctx,
                )
            } {
                Ok(lookup_entry) => lookup_entry,
                Err(e) => {
                    error!("failed to lookup entry: {e}");
                    continue;
                }
            };

            let dir_entry = DirEntry {
                ino: entry.st.st_ino,
                offset: offset + 1 + (i as u64),
                type_: match entry.st.st_mode & libc::S_IFMT {
                    libc::S_IFREG => libc::DT_REG,
                    libc::S_IFDIR => libc::DT_DIR,
                    libc::S_IFLNK => libc::DT_LNK,
                    libc::S_IFCHR => libc::DT_CHR,
                    libc::S_IFBLK => libc::DT_BLK,
                    libc::S_IFIFO => libc::DT_FIFO,
                    _ => libc::DT_UNKNOWN,
                } as u32,
                name: entry.name.as_bytes(),
            };

            let new_nodeid = lookup_entry.nodeid;
            match add_entry(dir_entry, lookup_entry) {
                Ok(0) => {
                    // out of space
                    // forget this entry
                    self.do_forget(new_nodeid, 1);
                    break;
                }
                Ok(_) => {}
                Err(e) => {
                    // continue
                    error!("failed to add entry: {:?}", e);
                }
            }
        }

        Ok(())
    }

    fn open(
        &self,
        _ctx: Context,
        nodeid: NodeId,
        flags: u32,
    ) -> io::Result<(Option<Handle>, OpenOptions)> {
        self.do_open(nodeid, flags)
    }

    fn release(
        &self,
        _ctx: Context,
        nodeid: NodeId,
        _flags: u32,
        handle: Handle,
        _flush: bool,
        _flock_release: bool,
        _lock_owner: Option<u64>,
    ) -> io::Result<()> {
        self.do_release(nodeid, handle)
    }

    fn create(
        &self,
        ctx: Context,
        parent: NodeId,
        name: &CStr,
        mode: u32,
        flags: u32,
        umask: u32,
        extensions: Extensions,
    ) -> io::Result<(Entry, Option<Handle>, OpenOptions)> {
        let name = &name.to_string_lossy();
        let (c_path, _, parent_flags) = self.name_to_path_and_data(parent, name)?;

        let flags = self.parse_open_flags(flags as i32);

        // Safe because this doesn't modify any memory and we check the return value. We don't
        // really check `flags` because if the kernel can't handle poorly specified flags then we
        // have much bigger problems.
        let fd = unsafe {
            OwnedFd::from_raw_fd(
                nix::fcntl::open(
                    c_path.as_ref(),
                    flags | OFlag::O_CREAT | OFlag::O_CLOEXEC | OFlag::O_NOFOLLOW,
                    Mode::from_bits_unchecked(mode as u16),
                )
                .map_err(nix_linux_error)?,
            )
        };

        if let Err(e) = set_xattr_stat(
            FileRef::Fd(fd.as_fd()),
            Some((ctx.uid, ctx.gid)),
            Some(libc::S_IFREG as u32 | (mode & !(umask & 0o777))),
        ) {
            return Err(e);
        }

        // Set security context
        if let Some(secctx) = extensions.secctx {
            set_secctx(FileRef::Fd(fd.as_fd()), secctx, false)?
        };

        let st = fstat(&fd, false)?;
        let entry = self.finish_lookup(parent_flags, name, st, FileRef::Fd(fd.as_fd()), &ctx)?;

        let handle = self.next_handle.fetch_add(1, Ordering::Relaxed);
        let data = HandleData {
            nodeid: entry.nodeid,
            file: RwLock::new(ManuallyDrop::new(File::from(fd))),
            dirstream: Mutex::new(DirStream {
                stream: 0,
                offset: 0,
                entries: None,
            }),
        };

        self.handles.insert(handle, Arc::new(data));

        let mut opts = OpenOptions::empty();
        match self.cfg.cache_policy {
            CachePolicy::Never => opts |= OpenOptions::DIRECT_IO,
            CachePolicy::Auto => {
                // file: provide CTO consistency
                // check ctime, and invalidate cache if ctime has changed
                // race OK: we'll just be missing cache for a file
                // TODO: reuse from earlier lookup
                if let Some(node) = self.nodeids.get(&entry.nodeid) {
                    let ctime = st_ctime(&entry.attr);
                    if node.last_ctime.swap(ctime, Ordering::Relaxed) == ctime {
                        opts |= OpenOptions::KEEP_CACHE;
                    }
                }
            }
            CachePolicy::Always => opts |= OpenOptions::KEEP_CACHE,
        };

        Ok((entry, Some(handle), opts))
    }

    fn unlink(&self, ctx: Context, parent: NodeId, name: &CStr) -> io::Result<()> {
        self.do_unlink(ctx, parent, name, 0)
    }

    fn read<W: io::Write + ZeroCopyWriter>(
        &self,
        _ctx: Context,
        nodeid: NodeId,
        handle: Handle,
        mut w: W,
        size: u32,
        offset: u64,
        _lock_owner: Option<u64>,
        _flags: u32,
    ) -> io::Result<usize> {
        debug!("read: {:?}", nodeid);

        let data = self
            .handles
            .get(&handle)
            .filter(|hd| hd.nodeid == nodeid)
            .map(|v| v.clone())
            .ok_or_else(ebadf)?;

        // This is safe because write_from uses preadv64, so the underlying file descriptor
        // offset is not affected by this operation.
        let f = data.file.read();
        w.write_from(&f, size as usize, offset)
    }

    fn write<R: io::Read + ZeroCopyReader>(
        &self,
        _ctx: Context,
        nodeid: NodeId,
        handle: Handle,
        mut r: R,
        size: u32,
        offset: u64,
        _lock_owner: Option<u64>,
        _delayed_write: bool,
        _kill_priv: bool,
        _flags: u32,
    ) -> io::Result<usize> {
        let data = self
            .handles
            .get(&handle)
            .filter(|hd| hd.nodeid == nodeid)
            .map(|v| v.clone())
            .ok_or_else(ebadf)?;

        // This is safe because read_to uses pwritev64, so the underlying file descriptor
        // offset is not affected by this operation.
        let f = data.file.read();
        r.read_to(&f, size as usize, offset)
    }

    fn getattr(
        &self,
        _ctx: Context,
        nodeid: NodeId,
        _handle: Option<Handle>,
    ) -> io::Result<(bindings::stat64, Duration)> {
        self.do_getattr(nodeid, _ctx)
    }

    fn setattr(
        &self,
        _ctx: Context,
        nodeid: NodeId,
        attr: bindings::stat64,
        handle: Option<Handle>,
        valid: SetattrValid,
    ) -> io::Result<(bindings::stat64, Duration)> {
        let c_path = self.nodeid_to_path(nodeid)?;

        enum Data {
            Handle(RawFd),
            FilePath,
        }

        // If we have a handle then use it otherwise get a new fd from the nodeid.
        let data = if let Some(handle) = handle {
            let hd = self
                .handles
                .get(&handle)
                .filter(|hd| hd.nodeid == nodeid)
                .map(|v| v.clone())
                .ok_or_else(ebadf)?;

            let fd = hd.file.write().as_raw_fd();
            Data::Handle(fd)
        } else {
            Data::FilePath
        };

        if valid.contains(SetattrValid::MODE) {
            // TODO: store sticky bit in xattr
            match data {
                Data::Handle(fd) => {
                    fchmod(fd, Mode::from_bits_truncate(attr.st_mode))?;
                }
                Data::FilePath => {
                    set_permissions(
                        Path::new(&*c_path.to_string_lossy()),
                        Permissions::from_mode(attr.st_mode as u32),
                    )?;
                }
            }
        }

        if valid.intersects(SetattrValid::UID | SetattrValid::GID) {
            let uid = if valid.contains(SetattrValid::UID) {
                attr.st_uid
            } else {
                // Cannot use -1 here because these are unsigned values.
                std::u32::MAX
            };
            let gid = if valid.contains(SetattrValid::GID) {
                attr.st_gid
            } else {
                // Cannot use -1 here because these are unsigned values.
                std::u32::MAX
            };

            set_xattr_stat(FileRef::Path(&c_path), Some((uid, gid)), None)?;
        }

        if valid.contains(SetattrValid::SIZE) {
            // Safe because this doesn't modify any memory and we check the return value.
            match data {
                Data::Handle(fd) => ftruncate(fd, attr.st_size),
                _ => {
                    // There is no `ftruncateat` so we need to get a new fd and truncate it.
                    let f = self.open_nodeid(nodeid, OFlag::O_NONBLOCK | OFlag::O_RDWR)?;
                    ftruncate(f.as_raw_fd(), attr.st_size)
                }
            }
            .map_err(nix_linux_error)?;
        }

        if valid.intersects(SetattrValid::ATIME | SetattrValid::MTIME) {
            let mut atime = libc::timespec {
                tv_sec: 0,
                tv_nsec: libc::UTIME_OMIT,
            };
            let mut mtime = libc::timespec {
                tv_sec: 0,
                tv_nsec: libc::UTIME_OMIT,
            };

            if valid.contains(SetattrValid::ATIME_NOW) {
                atime.tv_nsec = libc::UTIME_NOW;
            } else if valid.contains(SetattrValid::ATIME) {
                atime.tv_sec = attr.st_atime;
                atime.tv_nsec = attr.st_atime_nsec;
            }

            if valid.contains(SetattrValid::MTIME_NOW) {
                mtime.tv_nsec = libc::UTIME_NOW;
            } else if valid.contains(SetattrValid::MTIME) {
                mtime.tv_sec = attr.st_mtime;
                mtime.tv_nsec = attr.st_mtime_nsec;
            }

            // Safe because this doesn't modify any memory and we check the return value.
            let atime = TimeSpec::from_timespec(atime);
            let mtime = TimeSpec::from_timespec(mtime);
            match data {
                Data::Handle(fd) => futimens(fd, &atime, &mtime),
                Data::FilePath => utimensat(
                    None,
                    c_path.as_ref(),
                    &atime,
                    &mtime,
                    UtimensatFlags::NoFollowSymlink,
                ),
            }
            .map_err(nix_linux_error)?;
        }

        self.do_getattr(nodeid, _ctx)
    }

    fn rename(
        &self,
        _ctx: Context,
        olddir: NodeId,
        oldname: &CStr,
        newdir: NodeId,
        newname: &CStr,
        flags: u32,
    ) -> io::Result<()> {
        let mut mflags: u32 = 0;
        if ((flags as i32) & bindings::LINUX_RENAME_NOREPLACE) != 0 {
            mflags |= libc::RENAME_EXCL;
        }
        if ((flags as i32) & bindings::LINUX_RENAME_EXCHANGE) != 0 {
            mflags |= libc::RENAME_SWAP;
        }

        if ((flags as i32) & bindings::LINUX_RENAME_WHITEOUT) != 0
            && ((flags as i32) & bindings::LINUX_RENAME_EXCHANGE) != 0
        {
            return Err(linux_error(io::Error::from_raw_os_error(libc::EINVAL)));
        }

        let old_cpath = self.name_to_path(olddir, &oldname.to_string_lossy())?;
        let new_cpath = self.name_to_path(newdir, &newname.to_string_lossy())?;

        let mut res = unsafe { libc::renamex_np(old_cpath.as_ptr(), new_cpath.as_ptr(), mflags) };
        // ENOTSUP = not supported by FS (e.g. NFS). retry and simulate if only flag is RENAME_EXCL
        // GNU coreutils 'mv' uses RENAME_EXCL so this is common
        // (hard to simulate RENAME_SWAP)
        if res == -1 && Errno::last() == Errno::ENOTSUP && mflags == libc::RENAME_EXCL {
            // EXCL means that target must not exist, so check it
            match access(new_cpath.as_ref(), AccessFlags::F_OK) {
                Ok(_) => return Err(linux_error(io::Error::from_raw_os_error(libc::EEXIST))),
                Err(Errno::ENOENT) => {}
                Err(e) => return Err(linux_error(io::Error::from_raw_os_error(e as i32))),
            }

            res = unsafe { libc::renamex_np(old_cpath.as_ptr(), new_cpath.as_ptr(), 0) }
        }

        if res == 0 {
            if ((flags as i32) & bindings::LINUX_RENAME_WHITEOUT) != 0 {
                if let Ok(fd) = nix::fcntl::open(
                    old_cpath.as_ref(),
                    OFlag::O_CREAT | OFlag::O_CLOEXEC | OFlag::O_NOFOLLOW,
                    Mode::from_bits_truncate(0o600),
                ) {
                    let fd = unsafe { OwnedFd::from_raw_fd(fd) };
                    if let Err(e) = set_xattr_stat(
                        FileRef::Fd(fd.as_fd()),
                        None,
                        Some((libc::S_IFCHR | 0o600) as u32),
                    ) {
                        return Err(e);
                    }
                }
            }

            Ok(())
        } else {
            Err(linux_error(io::Error::last_os_error()))
        }
    }

    fn mknod(
        &self,
        ctx: Context,
        parent: NodeId,
        name: &CStr,
        mode: u32,
        _rdev: u32,
        umask: u32,
        extensions: Extensions,
    ) -> io::Result<Entry> {
        let name = &name.to_string_lossy();
        let c_path = self.name_to_path(parent, name)?;

        let fd = unsafe {
            OwnedFd::from_raw_fd(
                nix::fcntl::open(
                    c_path.as_ref(),
                    OFlag::O_CREAT | OFlag::O_CLOEXEC | OFlag::O_NOFOLLOW,
                    Mode::from_bits_truncate(0o600),
                )
                .map_err(nix_linux_error)?,
            )
        };

        // Set security context
        if let Some(secctx) = extensions.secctx {
            set_secctx(FileRef::Fd(fd.as_fd()), secctx, false)?
        };

        if let Err(e) = set_xattr_stat(
            FileRef::Fd(fd.as_fd()),
            Some((ctx.uid, ctx.gid)),
            Some(mode & !umask),
        ) {
            return Err(e);
        }

        self.do_lookup(parent, name, &ctx)
    }

    fn link(
        &self,
        _ctx: Context,
        nodeid: NodeId,
        newparent: NodeId,
        newname: &CStr,
    ) -> io::Result<Entry> {
        let orig_c_path = self.nodeid_to_path(nodeid)?;
        let newname = &newname.to_string_lossy();
        let (link_c_path, _, parent_flags) = self.name_to_path_and_data(newparent, newname)?;

        // Safe because this doesn't modify any memory and we check the return value.
        if parent_flags.contains(NodeFlags::LINK_AS_CLONE) {
            // translate link to clonefile as a workaround for slow hardlinking on APFS, and because ioctl(FICLONE) semantics don't fit macOS well
            let res = unsafe {
                libc::clonefile(orig_c_path.as_ptr(), link_c_path.as_ptr(), CLONE_NOFOLLOW)
            };
            if res == -1 && Errno::last() == Errno::ENOTSUP {
                // only APFS supports clonefile. fall back to link
                nix::unistd::linkat(
                    None,
                    orig_c_path.as_ref(),
                    None,
                    link_c_path.as_ref(),
                    LinkatFlags::NoSymlinkFollow,
                )
                .map_err(nix_linux_error)?;
            }
        } else {
            // only APFS supports clonefile. fall back to link
            nix::unistd::linkat(
                None,
                orig_c_path.as_ref(),
                None,
                link_c_path.as_ref(),
                LinkatFlags::NoSymlinkFollow,
            )
            .map_err(nix_linux_error)?;
        }

        self.do_lookup(newparent, newname, &_ctx)
    }

    fn symlink(
        &self,
        ctx: Context,
        linkname: &CStr,
        parent: NodeId,
        name: &CStr,
        extensions: Extensions,
    ) -> io::Result<Entry> {
        let name = &name.to_string_lossy();
        let c_path = self.name_to_path(parent, name)?;

        // Safe because this doesn't modify any memory and we check the return value.
        symlinkat(linkname.as_ref(), None, c_path.as_ref()).map_err(nix_linux_error)?;

        // Set security context
        if let Some(secctx) = extensions.secctx {
            set_secctx(FileRef::Path(&c_path), secctx, true)?
        };

        let mut entry = self.do_lookup(parent, name, &ctx)?;
        let mode = libc::S_IFLNK | 0o777;
        set_xattr_stat(
            FileRef::Path(&c_path),
            Some((ctx.uid, ctx.gid)),
            Some(mode as u32),
        )?;
        entry.attr.st_uid = ctx.uid;
        entry.attr.st_gid = ctx.gid;
        entry.attr.st_mode = mode;
        Ok(entry)
    }

    fn readlink(&self, _ctx: Context, nodeid: NodeId) -> io::Result<Vec<u8>> {
        let c_path = self.nodeid_to_path(nodeid)?;

        let mut buf = vec![0; libc::PATH_MAX as usize];
        let res = unsafe {
            libc::readlink(
                c_path.as_ptr(),
                buf.as_mut_ptr() as *mut libc::c_char,
                buf.len(),
            )
        };
        if res == -1 {
            return Err(linux_error(io::Error::last_os_error()));
        }

        buf.resize(res as usize, 0);
        Ok(buf)
    }

    fn flush(
        &self,
        _ctx: Context,
        _nodeid: NodeId,
        _handle: Handle,
        _lock_owner: u64,
    ) -> io::Result<()> {
        // returning ENOSYS causes no_flush=1 to be set, skipping future calls
        // we could emulate this with dup+close to trigger nfs_vnop_close on NFS,
        // but it's usually ok to just wait for last fd to be closed (i.e. RELEASE)
        // multi-fd is rare anyway
        Err(linux_error(io::Error::from_raw_os_error(libc::ENOSYS)))
    }

    fn fsync(
        &self,
        _ctx: Context,
        nodeid: NodeId,
        _datasync: bool,
        handle: Handle,
    ) -> io::Result<()> {
        let data = self
            .handles
            .get(&handle)
            .filter(|hd| hd.nodeid == nodeid)
            .map(|v| v.clone())
            .ok_or_else(ebadf)?;

        // doesn't need exclusive fd access, but it should be a barrier point
        let fd = data.file.write().as_raw_fd();

        // use barrier fsync to preserve semantics and avoid DB corruption
        // Safe because this doesn't modify any memory and we check the return value.
        let res = unsafe { libc::fcntl(fd, libc::F_BARRIERFSYNC, 0) };

        if res == 0 {
            Ok(())
        } else {
            Err(linux_error(io::Error::last_os_error()))
        }
    }

    fn fsyncdir(
        &self,
        ctx: Context,
        nodeid: NodeId,
        datasync: bool,
        handle: Handle,
    ) -> io::Result<()> {
        self.fsync(ctx, nodeid, datasync, handle)
    }

    // access not implemented: we use default_permissions

    fn setxattr(
        &self,
        _ctx: Context,
        nodeid: NodeId,
        name: &CStr,
        value: &[u8],
        flags: u32,
    ) -> io::Result<()> {
        debug!(
            "setxattr: nodeid={} name={:?} value={:?}",
            nodeid, name, value
        );

        if !self.cfg.xattr {
            return Err(linux_error(io::Error::from_raw_os_error(libc::ENOSYS)));
        }

        if name.to_bytes() == STAT_XATTR_KEY {
            return Err(linux_error(io::Error::from_raw_os_error(libc::EACCES)));
        }

        let mut mflags: i32 = 0;
        if (flags as i32) & bindings::LINUX_XATTR_CREATE != 0 {
            mflags |= libc::XATTR_CREATE;
        }
        if (flags as i32) & bindings::LINUX_XATTR_REPLACE != 0 {
            mflags |= libc::XATTR_REPLACE;
        }

        let c_path = self.nodeid_to_path(nodeid)?;

        // Safe because this doesn't modify any memory and we check the return value.
        let res = unsafe {
            libc::setxattr(
                c_path.as_ptr(),
                name.as_ptr(),
                value.as_ptr() as *const libc::c_void,
                value.len(),
                0,
                mflags as libc::c_int,
            )
        };
        if res == 0 {
            Ok(())
        } else {
            Err(linux_error(io::Error::last_os_error()))
        }
    }

    fn getxattr(
        &self,
        _ctx: Context,
        nodeid: NodeId,
        name: &CStr,
        size: u32,
    ) -> io::Result<GetxattrReply> {
        debug!("getxattr: nodeid={} name={:?}, size={}", nodeid, name, size);

        if !self.cfg.xattr {
            return Err(linux_error(io::Error::from_raw_os_error(libc::ENOSYS)));
        }

        if name.to_bytes() == STAT_XATTR_KEY {
            return Err(linux_error(io::Error::from_raw_os_error(libc::EACCES)));
        }

        let mut buf = vec![0; size as usize];

        let c_path = self.nodeid_to_path(nodeid)?;

        // Safe because this will only modify the contents of `buf`
        let res = unsafe {
            if size == 0 {
                libc::getxattr(
                    c_path.as_ptr(),
                    name.as_ptr(),
                    std::ptr::null_mut(),
                    size as libc::size_t,
                    0,
                    0,
                )
            } else {
                libc::getxattr(
                    c_path.as_ptr(),
                    name.as_ptr(),
                    buf.as_mut_ptr() as *mut libc::c_void,
                    size as libc::size_t,
                    0,
                    0,
                )
            }
        };
        if res == -1 {
            return Err(linux_error(io::Error::last_os_error()));
        }

        if size == 0 {
            Ok(GetxattrReply::Count(res as u32))
        } else {
            buf.resize(res as usize, 0);
            Ok(GetxattrReply::Value(buf))
        }
    }

    fn listxattr(&self, _ctx: Context, nodeid: NodeId, size: u32) -> io::Result<ListxattrReply> {
        if !self.cfg.xattr {
            return Err(linux_error(io::Error::from_raw_os_error(libc::ENOSYS)));
        }

        let mut buf = vec![0; 512_usize];

        let c_path = self.nodeid_to_path(nodeid)?;

        // Safe because this will only modify the contents of `buf`.
        let res = unsafe {
            libc::listxattr(
                c_path.as_ptr(),
                buf.as_mut_ptr() as *mut libc::c_char,
                512,
                0,
            )
        };
        if res == -1 {
            return Err(linux_error(io::Error::last_os_error()));
        }

        buf.truncate(res as usize);

        if size == 0 {
            let mut clean_size = res as usize;

            for attr in buf.split(|c| *c == 0) {
                if attr.starts_with(&STAT_XATTR_KEY[..STAT_XATTR_KEY.len() - 1]) {
                    clean_size -= STAT_XATTR_KEY.len();
                }
            }

            Ok(ListxattrReply::Count(clean_size as u32))
        } else {
            let mut clean_buf = Vec::new();

            for attr in buf.split(|c| *c == 0) {
                if attr.is_empty() || attr.starts_with(&STAT_XATTR_KEY[..STAT_XATTR_KEY.len() - 1])
                {
                    continue;
                }

                clean_buf.extend_from_slice(attr);
                clean_buf.push(0);
            }

            if clean_buf.len() > size as usize {
                Err(io::Error::from_raw_os_error(LINUX_ERANGE))
            } else {
                Ok(ListxattrReply::Names(clean_buf))
            }
        }
    }

    fn removexattr(&self, _ctx: Context, nodeid: NodeId, name: &CStr) -> io::Result<()> {
        if !self.cfg.xattr {
            return Err(linux_error(io::Error::from_raw_os_error(libc::ENOSYS)));
        }

        if name.to_bytes() == STAT_XATTR_KEY {
            return Err(linux_error(io::Error::from_raw_os_error(
                bindings::LINUX_EACCES,
            )));
        }

        let c_path = self.nodeid_to_path(nodeid)?;

        // Safe because this doesn't modify any memory and we check the return value.
        let res = unsafe { libc::removexattr(c_path.as_ptr(), name.as_ptr(), 0) };

        if res == 0 {
            Ok(())
        } else {
            Err(linux_error(io::Error::last_os_error()))
        }
    }

    fn fallocate(
        &self,
        _ctx: Context,
        nodeid: NodeId,
        handle: Handle,
        _mode: u32,
        offset: u64,
        length: u64,
    ) -> io::Result<()> {
        let data = self
            .handles
            .get(&handle)
            .filter(|hd| hd.nodeid == nodeid)
            .map(|v| v.clone())
            .ok_or_else(ebadf)?;

        let fd = data.file.write().as_raw_fd();

        let mut fs = libc::fstore_t {
            fst_flags: libc::F_ALLOCATECONTIG,
            fst_posmode: libc::F_PEOFPOSMODE,
            fst_offset: 0,
            fst_length: (offset + length) as i64,
            fst_bytesalloc: 0,
        };

        let res = unsafe { libc::fcntl(fd, libc::F_PREALLOCATE, &mut fs as *mut _) };
        if res == -1 {
            fs.fst_flags = libc::F_ALLOCATEALL;
            let res = unsafe { libc::fcntl(fd, libc::F_PREALLOCATE, &mut fs as &mut _) };
            if res == -1 {
                return Err(linux_error(io::Error::last_os_error()));
            }
        }

        let res = unsafe { libc::ftruncate(fd, (offset + length) as i64) };

        if res == 0 {
            Ok(())
        } else {
            Err(linux_error(io::Error::last_os_error()))
        }
    }

    fn lseek(
        &self,
        _ctx: Context,
        nodeid: NodeId,
        handle: Handle,
        offset: u64,
        whence: u32,
    ) -> io::Result<u64> {
        let data = self
            .handles
            .get(&handle)
            .filter(|hd| hd.nodeid == nodeid)
            .map(|v| v.clone())
            .ok_or_else(ebadf)?;

        // SEEK_DATA and SEEK_HOLE have slightly different semantics
        // in Linux vs. macOS, which means we can't support them.
        let mwhence = if whence == 3 {
            // SEEK_DATA
            return Ok(offset);
        } else if whence == 4 {
            // SEEK_HOLE
            libc::SEEK_END
        } else {
            whence as i32
        };

        let fd = data.file.write().as_raw_fd();

        // Safe because this doesn't modify any memory and we check the return value.
        let res = unsafe { libc::lseek(fd, offset as bindings::off64_t, mwhence as libc::c_int) };
        if res == -1 {
            Err(linux_error(io::Error::last_os_error()))
        } else {
            Ok(res as u64)
        }
    }

    #[allow(clippy::too_many_arguments)]
    fn ioctl(
        &self,
        _ctx: Context,
        _nodeid: Self::NodeId,
        _ohandle: Self::Handle,
        _flags: u32,
        cmd: u32,
        _arg: u64,
        _in_size: u32,
        out_size: u32,
    ) -> io::Result<Vec<u8>> {
        if self.cfg.allow_rosetta_ioctl && cmd == IOCTL_ROSETTA {
            let resp = get_rosetta_data();
            if resp.len() >= out_size as usize {
                debug!("returning rosetta data: {:?}", &resp[..out_size as usize]);
                return Ok(resp[..out_size as usize].to_vec());
            }
        }

        Err(linux_error(io::Error::from_raw_os_error(libc::ENOSYS)))
    }
}
