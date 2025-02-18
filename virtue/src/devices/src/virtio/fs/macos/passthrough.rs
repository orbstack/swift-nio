// Copyright 2019 The Chromium OS Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

// Copyright 2024 Orbital Labs, LLC. All rights reserved.
// Changes to this file are confidential and proprietary, subject to terms in the LICENSE file.

use core::str;
use std::cell::{RefCell, RefMut};
use std::ffi::{CStr, CString, OsStr};
use std::fmt::Debug;
use std::fs::set_permissions;
use std::fs::File;
use std::fs::Permissions;
use std::io;
use std::marker::PhantomData;
use std::mem::{self, ManuallyDrop, MaybeUninit};
use std::num::NonZeroU64;
use std::os::fd::{AsFd, BorrowedFd, OwnedFd};
use std::os::unix::ffi::OsStrExt;
use std::os::unix::fs::PermissionsExt;
use std::os::unix::io::AsRawFd;
use std::os::unix::net::UnixDatagram;
use std::path::Path;
use std::ptr::{copy, copy_nonoverlapping, slice_from_raw_parts, NonNull};
use std::str::FromStr;
use std::sync::atomic::{AtomicBool, AtomicI64, AtomicU32, AtomicU64, Ordering};
use std::sync::{Arc, Weak};
use std::thread::JoinHandle;
use std::time::Duration;

use crate::virtio::fs::attrlist::{self, AttrlistEntry};
use crate::virtio::fs::filesystem::SecContext;
use crate::virtio::rosetta::get_rosetta_data;
use crate::virtio::{FsCallbacks, FxDashMap, NfsInfo};
use bitflags::bitflags;
use derive_more::{Display, From, Into};
use libc::{sysdir_search_path_directory_t, AT_FDCWD, MAXPATHLEN, SEEK_DATA, SEEK_HOLE};
use nix::errno::Errno;
use nix::fcntl::{self, open, AtFlags, OFlag};
use nix::sys::stat::fchmod;
use nix::sys::stat::{futimens, utimensat, Mode, UtimensatFlags};
use nix::sys::statfs::{fstatfs, statfs, Statfs};
use nix::sys::statvfs::statvfs;
use nix::sys::statvfs::Statvfs;
use nix::sys::time::TimeSpec;
use nix::sys::uio::pwrite;
use nix::unistd::{access, lseek, truncate, Whence};
use nix::unistd::{ftruncate, symlinkat};
use nix::unistd::{mkfifo, AccessFlags};
use parking_lot::RwLock;
use smol_str::SmolStr;
use utils::hypercalls::{HVC_DEVICE_VIRTIOFS_ROOT, HVC_DEVICE_VIRTIOFS_ROSETTA};
use utils::qos::{set_thread_qos, QosClass};
use utils::Mutex;

use super::super::bindings;
use super::super::filesystem::{
    Context, DirEntry, Entry, Extensions, FileSystem, FsOptions, GetxattrReply, ListxattrReply,
    OpenOptions, SetattrValid, ZeroCopyReader, ZeroCopyWriter,
};
use super::super::fuse;
use super::iopolicy;
use super::vnode_poll::VnodePoller;

// disabled because Linux doesn't FORGET everything on unmount
const DETECT_REFCOUNT_LEAKS: bool = false;

const IOC_NONE: u8 = 0;
#[allow(dead_code)]
const IOC_WRITE: u8 = 1;
const IOC_READ: u8 = 2;

const fn _ioc(dir: u8, typ: u8, nr: u8, size: u16) -> u32 {
    ((size as u32) << 16) | ((dir as u32) << 30) | ((typ as u32) << 8) | nr as u32
}

const IOCTL_ROSETTA_KEY: u32 = _ioc(IOC_READ, 0x61, 0x22, 0x45);
// macOS 13-14: nr=0x22. macOS 15: nr=0x25
// data and len are the same, so ignore nr
const IOCTL_ROSETTA_KEY_MASK: u32 = !_ioc(IOC_NONE, 0, 0xff, 0);

const IOCTL_ROSETTA_AOT_CONFIG: u32 = _ioc(IOC_READ, 0x61, 0x23, 0x80);
const IOCTL_ROSETTA_TSO_FALLBACK: u32 = _ioc(IOC_NONE, 0x61, 0x24, 0);

// filling with all 1 means: AOT on, with abstract socket, path = all 1
// this prevents it from creating ~/.cache/rosetta (and AOT connection always fails)
const ROSETTA_AOT_CONFIG: [u8; 0x80] = [0x1; 128];

const STAT_XATTR_KEY: &[u8] = b"user.orbstack.override_stat\0";

// set a 1M limit on xattr size to prevent DoS via vec allocation
// on macOS it's basically unlimited since getxattr has an offset arg for resource forks, but not on Linux
const MAX_XATTR_SIZE: usize = 1024 * 1024;

// pnpm and uv prefer clone, then fall back to hardlinks
// hard links are very slow on APFS (~170us to link+unlink) vs. clone (~65us)
const LINK_AS_CLONE_DIR_JS: &str = "node_modules";
const LINK_AS_CLONE_DIR_PY: &str = "site-packages";

// 2 hours - we invalidate via krpc
const DEFAULT_CACHE_TTL: Duration = Duration::from_secs(2 * 60 * 60);

const NSEC_PER_SEC: i64 = 1_000_000_000;

const CLONE_NOFOLLOW: u32 = 0x0001;

const FALLOC_FL_KEEP_SIZE: u32 = 0x01;
const FALLOC_FL_PUNCH_HOLE: u32 = 0x02;
const FALLOC_FL_KEEP_SIZE_AND_PUNCH_HOLE: u32 = FALLOC_FL_KEEP_SIZE | FALLOC_FL_PUNCH_HOLE;

const LINUX_SEEK_DATA: u32 = 3;
const LINUX_SEEK_HOLE: u32 = 4;

#[derive(
    Copy, Clone, Debug, Default, Ord, PartialOrd, Eq, PartialEq, Hash, From, Into, Display,
)]
pub struct NodeId(pub u64);

impl NodeId {
    pub fn option(self) -> OptionNodeId {
        NonZeroU64::new(self.0)
    }
}

#[derive(
    Copy, Clone, Debug, Default, Ord, PartialOrd, Eq, PartialEq, Hash, From, Into, Display,
)]
pub struct HandleId(pub u64);

// zero is not a valid nodeid, so use this to keep Option<NodeId> the same size
type OptionNodeId = Option<NonZeroU64>;

trait StatExt {
    fn can_open(&self) -> bool;
    fn ctime_ns(&self) -> i64;
    fn dev_ino(&self) -> DevIno;
}

impl StatExt for bindings::stat64 {
    fn can_open(&self) -> bool {
        // DO NOT open FIFOs, char/block devs: could block, will likely fail, doesn't work thru virtiofs
        !matches!(
            self.st_mode & libc::S_IFMT,
            libc::S_IFBLK | libc::S_IFCHR | libc::S_IFIFO | libc::S_IFSOCK
        )
    }

    fn ctime_ns(&self) -> i64 {
        self.st_ctime * NSEC_PER_SEC + self.st_ctime_nsec
    }

    fn dev_ino(&self) -> DevIno {
        DevIno(self.st_dev, self.st_ino)
    }
}

struct DirState {
    stream: Option<NonNull<libc::DIR>>,
    offset: i64,
    entries: Option<Vec<AttrlistEntry>>,
}

// libc::DIR is Send but not Sync
unsafe impl Send for DirState {}

// make sure libc::DIR can't be used unless DirState is locked
struct DirStreamRef<'a> {
    dir: *mut libc::DIR,
    state: PhantomData<&'a mut DirState>,
}

impl DirStreamRef<'_> {
    fn as_ptr(&self) -> *mut libc::DIR {
        self.dir
    }
}

pub(crate) struct HandleData {
    dir: Mutex<DirState>,
    file: ManuallyDrop<File>,
    node_flags: NodeFlags,
}

impl HandleData {
    fn new(
        handle: HandleId,
        file: File,
        is_readonly_reg: bool,
        poller: &Option<Arc<VnodePoller>>,
        node_flags: NodeFlags,
    ) -> io::Result<Self> {
        let hd = HandleData {
            node_flags,
            file: ManuallyDrop::new(file),
            dir: Mutex::new(DirState {
                stream: None,
                offset: 0,
                entries: None,
            }),
        };

        // technically we only have to register it when read hits EOF, but that's flaky and won't actually save time, because the common case is that files (e.g. source code) will be fully read
        if is_readonly_reg {
            if let Some(poller) = poller {
                poller.register(hd.file.as_fd(), handle)?;
            }
        }

        Ok(hd)
    }

    pub fn path(&self) -> io::Result<String> {
        get_path_by_fd(self.file.as_fd())
    }

    fn readdir_stream(&self, ds: &mut DirState) -> io::Result<DirStreamRef> {
        // dir stream is opened lazily in case client calls opendir() then releasedir() without ever reading entries, or only uses getattrlistbulk
        if let Some(s) = ds.stream {
            Ok(DirStreamRef {
                dir: s.as_ptr(),
                state: PhantomData,
            })
        } else {
            let dir = unsafe { libc::fdopendir(self.file.as_raw_fd()) };
            ds.stream = match NonNull::new(dir) {
                Some(s) => Some(s),
                None => return Err(io::Error::last_os_error()),
            };
            Ok(DirStreamRef {
                dir,
                state: PhantomData,
            })
        }
    }

    fn check_io(&self) -> io::Result<()> {
        // if in synchronous (hvc) context, force guest to retry with async worker
        if self.node_flags.contains(NodeFlags::NO_SYNC_IO) {
            iopolicy::check_blocking_io()?;
        }

        Ok(())
    }
}

impl AsFd for HandleData {
    fn as_fd(&self) -> BorrowedFd<'_> {
        self.file.as_fd()
    }
}

impl Drop for HandleData {
    fn drop(&mut self) {
        let ds = self.dir.lock().unwrap();
        if let Some(stream) = ds.stream {
            // this is a dir, and it had a stream open
            // closedir *closes* the fd passed to fdopendir (which is the fd that File holds)
            // so this invalidates the OwnedFd ownership
            unsafe { libc::closedir(stream.as_ptr()) };
        } else {
            // this is a file, or a dir with no stream open
            // manually drop File to close OwnedFd
            unsafe { ManuallyDrop::drop(&mut self.file) };
        }
    }
}

#[derive(Clone, Copy, Debug)]
enum FileRef<'a> {
    Path(&'a CStr),
    Fd(BorrowedFd<'a>),
}

trait AsFileRef {
    fn as_ref(&self) -> FileRef<'_>;
}

impl<'a> AsFileRef for FileRef<'a> {
    fn as_ref(&self) -> FileRef<'a> {
        *self
    }
}

#[derive(Clone, Debug)]
// generic over OwnedFd and HandleData
enum OwnedFileRef<F: AsFd> {
    Fd(Arc<F>),
    Path(CString),
}

impl<F: AsFd> AsFileRef for OwnedFileRef<F> {
    fn as_ref(&self) -> FileRef<'_> {
        match self {
            OwnedFileRef::Fd(fd) => FileRef::Fd(fd.as_fd()),
            OwnedFileRef::Path(path) => FileRef::Path(path),
        }
    }
}

fn get_xattr_stat(_file: FileRef) -> io::Result<Option<(u32, u32, u32)>> {
    Ok(None)
}

fn set_xattr_stat(
    _file: FileRef,
    _owner: Option<(u32, u32)>,
    _mode: Option<u32>,
) -> io::Result<()> {
    Ok(())
}

fn fstat<T: AsFd>(fd: T, host: bool) -> io::Result<bindings::stat64> {
    let mut st = nix::sys::stat::fstat(&fd)?;

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
    let mut st = nix::sys::stat::lstat(c_path)?;

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

pub fn get_path_by_fd<T: AsRawFd>(fd: T) -> io::Result<String> {
    // allocate it on stack
    debug!("get_path_by_fd: fd={}", fd.as_raw_fd());
    let mut path_buf = MaybeUninit::<[u8; MAXPATHLEN as usize]>::uninit();
    // safe: F_GETPATH is limited to MAXPATHLEN
    let ret = unsafe { libc::fcntl(fd.as_raw_fd(), libc::F_GETPATH, path_buf.as_mut_ptr()) };
    if ret == -1 {
        return Err(io::Error::last_os_error());
    }

    // safe: kernel guarantees UTF-8 and null termination
    let cstr = unsafe { CStr::from_ptr(path_buf.as_ptr() as *const _) };
    Ok(unsafe { String::from_utf8_unchecked(cstr.to_bytes().to_vec()) })
}

fn listxattr(c_path: &CStr) -> nix::Result<Vec<u8>> {
    fn inner(c_path: &CStr, mut buf: Option<&mut [u8]>) -> nix::Result<usize> {
        let ret = unsafe {
            libc::listxattr(
                c_path.as_ptr(),
                buf.as_mut()
                    .map(|b| b.as_mut_ptr() as *mut libc::c_char)
                    .unwrap_or(std::ptr::null_mut()),
                buf.map(|b| b.len()).unwrap_or(0),
                0,
            )
        };
        Errno::result(ret).map(|size| size as usize)
    }

    let mut buf = vec![0u8; 512];
    match inner(c_path, Some(&mut buf)) {
        Ok(size) => {
            buf.truncate(size);
            Ok(buf)
        }
        Err(Errno::ERANGE) => {
            // get the size we need
            let size = inner(c_path, None)?;
            let mut buf = vec![0u8; size];
            match inner(c_path, Some(&mut buf)) {
                Ok(size) => {
                    buf.truncate(size);
                    Ok(buf)
                }
                Err(e) => Err(e),
            }
        }
        Err(e) => Err(e),
    }
}

fn ebadf(nodeid: NodeId) -> io::Error {
    error!("node not found: {}", nodeid);
    Errno::EBADF.into()
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
            allow_rosetta_ioctl: false,
            nfs_info: None,
        }
    }
}

#[derive(Debug)]
struct NodeLocation {
    parent: Option<Arc<NodeData>>,
    // TODO: normalize
    name: SmolStr,
}

#[derive(Debug)]
struct NodeData {
    // TODO: can we get away without this?
    nodeid: NodeId,

    flags: NodeFlags, // for flags propagated to children

    loc: RwLock<NodeLocation>,

    // TODO: update this, or don't keep it here
    nlink: u16, // for getattrlistbulk buffer size

    // state
    refcount: AtomicU32,
    // for CTO consistency: clear cache on open if ctime has changed
    // must only be updated on open
    last_open_ctime: AtomicI64,

    is_mountpoint_parent: bool,
    is_mountpoint: bool,
}

thread_local! {
    static PATH_BUFFERS: [RefCell<Vec<u8>>; 2] = const { [const { RefCell::new(Vec::new()) }; 2] };
}

impl NodeData {
    fn with_path<T>(&self, f: impl FnOnce(&CStr) -> T) -> T {
        self.with_raw_path(f, None)
    }

    fn with_subpath<T>(&self, name: &str, f: impl FnOnce(&CStr) -> T) -> T {
        if name == ".." || name.contains('/') {
            panic!("invalid subpath: {}", name);
        }

        self.with_raw_path(f, Some(name))
    }

    fn with_raw_path<T>(&self, f: impl FnOnce(&CStr) -> T, subpath: Option<&str>) -> T {
        debug!(
            "with_raw_path is_mountpoint_parent={} is_mountpoint={} subpath={:?}",
            self.is_mountpoint_parent, self.is_mountpoint, subpath
        );
        if self.is_mountpoint
        // we'd need a reference (here) to nfs_info to check this
        // || (self.is_mountpoint_parent && subpath == Some("OrbStack"))
        // instead, check this in begin_lookup
        {
            f(&CString::from_str("/var/empty").unwrap())
        } else {
            PATH_BUFFERS.with(|v| {
                let mut buf = v
                    .iter()
                    .find_map(|v| v.try_borrow_mut().ok())
                    .expect("out of path buffers");
                buf.clear();

                self._build_path(&mut buf);

                if let Some(subpath_name) = subpath {
                    buf.push(b'/');
                    buf.extend_from_slice(subpath_name.as_bytes());
                }

                // Push a null byte as CStrings are null-terminated. Since we're building the string from
                // a pointer to the slice, we'll keep going until we find a null byte, which otherwise
                // might not be the correct place if the buffer has been used before.
                buf.push(b'\0');

                let c_path = unsafe { CStr::from_bytes_with_nul_unchecked(&buf) };
                debug!("built NODE path nodeid={} path={:?}", self.nodeid, c_path);
                f(c_path)
            })
        }
    }

    fn owned_ref(&self) -> OwnedFileRef<HandleData> {
        self.with_path(|path| OwnedFileRef::Path(path.to_owned()))
    }

    fn _build_path(&self, buf: &mut Vec<u8>) {
        let loc = self.loc.read();
        match loc.parent {
            Some(ref parent) => {
                buf.insert_slice(0, loc.name.as_bytes());
                buf.insert(0, b'/');
                parent._build_path(buf);
            }
            None => {
                buf.insert_slice(0, loc.name.as_bytes());
            }
        }
    }

    // this is "inc not zero"
    fn inc_ref(&self) -> Result<(), ()> {
        loop {
            let refcount = self.refcount.load(Ordering::Relaxed);
            if refcount == 0 {
                return Err(());
            }
            if self
                .refcount
                .compare_exchange(refcount, refcount + 1, Ordering::Relaxed, Ordering::Relaxed)
                .is_ok()
            {
                return Ok(());
            }
        }
    }
}

bitflags! {
    #[derive(Debug, Copy, Clone, Hash, Eq, PartialEq, Ord, PartialOrd)]
    pub struct NodeFlags: u16 {
        // inherited
        const LINK_AS_CLONE = 1 << 0;
        const INHERITED_FLAGS = Self::LINK_AS_CLONE.bits();

        // per-node
        const NO_SYNC_IO = 1 << 1;
    }
}

impl NodeData {
    fn check_io(&self) -> io::Result<()> {
        // if in synchronous (hvc) context, force guest to retry with async worker
        if self.flags.contains(NodeFlags::NO_SYNC_IO) {
            iopolicy::check_blocking_io()?;
        }

        Ok(())
    }
}

impl Drop for NodeData {
    fn drop(&mut self) {
        // let loc = self.loc.get_mut();
        // if let Some(ref parent) = loc.parent {
        //     // TODO: remove clone
        //     if let dashmap::mapref::entry::Entry::Occupied(mut e) =
        //         parent.children.entry(loc.name.clone())
        //     {
        //         if e.get().upgrade().is_none() {
        //             debug!("drop stale entry: {}", loc.name);
        //             e.remove();
        //         }
        //     }
        // }
    }
}

#[derive(Copy, Clone, Debug, Default, Ord, PartialOrd, Eq, PartialEq, Hash)]
#[repr(packed)]
struct DevIno(pub i32, pub u64);

impl DevIno {
    fn hash(&self) -> u64 {
        // TODO: better hash
        self.1 ^ (((self.0 as u64) << 32).rotate_left(16))
    }
}

#[derive(Debug, Copy, Clone)]
struct DevInfo {
    // local or remote (e.g. nfs)
    local: bool,
    // fsid for fsgetpath
    fsid: libc::fsid_t,
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
    // - duplicate nodeids for a single DevIno will be fixed by finish_lookup
    nodeids: FxDashMap<NodeId, Arc<NodeData>>,
    next_nodeid: AtomicU64,
    // maps nodes to children - keyed by parent node id + child name
    node_children: FxDashMap<(NodeId, SmolStr), Weak<NodeData>>,

    handles: Arc<FxDashMap<HandleId, Arc<HandleData>>>,
    next_handle: AtomicU64,

    // volfs supported?
    dev_info: FxDashMap<i32, DevInfo>,

    // Whether writeback caching is enabled for this directory. This will only be true when
    // `cfg.writeback` is true and `init` was called with `FsOptions::WRITEBACK_CACHE`.
    writeback: AtomicBool,
    cfg: Config,

    poller: Option<Arc<VnodePoller>>,
    poller_thread: Option<JoinHandle<()>>,
}

impl PassthroughFs {
    pub fn new(cfg: Config, callbacks: Option<Arc<dyn FsCallbacks>>) -> io::Result<PassthroughFs> {
        // init with root nodeid
        let st = nix::sys::stat::stat(Path::new(&cfg.root_dir))?;
        let nodeids = FxDashMap::default();
        nodeids.insert(
            NodeId(fuse::ROOT_ID),
            Arc::new(NodeData {
                nodeid: NodeId(fuse::ROOT_ID),

                loc: RwLock::new(NodeLocation {
                    parent: None,
                    name: SmolStr::new(&cfg.root_dir),
                }),

                // refcount 2 so it can never be dropped
                refcount: AtomicU32::new(2),
                last_open_ctime: AtomicI64::new(st.ctime_ns()),
                flags: NodeFlags::empty(),
                nlink: st.st_nlink,
                is_mountpoint_parent: false,
                is_mountpoint: false,
            }),
        );

        let dev_info = FxDashMap::default();

        let handles = Arc::new(FxDashMap::default());
        let poller = match callbacks {
            Some(callbacks) => Some(Arc::new(VnodePoller::new(callbacks, handles.clone())?)),
            None => None,
        };

        let children_map = FxDashMap::default();

        let mut fs = PassthroughFs {
            nodeids,
            next_nodeid: AtomicU64::new(fuse::ROOT_ID + 1),

            handles,
            next_handle: AtomicU64::new(1),

            dev_info,

            writeback: AtomicBool::new(false),
            cfg,

            // poller is useless if we have no invalidation callbacks
            poller,
            poller_thread: None,

            node_children: children_map,
        };

        if let Some(ref poller) = fs.poller {
            let poller_clone = poller.clone();
            let handle = std::thread::Builder::new()
                .name(format!("fs{} poller", fs.hvc_id().unwrap()))
                .spawn(move || {
                    // maintenance tasks only: cache invalidation
                    set_thread_qos(QosClass::Background, None).unwrap();
                    poller_clone.main_loop().unwrap();
                })?;
            fs.poller_thread = Some(handle);
        }

        Ok(fs)
    }

    fn get_node(&self, nodeid: NodeId) -> io::Result<Arc<NodeData>> {
        // race OK: primary key lookup only
        let node = self
            .nodeids
            .get(&nodeid)
            .ok_or_else(|| ebadf(nodeid))?
            .clone();
        node.check_io()?;
        Ok(node)
    }

    fn get_handle(&self, _nodeid: NodeId, handle: HandleId) -> io::Result<Arc<HandleData>> {
        let hd = self.handles.get(&handle).ok_or(Errno::EBADF)?.clone();
        hd.check_io()?;
        Ok(hd)
    }

    fn get_dev_info<F, R>(&self, dev: i32, file_ref_fn: F) -> io::Result<DevInfo>
    where
        F: FnOnce() -> io::Result<R>,
        R: AsFileRef + Debug,
    {
        // TODO: what if the mountpoint disappears, and st_dev gets reused?
        if let Some(info) = self.dev_info.get(&dev) {
            return Ok(*info);
        }

        // not in cache: check it
        // statfs doesn't trigger TCC (only open does)
        let file_ref = file_ref_fn()?;
        let stf = match file_ref.as_ref() {
            FileRef::Path(c_path) => statfs(c_path),
            FileRef::Fd(fd) => fstatfs(fd),
        }?;
        // transmute type (repr(transparent))
        let stf = unsafe { mem::transmute::<Statfs, libc::statfs>(stf) };
        let dev_info = DevInfo {
            local: (stf.f_flags & libc::MNT_LOCAL as u32) != 0,
            fsid: stf.f_fsid,
        };

        debug!(?dev, ?file_ref, ?dev_info, "dev_info");
        // race OK: will be the same result
        self.dev_info.insert(dev, dev_info);
        Ok(dev_info)
    }

    fn begin_lookup(
        &self,
        parent: NodeId,
        name: &str,
    ) -> io::Result<(CString, Arc<NodeData>, libc::stat)> {
        let node = self.get_node(parent)?;
        let is_mp_path: bool = self
            .cfg
            .nfs_info
            .as_ref()
            .map(|nfs_info| node.is_mountpoint_parent && nfs_info.dir_name == name)
            .unwrap_or(false);
        let c_path = if is_mp_path {
            CString::from_str("/var/empty").unwrap()
        } else {
            self.get_node(parent)?
                .with_subpath(name, |c_path| c_path.to_owned())
        };

        debug!(?c_path, "begin_lookup");

        let st = lstat(&c_path, false)?;
        Ok((c_path, node, st))
    }

    fn do_lookup(&self, parent: NodeId, name: &str) -> io::Result<Entry> {
        let (c_path, parent, st) = self.begin_lookup(parent, name)?;
        let (entry, _) = self.finish_lookup(&parent, name, st, FileRef::Path(&c_path))?;
        Ok(entry)
    }

    fn filter_stat(&self, st: &mut bindings::stat64, nodeid: NodeId) {
        // root generation must be zero
        // for other inodes, we ignore st_gen because getattrlistbulk doesn't support it, so returning it here would break revalidate
        st.st_gen = 0;

        // don't report st_nlink on directories
        // getattrlistbulk doesn't support st_nlink on dirs: ATTR_FILE_LINKCOUNT is only for files; ATTR_DIR_ENTRYCOUNT is all children, not only subdirs; ATTR_DIR_LINKCOUNT is # of dir hardlinks -- actual hardlinks, because HFS+ supports dir hardlinks
        // dirs normally have weird st_nlink behavior because st_nlink = 2 (".", "..") + # of subdirs, not # of children
        // just report 1 ("unknown") to avoid inconsistency between stat and readdirplus. this is acceptable behavior; btrfs always does it
        if st.st_mode & libc::S_IFDIR != 0 {
            st.st_nlink = 1;
        }

        // st_ino must not be nodeid (as nodeid is per-dentry), and must not collide across host filesystems
        //st.st_ino = st.dev_ino().hash();
        st.st_ino = nodeid.0;
    }

    fn get_or_insert_node(
        &self,
        parent: &Arc<NodeData>,
        name: &str,
        st: &bindings::stat64,
        file_ref: FileRef,
    ) -> io::Result<(NodeId, NodeFlags)> {
        let smol_name = SmolStr::from(name);
        if let Some(e) = self.node_children.get(&(parent.nodeid, smol_name.clone())) {
            if let Some(node) = e.upgrade() {
                if node.inc_ref().is_ok() {
                    debug!(?name, nodeid = ?node.nodeid, "existing node");
                    return Ok((node.nodeid, node.flags));
                }
            }
        }

        // this (parent, name) is new
        // create a new nodeid and return it
        let mut new_nodeid = self.next_nodeid.fetch_add(1, Ordering::Relaxed).into();
        debug!(?name, ?new_nodeid, "new node");

        let dev_info = self.get_dev_info(st.st_dev, || Ok(file_ref))?;

        let (is_mountpoint_parent, is_mountpoint) =
            if let Some(nfs_info) = self.cfg.nfs_info.as_ref() {
                (
                    nfs_info.parent_dir_name == name,
                    parent.is_mountpoint_parent && nfs_info.dir_name == name,
                )
            } else {
                (false, false)
            };

        let mut node = NodeData {
            nodeid: new_nodeid,

            loc: RwLock::new(NodeLocation {
                parent: Some(parent.clone()),
                name: SmolStr::from(name),
            }),

            refcount: AtomicU32::new(1),
            last_open_ctime: AtomicI64::new(st.ctime_ns()),
            flags: parent.flags & NodeFlags::INHERITED_FLAGS,
            nlink: st.st_nlink,

            is_mountpoint_parent,
            is_mountpoint,
        };

        // flag to use clonefile instead of link, for package managers
        if name == LINK_AS_CLONE_DIR_JS || name == LINK_AS_CLONE_DIR_PY {
            node.flags |= NodeFlags::LINK_AS_CLONE;
        }

        // no sync IO on remote/network file systems
        if !dev_info.local {
            node.flags |= NodeFlags::NO_SYNC_IO;
        }

        let node_flags = node.flags;
        let node = Arc::new(node);
        let weak = Arc::downgrade(&node);
        self.nodeids.insert(new_nodeid, node);
        match self.node_children.entry((parent.nodeid, smol_name)) {
            dashmap::mapref::entry::Entry::Vacant(e) => {
                e.insert(weak);
            }
            dashmap::mapref::entry::Entry::Occupied(mut e) => {
                debug!(?name, "raced with another thread");
                // we raced with another thread, which added a nodeid for this (parent, name)
                // does the old nodeid still exist?
                if let Some(old_node) = e.get().upgrade() {
                    // yes: try to acquire it
                    if old_node.inc_ref().is_ok() {
                        // success: remove the new one we added
                        self.nodeids.remove(&new_nodeid);
                        // and change new nodeid to the old one
                        new_nodeid = old_node.nodeid;
                    } else {
                        // stale: refcount = 0
                        e.insert(weak);
                    }
                } else {
                    // stale: freed
                    e.insert(weak);
                }
            }
        }

        Ok((new_nodeid, node_flags))
    }

    fn finish_lookup(
        &self,
        parent: &Arc<NodeData>,
        name: &str,
        mut st: bindings::stat64,
        file_ref: FileRef,
    ) -> io::Result<(Entry, NodeFlags)> {
        let (nodeid, node_flags) = self.get_or_insert_node(parent, name, &st, file_ref)?;

        debug!(
            "finish_lookup: dev={} ino={} ref={:?} -> nodeid={}",
            st.st_dev, st.st_ino, file_ref, nodeid
        );

        self.filter_stat(&mut st, nodeid);

        Ok((
            Entry {
                nodeid,
                generation: 0,
                attr: st,
                attr_timeout: self.cfg.attr_timeout,
                entry_timeout: self.cfg.entry_timeout,
            },
            node_flags,
        ))
    }

    fn do_forget(&self, nodeid: NodeId, count: u64) {
        debug!("do_forget: nodeid={} count={}", nodeid, count);
        if let dashmap::mapref::entry::Entry::Occupied(e) = self.nodeids.entry(nodeid) {
            // no check_io: closing a read-only fd is OK and won't trigger flush

            // decrement the refcount
            if e.get().refcount.fetch_sub(count as u32, Ordering::Relaxed) == count as u32 {
                // count - count = 0
                // this nodeid is no longer in use

                e.remove();
            }
        }
    }

    fn do_readdir<F>(
        &self,
        _ctx: &Context,
        nodeid: NodeId,
        handle: HandleId,
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

        let node = self.get_node(nodeid)?;
        let data = self.get_handle(nodeid, handle)?;

        // dir stream is opened lazily in case client calls opendir() then releasedir() without ever reading entries
        let mut ds = data.dir.lock().unwrap();
        let stream = data.readdir_stream(&mut ds)?;

        if (offset as i64) != ds.offset {
            unsafe { libc::seekdir(stream.as_ptr(), offset as i64) };
        }

        loop {
            ds.offset = unsafe { libc::telldir(stream.as_ptr()) };

            let dentry = unsafe { libc::readdir(stream.as_ptr()) };
            if dentry.is_null() {
                break;
            }

            // include "." and ".." - FUSE expects them
            let name = unsafe {
                CStr::from_bytes_until_nul(&*slice_from_raw_parts(
                    (*dentry).d_name.as_ptr() as *const u8,
                    (*dentry).d_name.len(),
                ))
                .unwrap()
                .to_bytes()
            };

            let smol_name = SmolStr::from(std::str::from_utf8(name).unwrap());
            let mut ino = unsafe { (*dentry).d_ino };
            if let Some(nfs_info) = self.cfg.nfs_info.as_ref() {
                // replace nfs mountpoint ino with /var/empty - that's what lookup returns
                if ino == nfs_info.dir_inode {
                    ino = nfs_info.empty_dir_inode;
                }
            }

            // TODO: optimize
            let dt_ino: u64 = if let Some(node) = self
                .node_children
                .get(&(node.nodeid, smol_name))
                .and_then(|n| n.upgrade())
            {
                node.nodeid.0
            } else {
                // if we can't find it, just use st_ino
                ino
            };

            let res = unsafe {
                add_entry(DirEntry {
                    ino: dt_ino,
                    offset: (ds.offset + 1) as u64,
                    type_: u32::from((*dentry).d_type),
                    name,
                })
            };

            match res {
                Ok(size) => {
                    if size == 0 {
                        unsafe { libc::seekdir(stream.as_ptr(), ds.offset) };
                        break;
                    }
                }
                Err(e) => {
                    error!(
                        "failed to add entry {}: {:?}",
                        std::str::from_utf8(name).unwrap(),
                        e
                    );
                    continue;
                }
            }
        }

        Ok(())
    }

    fn convert_open_flags(&self, lflags: i32) -> OFlag {
        let mut flags = OFlag::from_bits_retain(lflags & libc::O_ACCMODE);

        if (lflags & bindings::LINUX_O_NONBLOCK) != 0 {
            flags |= OFlag::O_NONBLOCK;
        }
        if (lflags & bindings::LINUX_O_APPEND) != 0 {
            flags |= OFlag::O_APPEND;
        }
        if (lflags & bindings::LINUX_O_CREAT) != 0 {
            flags |= OFlag::O_CREAT;
        }
        if (lflags & bindings::LINUX_O_TRUNC) != 0 {
            flags |= OFlag::O_TRUNC;
        }
        if (lflags & bindings::LINUX_O_EXCL) != 0 {
            flags |= OFlag::O_EXCL;
        }
        if (lflags & bindings::LINUX_O_NOFOLLOW) != 0 {
            flags |= OFlag::O_NOFOLLOW;
        }
        if (lflags & bindings::LINUX_O_CLOEXEC) != 0 {
            flags |= OFlag::O_CLOEXEC;
        }

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

        // always add O_NOFOLLOW to prevent escape via symlink race
        // Linux will never try to open a symlink
        flags |= OFlag::O_CLOEXEC | OFlag::O_NOFOLLOW;
        flags.remove(OFlag::O_EXLOCK);

        flags
    }

    fn finish_open(
        &self,
        file: File,
        flags: OFlag,
        nodeid: NodeId,
        node_flags: NodeFlags,
        st: bindings::stat64,
    ) -> io::Result<(HandleId, OpenOptions)> {
        let handle = self.next_handle.fetch_add(1, Ordering::Relaxed).into();
        // only register regular files that are read-only
        let is_readonly_reg =
            !flags.intersects(OFlag::O_DIRECTORY | OFlag::O_WRONLY | OFlag::O_RDWR);
        let data = HandleData::new(handle, file, is_readonly_reg, &self.poller, node_flags)?;

        debug!("finish_open: nodeid={} -> handle={:?}", nodeid, handle);
        self.handles.insert(handle, Arc::new(data));

        let mut opts = OpenOptions::empty();
        match self.cfg.cache_policy {
            // We only set the direct I/O option on files.
            CachePolicy::Never => {
                opts.set(OpenOptions::DIRECT_IO, !flags.contains(OFlag::O_DIRECTORY))
            }
            CachePolicy::Auto => {
                // TODO: how come readdirplus never gets cached?
                if flags.contains(OFlag::O_DIRECTORY) {
                    opts |= OpenOptions::CACHE_DIR;
                }

                // provide CTO consistency
                // check ctime, and invalidate dir/file cache if ctime has changed
                // race OK: we'll just be missing cache for a file
                // fstat() is the slow part, so should be faster to release and re-acquire map ref here
                if let Some(node) = self.nodeids.get(&nodeid) {
                    // no check_io: no IO here
                    let ctime = st.ctime_ns();
                    if node.last_open_ctime.swap(ctime, Ordering::Relaxed) == ctime {
                        // this works for dirs because readdir data is also in inode page cache
                        opts |= OpenOptions::KEEP_CACHE;
                    }
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

        Ok((handle, opts))
    }

    fn do_open(
        &self,
        ctx: &Context,
        nodeid: NodeId,
        flags: u32,
    ) -> io::Result<(Option<HandleId>, OpenOptions)> {
        let flags = self.convert_open_flags(flags as i32);
        let node = self.get_node(nodeid)?;
        let file = node.with_path(|c_path| {
            nix::fcntl::open(c_path, flags, Mode::empty()).map(|f| unsafe { File::from(f) })
        })?;
        // early stat to avoid broken handle state if it fails
        let st = fstat(&file, false)?;

        // Linux normally won't open fifos/devs, but guest might maliciously trick us into doing it
        // reading from one will cause us to hang on read, preventing VM stop
        if !st.can_open() {
            return Err(Errno::EOPNOTSUPP.into());
        }

        let (handle, opts) = self.finish_open(file, flags, nodeid, node.flags, st)?;
        Ok((Some(handle), opts))
    }

    fn do_release(&self, _ctx: &Context, _nodeid: NodeId, handle: HandleId) -> io::Result<()> {
        if let dashmap::mapref::entry::Entry::Occupied(e) = self.handles.entry(handle) {
            // check_io needed: on NFS, close() will flush if fd was written to
            e.get().check_io()?;

            // We don't need to close the file here because that will happen automatically when
            // the last `Arc` is dropped.
            e.remove();
            return Ok(());
        }

        Err(Errno::EBADF.into())
    }

    fn do_getattr(
        &self,
        file_ref: FileRef,
        nodeid: NodeId,
    ) -> io::Result<(bindings::stat64, Duration)> {
        let st = match file_ref {
            FileRef::Path(c_path) => lstat(c_path, false)?,
            FileRef::Fd(fd) => fstat(fd, false)?,
        };

        self.finish_getattr(st, nodeid)
    }

    fn finish_getattr(
        &self,
        mut st: bindings::stat64,
        nodeid: NodeId,
    ) -> io::Result<(bindings::stat64, Duration)> {
        self.filter_stat(&mut st, nodeid);
        st.st_ino = nodeid.0;
        Ok((st, self.cfg.attr_timeout))
    }

    fn do_setattr(
        &self,
        ctx: Context,
        nodeid: NodeId,
        attr: bindings::stat64,
        handle: Option<HandleId>,
        valid: SetattrValid,
    ) -> io::Result<(bindings::stat64, Duration)> {
        let file_ref = self.get_file_ref(nodeid, handle)?;

        if valid.contains(SetattrValid::MODE) {
            // TODO: store sticky bit in xattr. don't allow suid/sgid
            match file_ref.as_ref() {
                FileRef::Fd(fd) => {
                    fchmod(&fd, Mode::from_bits_truncate(attr.st_mode))?;
                }
                FileRef::Path(path) => {
                    set_permissions(
                        Path::new(&*path.to_string_lossy()),
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
                u32::MAX
            };
            let gid = if valid.contains(SetattrValid::GID) {
                attr.st_gid
            } else {
                // Cannot use -1 here because these are unsigned values.
                u32::MAX
            };

            set_xattr_stat(file_ref.as_ref(), Some((uid, gid)), None)?;
        }

        if valid.contains(SetattrValid::SIZE) {
            debug!(
                "ftruncate: nodeid={} handle={:?} size={}",
                nodeid, handle, attr.st_size
            );

            match file_ref.as_ref() {
                FileRef::Fd(fd) => ftruncate(fd, attr.st_size),
                FileRef::Path(path) => truncate(path, attr.st_size),
            }?;
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
            match file_ref.as_ref() {
                FileRef::Fd(fd) => futimens(&fd, &atime, &mtime),
                FileRef::Path(path) => utimensat(
                    fcntl::AT_FDCWD,
                    path,
                    &atime,
                    &mtime,
                    UtimensatFlags::NoFollowSymlink,
                ),
            }?;
        }

        self.do_getattr(file_ref.as_ref(), nodeid)
    }

    fn do_unlink(
        &self,
        _ctx: Context,
        parent: NodeId,
        name: &CStr,
        flags: libc::c_int,
    ) -> io::Result<()> {
        // Safe because this doesn't modify any memory and we check the return value.
        let res = self
            .get_node(parent)?
            .with_subpath(&name.to_string_lossy(), |c_path| unsafe {
                libc::unlinkat(AT_FDCWD, c_path.as_ptr(), flags)
            });

        if res == 0 {
            Ok(())
        } else {
            Err(io::Error::last_os_error())
        }
    }

    fn get_file_ref(
        &self,
        nodeid: NodeId,
        handle: Option<HandleId>,
    ) -> io::Result<OwnedFileRef<HandleData>> {
        if let Some(handle) = handle {
            let hd = self.get_handle(nodeid, handle)?;
            Ok(OwnedFileRef::Fd(hd))
        } else {
            let node = self.get_node(nodeid)?;
            Ok(node.owned_ref())
        }
    }
}

fn set_secctx(file: FileRef, secctx: &SecContext, symlink: bool) -> io::Result<()> {
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

impl Drop for PassthroughFs {
    fn drop(&mut self) {
        if let Some(ref poller) = self.poller {
            let _ = poller.stop();
            if let Some(ref poller_thread) = self.poller_thread {
                poller_thread.thread().unpark();
            }
        }
    }
}

impl FileSystem for PassthroughFs {
    type NodeId = NodeId;
    type Handle = HandleId;

    fn hvc_id(&self) -> Option<usize> {
        Some(if self.cfg.root_dir == "/" {
            HVC_DEVICE_VIRTIOFS_ROOT
        } else {
            HVC_DEVICE_VIRTIOFS_ROSETTA
        })
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
        if DETECT_REFCOUNT_LEAKS {
            for node in self.nodeids.iter() {
                if (node.key().0) == fuse::ROOT_ID {
                    continue;
                }

                warn!(
                    "leaked node: nodeid={} refcount={}",
                    node.key(),
                    node.refcount.load(Ordering::Relaxed)
                );
            }
        }

        self.handles.clear();
        self.nodeids.clear();

        // TODO: handle remount
        if let Some(ref poller) = self.poller {
            poller.stop().unwrap();
        }
    }

    fn statfs(&self, _ctx: Context, nodeid: NodeId) -> io::Result<Statvfs> {
        Ok(self.get_node(nodeid)?.with_path(statvfs)?)
    }

    fn lookup(&self, ctx: Context, parent: NodeId, name: &CStr) -> io::Result<Entry> {
        debug!("lookup: {:?}", name);
        self.do_lookup(parent, &name.to_string_lossy())
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
        ctx: Context,
        nodeid: NodeId,
        flags: u32,
    ) -> io::Result<(Option<HandleId>, OpenOptions)> {
        self.do_open(&ctx, nodeid, flags | libc::O_DIRECTORY as u32)
    }

    fn releasedir(
        &self,
        ctx: Context,
        nodeid: NodeId,
        _flags: u32,
        handle: HandleId,
    ) -> io::Result<()> {
        self.do_release(&ctx, nodeid, handle)
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
        self.get_node(parent)?.with_subpath(name, |c_path| {
            // Safe because this doesn't modify any memory and we check the return value.
            if unsafe { libc::mkdir(c_path.as_ptr(), (mode & !umask) as u16) } == 0 {
                // Set security context
                if let Some(secctx) = &extensions.secctx {
                    set_secctx(FileRef::Path(c_path), secctx, false)?
                };

                set_xattr_stat(
                    FileRef::Path(c_path),
                    Some((ctx.uid, ctx.gid)),
                    Some(mode & !umask),
                )
            } else {
                Err(io::Error::last_os_error())
            }
        })?;
        self.do_lookup(parent, name)
    }

    fn rmdir(&self, ctx: Context, parent: NodeId, name: &CStr) -> io::Result<()> {
        self.do_unlink(ctx, parent, name, libc::AT_REMOVEDIR)
    }

    fn readdir<F>(
        &self,
        ctx: Context,
        nodeid: NodeId,
        handle: HandleId,
        size: u32,
        offset: u64,
        add_entry: F,
    ) -> io::Result<()>
    where
        F: FnMut(DirEntry) -> io::Result<usize>,
    {
        self.do_readdir(&ctx, nodeid, handle, size, offset, add_entry)
    }

    fn readdirplus<F>(
        &self,
        ctx: Context,
        nodeid: NodeId,
        handle: HandleId,
        size: u32,
        mut offset: u64,
        mut add_entry: F,
    ) -> io::Result<()>
    where
        F: FnMut(DirEntry, Entry) -> io::Result<usize>,
    {
        debug!(
            "readdirplus: nodeid={}, handle={}, size={}, offset={}",
            nodeid, handle, size, offset
        );

        let node = self.get_node(nodeid)?;
        let data = self.get_handle(nodeid, handle)?;

        if size == 0 {
            return Ok(());
        }

        // emit "." and ".." first. according to FUSE docs, kernel does this if we don't, but that's not true (and it breaks some apps)
        // skip dirstream lock if only reading . and ..
        if offset == 0 {
            match add_entry(
                DirEntry {
                    // TODO: this is very wrong
                    ino: 498207589,
                    offset: 1,
                    type_: libc::DT_DIR as u32,
                    name: b".",
                },
                Entry::default(),
            ) {
                Ok(0) => return Ok(()),
                Ok(_) => {}
                Err(e) => error!("failed to add entry: {:?}", e),
            }

            offset = 1;
        }
        if offset == 1 {
            match add_entry(
                DirEntry {
                    // TODO: propagate parent nodeid info?
                    ino: 12321389314,
                    offset: 2,
                    type_: libc::DT_DIR as u32,
                    name: b"..",
                },
                Entry::default(),
            ) {
                Ok(0) => return Ok(()),
                Ok(_) => {}
                Err(e) => error!("failed to add entry: {:?}", e),
            }

            offset = 2;
        }

        let mut ds = data.dir.lock().unwrap();
        // rewind and re-read dir if necessary (other offsets are vec-based)
        let ds_offset = offset - 2;
        if ds_offset == 0 && ds.offset != 0 {
            lseek(&*data.file, 0, Whence::SeekSet)?;
            ds.offset = 0;
            ds.entries = None;
        }

        // read entries if not already done
        let entries = if let Some(entries) = ds.entries.as_ref() {
            entries
        } else {
            // reserve # entries = nlink - 2 ("." and "..")
            let capacity = node.nlink.saturating_sub(2);

            // for NFS loop prevention to work, use legacy impl on home dir
            // getattrlistbulk on home can sometimes stat on mount and cause deadlock
            let entries = if node.is_mountpoint_parent {
                attrlist::list_dir_legacy(
                    data.readdir_stream(&mut ds)?.as_ptr(),
                    capacity as usize,
                    |name| {
                        let (_, _, st) = self.begin_lookup(nodeid, name)?;
                        Ok(st)
                    },
                )?
            } else {
                attrlist::list_dir(data.file.as_fd(), capacity as usize)?
            };

            ds.offset = entries.len() as i64;
            ds.entries = Some(entries);
            ds.entries.as_ref().unwrap()
        };

        if ds_offset >= entries.len() as u64 {
            return Ok(());
        }

        for (i, entry) in entries[ds_offset as usize..].iter().enumerate() {
            let st = if let Some(ref st) = entry.st {
                st
            } else {
                // on error, fall back to normal readdir response for this entry
                // linux can get the real error on lookup
                // unfortunately, on error, getattrlistbulk only returns ATTR_CMN_NAME + ATTR_CMN_ERROR. no inode or type like readdir
                let dir_entry = DirEntry {
                    // just can't be 0
                    ino: offset + 1 + (i as u64),
                    offset: offset + 1 + (i as u64),
                    type_: libc::DT_UNKNOWN as u32,
                    name: entry.name.as_bytes(),
                };
                // nodeid=0 means skip readdirplus lookup entry
                if let Ok(0) = add_entry(dir_entry, Entry::default()) {
                    break;
                }

                continue;
            };

            // we trust kernel to return valid utf-8 names
            debug!(
                "list_dir: name={} mountpoint={} dev={} ino={} offset={}",
                &entry.name,
                &entry.is_mountpoint,
                st.st_dev,
                st.st_ino,
                offset + 1 + (i as u64)
            );

            let smol_name = SmolStr::from(&entry.name);
            let result = if self.node_children.get(&(node.nodeid, smol_name)).is_some() {
                // don't return attrs for files with existing nodeids (i.e. inode struct on the linux side)
                // this prevents a race (cargo build [st_size], rm -rf [st_nlink? not sure]) where Linux is writing to a file that's growing in size, and something else calls readdirplus on its parent dir, causing FUSE to update the existing inode's attributes with a stale size, causing the readable portion of the file to be truncated when the next rustc process tries to read from the previous compilation output
                // it's OK for perf, because readdirplus covers two cases: (1) providing attrs to avoid lookup for a newly-seen file, and (2) updating invalidated attrs (>2h timeout, or set in inval_mask) on existing files
                // we only really care about the former case. for the latter case, inval_mask is rarely set in perf-critical contexts, and readdirplus is unlikely to help with the >2h timeout (would the first revalidation call be preceded by readdirplus?)
                // if the 2h-timeout case turns out to be important, we can record last-attr-update timestamp in NodeData and return attrs if expired. races won't happen 2 hours apart

                Ok(Entry::default())
            } else if entry.is_mountpoint {
                // mountpoints must be looked up again. getattrlistbulk returns the orig fs mountpoint dir
                self.do_lookup(nodeid, &entry.name)
            } else {
                // only do path lookup once
                // TODO: avoid constantly rebuilding paths
                node.with_subpath(&entry.name, |path| {
                    self.finish_lookup(&node, &entry.name, *st, FileRef::Path(&path))
                        .map(|(entry, _)| entry)
                })
            };

            // if lookup failed, return no entry, so linux will get the error on lookup
            let lookup_entry = result.unwrap_or(Entry::default());
            let new_nodeid = lookup_entry.nodeid;
            let dir_entry = DirEntry {
                ino: st.dev_ino().hash(),
                offset: offset + 1 + (i as u64),
                // same values on macOS and Linux
                type_: match st.st_mode & libc::S_IFMT {
                    libc::S_IFREG => libc::DT_REG,
                    libc::S_IFDIR => libc::DT_DIR,
                    libc::S_IFLNK => libc::DT_LNK,
                    libc::S_IFCHR => libc::DT_CHR,
                    libc::S_IFBLK => libc::DT_BLK,
                    libc::S_IFIFO => libc::DT_FIFO,
                    libc::S_IFSOCK => libc::DT_SOCK,
                    _ => libc::DT_UNKNOWN,
                } as u32,
                name: entry.name.as_bytes(),
            };

            debug!(?dir_entry, ?lookup_entry, "readdirplus entry");

            match add_entry(dir_entry, lookup_entry) {
                Ok(0) => {
                    // out of space
                    // forget this entry (only if we looked up a potentially *new* nodeid for it)
                    if new_nodeid != NodeId(0) {
                        self.do_forget(new_nodeid, 1);
                    }
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
        ctx: Context,
        nodeid: NodeId,
        flags: u32,
    ) -> io::Result<(Option<HandleId>, OpenOptions)> {
        self.do_open(&ctx, nodeid, flags)
    }

    fn release(
        &self,
        ctx: Context,
        nodeid: NodeId,
        _flags: u32,
        handle: HandleId,
        _flush: bool,
        _flock_release: bool,
        _lock_owner: Option<u64>,
    ) -> io::Result<()> {
        self.do_release(&ctx, nodeid, handle)
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
    ) -> io::Result<(Entry, Option<HandleId>, OpenOptions)> {
        let name = &name.to_string_lossy();
        let parent = self.get_node(parent)?;
        let flags = self.convert_open_flags(flags as i32);

        // Safe because this doesn't modify any memory and we check the return value. We don't
        // really check `flags` because if the kernel can't handle poorly specified flags then we
        // have much bigger problems.
        let fd = parent.with_subpath(name, |c_path| unsafe {
            nix::fcntl::open(
                c_path,
                flags | OFlag::O_CREAT,
                Mode::from_bits_retain(mode as u16),
            )
            .map(|fd| File::from(fd))
        })?;

        set_xattr_stat(
            FileRef::Fd(fd.as_fd()),
            Some((ctx.uid, ctx.gid)),
            Some(libc::S_IFREG as u32 | (mode & !(umask & 0o777))),
        )?;

        // Set security context
        if let Some(secctx) = &extensions.secctx {
            set_secctx(FileRef::Fd(fd.as_fd()), secctx, false)?
        };

        let st = fstat(&fd, false)?;
        let (entry, node_flags) = self.finish_lookup(&parent, name, st, FileRef::Fd(fd.as_fd()))?;

        let (handle, opts) = self.finish_open(fd, flags, entry.nodeid, node_flags, st)?;
        Ok((entry, Some(handle), opts))
    }

    fn unlink(&self, ctx: Context, parent: NodeId, name: &CStr) -> io::Result<()> {
        self.do_unlink(ctx, parent, name, 0)
    }

    fn read<W: io::Write + ZeroCopyWriter>(
        &self,
        _ctx: Context,
        nodeid: NodeId,
        handle: HandleId,
        mut w: W,
        size: u32,
        offset: u64,
        _lock_owner: Option<u64>,
        _flags: u32,
    ) -> io::Result<usize> {
        let data = self.get_handle(nodeid, handle)?;

        // This is safe because write_from uses preadv64, so the underlying file descriptor
        // offset is not affected by this operation.
        debug!("read: {:?}", nodeid);
        w.write_from(&data.file, size as usize, offset)
    }

    fn write<R: io::Read + ZeroCopyReader>(
        &self,
        _ctx: Context,
        nodeid: NodeId,
        handle: HandleId,
        mut r: R,
        size: u32,
        offset: u64,
        _lock_owner: Option<u64>,
        _delayed_write: bool,
        _kill_priv: bool,
        _flags: u32,
    ) -> io::Result<usize> {
        let data = self.get_handle(nodeid, handle)?;

        // This is safe because read_to uses pwritev64, so the underlying file descriptor
        // offset is not affected by this operation.
        debug!(
            "write: nodeid={} handle={:?} size={} offset={}",
            nodeid, handle, size, offset
        );
        r.read_to(&data.file, size as usize, offset)
    }

    fn getattr(
        &self,
        ctx: Context,
        nodeid: NodeId,
        handle: Option<HandleId>,
    ) -> io::Result<(bindings::stat64, Duration)> {
        debug!("getattr: nodeid={} handle={:?}", nodeid, handle);
        let file_ref = self.get_file_ref(nodeid, handle)?;
        self.do_getattr(file_ref.as_ref(), nodeid)
    }

    fn setattr(
        &self,
        ctx: Context,
        nodeid: NodeId,
        attr: bindings::stat64,
        handle: Option<HandleId>,
        valid: SetattrValid,
    ) -> io::Result<(bindings::stat64, Duration)> {
        self.do_setattr(ctx, nodeid, attr, handle, valid)
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
        // whiteout is not supported until we implement set_xattr_stat
        if ((flags as i32) & bindings::LINUX_RENAME_WHITEOUT) != 0 {
            return Err(Errno::EINVAL.into());
        }

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
            return Err(Errno::EINVAL.into());
        }

        let oldname = oldname.to_string_lossy();
        let smol_oldname = SmolStr::from(oldname.clone());
        let newname = newname.to_string_lossy();
        let old_dir = self.get_node(olddir)?;
        let new_dir = self.get_node(newdir)?;

        // lock old dir, old node, new dir, and (if exists) new node
        // prevents race where stale path is used for open, pointing to wrong file
        //let old_node = old_dir.get_child(&oldname)?;
        let old_node: Arc<NodeData> = self
            .node_children
            .get(&(olddir, smol_oldname))
            .unwrap()
            .upgrade()
            .unwrap(); // this unwrap() might be risky

        //let new_node = new_dir.get_child(&newname).ok();

        //let _old_dir_loc = old_dir.loc.write();
        let mut old_node_loc = old_node.loc.write();

        //let _new_dir_loc = new_dir.loc.write();
        // if let Some(ref new_node) = new_node {
        //     unsafe { new_node.loc.raw().lock_exclusive() };
        //     let _guard =
        //         scopeguard::guard((), |_| unsafe { new_node.loc.raw().unlock_exclusive() });
        // }

        let res = old_dir.with_subpath(&oldname, |old_cpath| {
            new_dir.with_subpath(&newname, |new_cpath| {
                let mut res =
                    unsafe { libc::renamex_np(old_cpath.as_ptr(), new_cpath.as_ptr(), mflags) };
                // ENOTSUP = not supported by FS (e.g. NFS). retry and simulate if only flag is RENAME_EXCL
                // GNU coreutils 'mv' uses RENAME_EXCL so this is common
                // (hard to simulate RENAME_SWAP)
                if res == -1 && Errno::last() == Errno::ENOTSUP && mflags == libc::RENAME_EXCL {
                    // EXCL means that target must not exist, so check it
                    match access(new_cpath, AccessFlags::F_OK) {
                        Ok(_) => return Err(Errno::EEXIST),
                        Err(Errno::ENOENT) => {}
                        Err(e) => return Err(e),
                    }

                    res = unsafe { libc::renamex_np(old_cpath.as_ptr(), new_cpath.as_ptr(), 0) }
                }
                Ok(res)
            })
        })?;

        if res == 0 {
            // make the change in our FS tree
            // after rename returns, Linux updates its dentry tree to reflect the rename, using old inode/nodeid
            if mflags & libc::RENAME_SWAP != 0 {
                // swap
                // TODO
                todo!();
            } else {
                // rename
                let old_name_smol = SmolStr::from(oldname);
                let new_name_smol = SmolStr::from(newname);
                *old_node_loc = NodeLocation {
                    parent: Some(new_dir.clone()),
                    name: new_name_smol.clone(),
                };
                drop(old_node_loc);
                self.node_children
                    .insert((new_dir.nodeid, new_name_smol), Arc::downgrade(&old_node));
                self.node_children.remove(&(old_dir.nodeid, old_name_smol));
            }

            Ok(())
        } else {
            Err(io::Error::last_os_error())
        }
    }

    fn mknod(
        &self,
        ctx: Context,
        parent: NodeId,
        name: &CStr,
        mode: u32,
        rdev: u32,
        umask: u32,
        extensions: Extensions,
    ) -> io::Result<Entry> {
        debug!(
            "mknod: parent={} name={:?} mode={:x} rdev={} umask={:x}",
            parent, name, mode, rdev, umask
        );

        let name = &name.to_string_lossy();
        self.get_node(parent)?.with_subpath(name, |c_path| {
            // since we run as a normal user, we can't call mknod() to create chr/blk devices
            // TODO: once we support mode overrides, represent them with empty files / sockets
            match mode as u16 & libc::S_IFMT {
                0 | libc::S_IFREG => {
                    // on Linux, mknod can be used to create regular files using fmt = S_IFREG or 0
                    open(
                        c_path.as_ref(),
                        // match mknod behavior: EEXIST if already exists
                        OFlag::O_CREAT | OFlag::O_EXCL | OFlag::O_CLOEXEC,
                        // permissions only
                        Mode::from_bits_truncate(mode as u16),
                    )?;
                }
                libc::S_IFIFO => {
                    // FIFOs are actually safe because Linux just treats them as a device node, and will never issue VFS read call because they can't have data on real filesystems
                    // read/write blocking is all handled by the kernel
                    mkfifo(c_path, Mode::from_bits_truncate(mode as u16))?;
                }
                libc::S_IFSOCK => {
                    // we use datagram because it doesn't call listen
                    let _ = UnixDatagram::bind(OsStr::from_bytes(c_path.to_bytes()))?;
                }
                libc::S_IFCHR | libc::S_IFBLK => {
                    return Err(Errno::EPERM.into());
                }
                _ => {
                    return Err(Errno::EINVAL.into());
                }
            }

            // Set security context
            if let Some(secctx) = &extensions.secctx {
                set_secctx(FileRef::Path(&c_path), secctx, false)?
            };

            set_xattr_stat(
                FileRef::Path(c_path),
                Some((ctx.uid, ctx.gid)),
                Some(mode & !umask),
            )
        })?;
        self.do_lookup(parent, name)
    }

    fn link(
        &self,
        ctx: Context,
        nodeid: NodeId,
        new_parent_id: NodeId,
        new_name: &CStr,
    ) -> io::Result<Entry> {
        let new_name = &new_name.to_string_lossy();
        let new_parent = self.get_node(new_parent_id)?;
        self.get_node(nodeid)?.with_path(|orig_c_path| {
            new_parent.with_subpath(new_name, |new_c_path| {
                debug!(
                    "link new_parent={} new_name={} oldpath={:?} newpath={:?}",
                    new_parent.nodeid, new_name, orig_c_path, new_c_path
                );
                // Safe because this doesn't modify any memory and we check the return value.
                if new_parent.flags.contains(NodeFlags::LINK_AS_CLONE) {
                    // translate link to clonefile as a workaround for slow hardlinking on APFS, and because ioctl(FICLONE) semantics don't fit macOS well
                    let res = unsafe {
                        libc::clonefile(orig_c_path.as_ptr(), new_c_path.as_ptr(), CLONE_NOFOLLOW)
                    };
                    if res == -1 && Errno::last() == Errno::ENOTSUP {
                        // only APFS supports clonefile. fall back to link
                        nix::unistd::linkat(
                            fcntl::AT_FDCWD,
                            orig_c_path,
                            fcntl::AT_FDCWD,
                            new_c_path,
                            // NOFOLLOW is default; AT_SYMLINK_FOLLOW is opt-in
                            AtFlags::empty(),
                        )
                    } else {
                        Ok(())
                    }
                } else {
                    // only APFS supports clonefile. fall back to link
                    nix::unistd::linkat(
                        fcntl::AT_FDCWD,
                        orig_c_path,
                        fcntl::AT_FDCWD,
                        new_c_path,
                        // NOFOLLOW is default; AT_SYMLINK_FOLLOW is opt-in
                        AtFlags::empty(),
                    )
                }
            })
        })?;
        self.do_lookup(new_parent_id, new_name)
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
        self.get_node(parent)?.with_subpath(name, |c_path| {
            // Safe because this doesn't modify any memory and we check the return value.
            symlinkat(linkname, fcntl::AT_FDCWD, c_path)?;

            // Set security context
            if let Some(secctx) = &extensions.secctx {
                set_secctx(FileRef::Path(c_path), secctx, true)?
            };

            let entry = self.do_lookup(parent, name)?;

            // update xattr stat, and make sure it's reflected by current stat
            let mode = libc::S_IFLNK | 0o777;
            set_xattr_stat(
                FileRef::Path(c_path),
                Some((ctx.uid, ctx.gid)),
                Some(mode as u32),
            )?;

            Ok(entry)
        })
    }

    fn readlink(&self, _ctx: Context, nodeid: NodeId) -> io::Result<Vec<u8>> {
        let mut buf = vec![0; libc::PATH_MAX as usize];
        let res = self.get_node(nodeid)?.with_path(|c_path| unsafe {
            let res = libc::readlink(
                c_path.as_ptr(),
                buf.as_mut_ptr() as *mut libc::c_char,
                buf.len(),
            );
            debug!(?c_path, ?res, "readlink");
            res
        });
        if res == -1 {
            return Err(io::Error::last_os_error());
        }

        buf.resize(res as usize, 0);
        Ok(buf)
    }

    fn flush(
        &self,
        _ctx: Context,
        _nodeid: NodeId,
        _handle: HandleId,
        _lock_owner: u64,
    ) -> io::Result<()> {
        // returning ENOSYS causes no_flush=1 to be set, skipping future calls
        // we could emulate this with dup+close to trigger nfs_vnop_close on NFS,
        // but it's usually ok to just wait for last fd to be closed (i.e. RELEASE)
        // multi-fd is rare anyway
        Err(Errno::ENOSYS.into())
    }

    fn fsync(
        &self,
        _ctx: Context,
        nodeid: NodeId,
        _datasync: bool,
        handle: HandleId,
    ) -> io::Result<()> {
        let data = self.get_handle(nodeid, handle)?;

        // use barrier fsync to preserve semantics and avoid DB corruption
        // Safe because this doesn't modify any memory and we check the return value.
        let res = unsafe { libc::fcntl(data.file.as_raw_fd(), libc::F_BARRIERFSYNC, 0) };

        if res == 0 {
            Ok(())
        } else {
            Err(io::Error::last_os_error())
        }
    }

    fn fsyncdir(
        &self,
        ctx: Context,
        nodeid: NodeId,
        datasync: bool,
        handle: HandleId,
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
            return Err(Errno::ENOSYS.into());
        }

        if name.to_bytes() == STAT_XATTR_KEY {
            return Err(Errno::EACCES.into());
        }

        let mut mflags: i32 = 0;
        if (flags as i32) & bindings::LINUX_XATTR_CREATE != 0 {
            mflags |= libc::XATTR_CREATE;
        }
        if (flags as i32) & bindings::LINUX_XATTR_REPLACE != 0 {
            mflags |= libc::XATTR_REPLACE;
        }

        let res = self.get_node(nodeid)?.with_path(|c_path| unsafe {
            // Safe because this doesn't modify any memory and we check the return value.
            libc::setxattr(
                c_path.as_ptr(),
                name.as_ptr(),
                value.as_ptr() as *const libc::c_void,
                value.len(),
                0,
                mflags as libc::c_int,
            )
        });

        if res == 0 {
            Ok(())
        } else {
            Err(io::Error::last_os_error())
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
            return Err(Errno::ENOSYS.into());
        }

        if name.to_bytes() == STAT_XATTR_KEY {
            return Err(Errno::EACCES.into());
        }

        if size > MAX_XATTR_SIZE as u32 {
            return Err(Errno::E2BIG.into());
        }

        let mut buf = vec![0; size as usize];

        let res = self.get_node(nodeid)?.with_path(|c_path| {
            unsafe {
                // Safe because this will only modify the contents of `buf`
                if size == 0 {
                    libc::getxattr(
                        c_path.as_ptr(),
                        name.as_ptr(),
                        std::ptr::null_mut(),
                        0,
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
            }
        });

        if res == -1 {
            return Err(io::Error::last_os_error());
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
            return Err(Errno::ENOSYS.into());
        }

        // Safe because this will only modify the contents of `buf`.
        let buf = self.get_node(nodeid)?.with_path(listxattr)?;

        if size == 0 {
            let mut clean_size = buf.len();

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
                Err(Errno::ERANGE.into())
            } else {
                Ok(ListxattrReply::Names(clean_buf))
            }
        }
    }

    fn removexattr(&self, _ctx: Context, nodeid: NodeId, name: &CStr) -> io::Result<()> {
        if !self.cfg.xattr {
            return Err(Errno::ENOSYS.into());
        }

        if name.to_bytes() == STAT_XATTR_KEY {
            return Err(Errno::EACCES.into());
        }

        // Safe because this doesn't modify any memory and we check the return value.
        let res = self
            .get_node(nodeid)?
            .with_path(|c_path| unsafe { libc::removexattr(c_path.as_ptr(), name.as_ptr(), 0) });

        if res == 0 {
            Ok(())
        } else {
            Err(io::Error::last_os_error())
        }
    }

    fn fallocate(
        &self,
        _ctx: Context,
        nodeid: NodeId,
        handle: HandleId,
        mode: u32,
        offset: u64,
        length: u64,
    ) -> io::Result<()> {
        debug!(
            "fallocate: nodeid={} handle={:?} mode={} offset={} length={}",
            nodeid, handle, mode, offset, length
        );

        let data = self.get_handle(nodeid, handle)?;

        let file = &data.file;
        match mode {
            0 | FALLOC_FL_KEEP_SIZE => {
                // determine how many blocks to preallocate
                let st = fstat(file.as_fd(), true)?;
                let new_end = offset + length;
                let size_diff = new_end.saturating_sub(st.st_size as u64);
                let num_blocks = size_diff.div_ceil(st.st_blksize as u64);

                if num_blocks > 0 {
                    // this allocates blocks but doesn't change st_size
                    let mut fs = libc::fstore_t {
                        fst_flags: libc::F_ALLOCATEALL,
                        // TODO: what is volume offset? it seems to let us position the blocks with a "block location hint", but requires that length >= file size?
                        fst_posmode: libc::F_PEOFPOSMODE,
                        // offset must be 0 for physical EOF mode
                        // basically, this allocates extents and attempts to make them contiguous starting from the last (EOF) block, but it doesn't place the extents anywhere
                        fst_offset: 0,
                        // this is the number of bytes to allocate extents for, *not* offset+length target size
                        // we don't need to zero existing ranges
                        fst_length: num_blocks as i64 * st.st_blksize as i64,
                        fst_bytesalloc: 0,
                    };
                    let res = unsafe {
                        libc::fcntl(file.as_raw_fd(), libc::F_PREALLOCATE, &mut fs as *mut _)
                    };
                    if res == -1 {
                        return Err(io::Error::last_os_error());
                    }
                }

                // only change size if requested, and if new size is *greater*
                if mode & FALLOC_FL_KEEP_SIZE == 0 && new_end > st.st_size as u64 {
                    let res = unsafe { libc::ftruncate(file.as_raw_fd(), new_end as i64) };
                    if res == -1 {
                        return Err(io::Error::last_os_error());
                    }
                }

                Ok(())
            }

            FALLOC_FL_KEEP_SIZE_AND_PUNCH_HOLE => {
                let st = fstat(file.as_fd(), true)?;

                let zero_start = offset as libc::off_t;
                // the file must not grow. F_PUNCHHOLE can grow it
                let zero_end = (offset + length).min(st.st_size as u64) as libc::off_t;

                // macOS requires FS block size alignment
                // Linux zeroes partial blocks
                let block_size = st.st_blksize as libc::off_t;
                // start: round up
                let hole_start = (zero_start + block_size - 1) / block_size * block_size;
                // end: round down
                let hole_end = zero_end / block_size * block_size;

                if hole_start != hole_end {
                    let punch_hole = libc::fpunchhole_t {
                        fp_flags: 0,
                        reserved: 0,
                        fp_offset: hole_start,
                        fp_length: hole_end - hole_start,
                    };
                    let res =
                        unsafe { libc::fcntl(file.as_raw_fd(), libc::F_PUNCHHOLE, &punch_hole) };
                    if res == -1 {
                        return Err(io::Error::last_os_error());
                    }
                }

                // zero the starting block
                let zero_start_len = hole_start - zero_start;
                if zero_start_len > 0 {
                    let zero_start_buf = vec![0u8; zero_start_len as usize];
                    pwrite(file.as_fd(), &zero_start_buf, zero_start)?;
                }

                // zero the ending block
                let zero_end_len = zero_end - hole_end;
                if zero_end_len > 0 {
                    let zero_end_buf = vec![0u8; zero_end_len as usize];
                    pwrite(file.as_fd(), &zero_end_buf, hole_end)?;
                }

                Ok(())
            }

            // don't think it's possible to emulate FALLOC_FL_ZERO_RANGE
            // most programs (e.g. mkfs.ext4) will fall back to FALLOC_FL_PUNCH_HOLE
            _ => Err(Errno::EOPNOTSUPP.into()),
        }
    }

    fn lseek(
        &self,
        _ctx: Context,
        nodeid: NodeId,
        handle: HandleId,
        offset: u64,
        whence: u32,
    ) -> io::Result<u64> {
        debug!(
            "lseek: nodeid={} handle={:?} offset={} whence={}",
            nodeid, handle, offset, whence
        );

        let data = self.get_handle(nodeid, handle)?;

        // FUSE will only send SEEK_DATA and SEEK_HOLE.
        // it handles SEEK_SET, SEEK_CUR, SEEK_END itself
        let mac_whence = match whence {
            // this behavior used to be different:
            //   - Linux: if offset is in a data region, return offset
            //   - macOS: if offset is in a data region, return offset of *next* data region
            //   - macOS: can also return multiple adjacent, contiguous data regions
            // however, at least as of macOS 14.5, it's the same
            // implementation is in closed-source APFS so we can't check exactly when it changed
            LINUX_SEEK_DATA => SEEK_DATA,
            LINUX_SEEK_HOLE => SEEK_HOLE,
            _ => return Err(Errno::EINVAL.into()),
        };

        // result only depends on file and offset, not current pos, so this doesn't need an exclusive lock
        let len = unsafe { libc::lseek(data.file.as_raw_fd(), offset as i64, mac_whence) };
        Errno::result(len)?;
        Ok(len as u64)
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
    ) -> io::Result<&[u8]> {
        if self.cfg.allow_rosetta_ioctl {
            match cmd {
                // version-agnostic mask: match on dir/type/size; ignore nr
                x if x & IOCTL_ROSETTA_KEY_MASK == IOCTL_ROSETTA_KEY & IOCTL_ROSETTA_KEY_MASK => {
                    let payload = get_rosetta_data();
                    if payload.len() >= out_size as usize {
                        let resp = &payload[..out_size as usize];
                        debug!("returning rosetta data: {:?}", resp);
                        return Ok(resp);
                    }
                }

                IOCTL_ROSETTA_AOT_CONFIG => {
                    debug!("returning AOT config");
                    return Ok(&ROSETTA_AOT_CONFIG);
                }

                IOCTL_ROSETTA_TSO_FALLBACK => {
                    debug!("TSO fallback");
                    // empty response
                    return Ok(&[]);
                }

                _ => {}
            }
        }

        Err(Errno::ENOTTY.into())
    }
}

trait VecExt {
    fn insert_slice(&mut self, index: usize, bytes: &[u8]);
}

impl VecExt for Vec<u8> {
    // copied from std String::insert_str
    fn insert_slice(&mut self, index: usize, bytes: &[u8]) {
        let len = self.len();
        let amt = bytes.len();
        self.reserve(amt);

        unsafe {
            std::ptr::copy(
                self.as_ptr().add(index),
                self.as_mut_ptr().add(index + amt),
                len - index,
            );
            std::ptr::copy_nonoverlapping(bytes.as_ptr(), self.as_mut_ptr().add(index), amt);
            self.set_len(len + amt);
        }
    }
}
