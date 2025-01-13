// Copyright 2019 The Chromium OS Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

// Copyright 2024 Orbital Labs, LLC. All rights reserved.
// Changes to this file are confidential and proprietary, subject to terms in the LICENSE file.

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
use std::os::unix::io::{AsRawFd, FromRawFd};
use std::os::unix::net::UnixDatagram;
use std::path::Path;
use std::ptr::{slice_from_raw_parts, NonNull};
use std::sync::atomic::{AtomicBool, AtomicI64, AtomicU64, Ordering};
use std::sync::Arc;
use std::thread::JoinHandle;
use std::time::Duration;

use crate::virtio::fs::attrlist::{self, AttrlistEntry};
use crate::virtio::fs::filesystem::SecContext;
use crate::virtio::fs::multikey::MultikeyFxDashMap;
use crate::virtio::rosetta::get_rosetta_data;
use crate::virtio::{FsCallbacks, FxDashMap, NfsInfo};
use bitflags::bitflags;
use derive_more::{Display, From, Into};
use libc::{AT_FDCWD, MAXPATHLEN, SEEK_DATA, SEEK_HOLE};
use nix::errno::Errno;
use nix::fcntl::{open, AtFlags, OFlag};
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
use super::sys::fsgetpath_exists;
use super::vnode_poll::VnodePoller;

// disabled because Linux doesn't FORGET everything on unmount
const DETECT_REFCOUNT_LEAKS: bool = false;

const IOC_NONE: u8 = 0;
#[allow(dead_code)]
const IOC_WRITE: u8 = 1;
const IOC_READ: u8 = 2;

const fn _ioc(dir: u8, typ: u8, nr: u8, size: u16) -> u32 {
    (size as u32) << 16 | (dir as u32) << 30 | (typ as u32) << 8 | nr as u32
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
// maxfilesperproc=10240 on 8 GB x86
// must keep our own fd limit to avoid breaking vmgr
const MAX_PATH_FDS: u64 = 8000;

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

    fn check_io(&self, ctx: &Context) -> io::Result<()> {
        // if in synchronous (hvc) context, force guest to retry with async worker
        if self.node_flags.contains(NodeFlags::NO_SYNC_IO) && ctx.host.is_sync_call {
            debug!("require async");
            return Err(Errno::EDEADLK.into());
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
    let mut st = nix::sys::stat::fstat(fd.as_fd().as_raw_fd())?;

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

struct NodeData {
    // cached stat info
    // moving this here, along with packed dev_ino, saves 8 bytes (10%!)
    flags: NodeFlags, // for flags propagated to children
    nlink: u16,       // for getattrlistbulk buffer size

    dev_ino: DevIno,

    // state
    refcount: AtomicI64,
    // for CTO consistency: clear cache on open if ctime has changed
    // must only be updated on open
    last_open_ctime: AtomicI64,

    // open fd, if volfs is not supported
    // Arc makes sure this fd won't be closed while a FS call is using it
    // open-fd nodes are the slow case anyway, so this is OK for perf
    fd: Option<Arc<OwnedFd>>,

    // for path-based dev/ino refresh on dentry swap
    parent_nodeid: OptionNodeId,
    name: SmolStr,
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
    fn check_io(&self, ctx: &Context) -> io::Result<()> {
        // if in synchronous (hvc) context, force guest to retry with async worker
        if self.flags.contains(NodeFlags::NO_SYNC_IO) && ctx.host.is_sync_call {
            debug!("require async");
            return Err(Errno::EDEADLK.into());
        }

        Ok(())
    }
}

#[derive(Copy, Clone, Debug, Default, Ord, PartialOrd, Eq, PartialEq, Hash)]
#[repr(packed)]
struct DevIno(pub i32, pub u64);

#[derive(Debug, Copy, Clone)]
struct DevInfo {
    // supports volfs?
    volfs: bool,
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
    nodeids: MultikeyFxDashMap<NodeId, DevIno, NodeData>,
    next_nodeid: AtomicU64,

    handles: Arc<FxDashMap<HandleId, Arc<HandleData>>>,
    next_handle: AtomicU64,

    // volfs supported?
    dev_info: FxDashMap<i32, DevInfo>,
    num_open_fds: AtomicU64,

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
        let nodeids = MultikeyFxDashMap::new();
        nodeids.insert(
            NodeId(fuse::ROOT_ID),
            st.dev_ino(),
            NodeData {
                dev_ino: st.dev_ino(),

                // refcount 2 so it can never be dropped
                refcount: AtomicI64::new(2),
                last_open_ctime: AtomicI64::new(st.ctime_ns()),
                flags: NodeFlags::empty(),
                fd: None,
                nlink: st.st_nlink,

                parent_nodeid: None,
                name: SmolStr::new(""),
            },
        );

        let dev_info = FxDashMap::default();

        let handles = Arc::new(FxDashMap::default());
        let poller = match callbacks {
            Some(callbacks) => Some(Arc::new(VnodePoller::new(callbacks, handles.clone())?)),
            None => None,
        };

        let mut fs = PassthroughFs {
            nodeids,
            next_nodeid: AtomicU64::new(fuse::ROOT_ID + 1),

            handles,
            next_handle: AtomicU64::new(1),

            dev_info,
            num_open_fds: AtomicU64::new(0),

            writeback: AtomicBool::new(false),
            cfg,

            // poller is useless if we have no invalidation callbacks
            poller,
            poller_thread: None,
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

    // TODO: this entire set of functions is a mess
    fn get_nodeid(
        &self,
        ctx: &Context,
        nodeid: NodeId,
    ) -> io::Result<(DevIno, NodeFlags, Option<Arc<OwnedFd>>)> {
        // race OK: primary key lookup only
        let node = self.nodeids.get(&nodeid).ok_or_else(|| ebadf(nodeid))?;
        node.check_io(ctx)?;
        Ok((node.dev_ino, node.flags, node.fd.clone()))
    }

    // note: /.vol (volfs) is undocumented and deprecated
    // but worst-case scenario: we can use public APIs (fsgetpath) to get the path,
    // and also cache O_EVTONLY fds and paths.
    // lstat realpath=681.85ns, volfs=895.88ns, fsgetpath=1.1478us, lstat+fsgetpath=1.8592us
    fn nodeid_to_file_ref_and_data(
        &self,
        ctx: &Context,
        nodeid: NodeId,
    ) -> io::Result<(OwnedFileRef<OwnedFd>, NodeFlags)> {
        let (DevIno(dev, ino), node_flags, fd) = self.get_nodeid(ctx, nodeid)?;
        if let Some(fd) = fd {
            Ok((OwnedFileRef::Fd(fd), node_flags))
        } else {
            let path = self.devino_to_path(DevIno(dev, ino))?;
            Ok((OwnedFileRef::Path(path), node_flags))
        }
    }

    #[allow(dead_code)]
    fn nodeid_to_file_ref(
        &self,
        ctx: &Context,
        nodeid: NodeId,
    ) -> io::Result<OwnedFileRef<OwnedFd>> {
        Ok(self.nodeid_to_file_ref_and_data(ctx, nodeid)?.0)
    }

    fn nodeid_to_path_and_data(
        &self,
        ctx: &Context,
        nodeid: NodeId,
    ) -> io::Result<(CString, NodeFlags)> {
        let (file_ref, node_flags) = self.nodeid_to_file_ref_and_data(ctx, nodeid)?;
        match file_ref {
            OwnedFileRef::Path(path) => Ok((path, node_flags)),
            OwnedFileRef::Fd(fd) => {
                // to minimize race window and support renames, get latest path from fd
                // this also allows minimal opens (EVTONLY | RDONLY)
                // TODO: all handlers should support Fd or Path. this is just lowest-effort impl
                let path = get_path_by_fd(fd)?;
                Ok((CString::new(path)?, node_flags))
            }
        }
    }

    fn nodeid_to_path(&self, ctx: &Context, nodeid: NodeId) -> io::Result<CString> {
        Ok(self.nodeid_to_path_and_data(ctx, nodeid)?.0)
    }

    fn name_to_path_and_data(
        &self,
        ctx: &Context,
        parent: NodeId,
        name: &str,
    ) -> io::Result<(CString, DevIno, NodeFlags)> {
        // deny ".." to prevent root escape
        if name == ".." || name.contains('/') {
            return Err(Errno::ENOENT.into());
        }

        let (DevIno(parent_device, parent_inode), parent_flags, fd) =
            self.get_nodeid(ctx, parent)?;
        let path = if let Some(fd) = fd {
            // to minimize race window and support renames, get latest path from fd
            // this also allows minimal opens (EVTONLY | RDONLY)
            // TODO: all handlers should support Fd or Path. this is just lowest-effort impl
            format!("{}/{}", get_path_by_fd(fd)?, name)
        } else {
            format!("/.vol/{}/{}/{}", parent_device, parent_inode, name)
        };

        let cstr = CString::new(path)?;
        Ok((cstr, DevIno(parent_device, parent_inode), parent_flags))
    }

    fn name_to_path(&self, ctx: &Context, parent: NodeId, name: &str) -> io::Result<CString> {
        Ok(self.name_to_path_and_data(ctx, parent, name)?.0)
    }

    fn devino_to_path(&self, DevIno(dev, ino): DevIno) -> io::Result<CString> {
        let path = format!("/.vol/{}/{}", dev, ino);
        let cstr = CString::new(path)?;
        Ok(cstr)
    }

    fn get_handle(
        &self,
        ctx: &Context,
        _nodeid: NodeId,
        handle: HandleId,
    ) -> io::Result<Arc<HandleData>> {
        let hd = self.handles.get(&handle).ok_or(Errno::EBADF)?;
        hd.check_io(ctx)?;
        Ok(hd.clone())
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
            volfs: (stf.f_flags & libc::MNT_DOVOLFS as u32) != 0,
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
        ctx: &Context,
        parent: NodeId,
        name: &str,
    ) -> io::Result<(CString, NodeFlags, libc::stat)> {
        let (mut c_path, DevIno(parent_dev, parent_ino), parent_flags) =
            self.name_to_path_and_data(ctx, parent, name)?;
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
        Ok((c_path, parent_flags, st))
    }

    fn do_lookup(&self, parent: NodeId, name: &str, ctx: &Context) -> io::Result<Entry> {
        let (c_path, parent_flags, st) = self.begin_lookup(ctx, parent, name)?;
        let (entry, _) =
            self.finish_lookup(parent, parent_flags, name, st, FileRef::Path(&c_path))?;
        Ok(entry)
    }

    fn filter_stat(&self, nodeid: NodeId, st: &mut bindings::stat64) {
        // TODO: xattr stat

        // root generation must be zero
        // for other inodes, we ignore st_gen because getattrlistbulk (readdirplus) doesn't support it, so returning it here would break revalidate
        st.st_gen = 0;

        // return nodeid as st_ino to avoid collisions across host fileesystems, as st_dev is always the same
        st.st_ino = nodeid.0;
    }

    fn finish_lookup(
        &self,
        parent: NodeId,
        parent_flags: NodeFlags,
        name: &str,
        mut st: bindings::stat64,
        file_ref: FileRef,
    ) -> io::Result<(Entry, NodeFlags)> {
        // race OK: if we fail to find a nodeid by (dev,ino), we'll just make a new one, and old one will gradually be forgotten
        let dev_ino = st.dev_ino();
        let (nodeid, node_flags) = if let Some(node) = self.nodeids.get_alt(&dev_ino) {
            // no check_io: caller already checked before it got the stat result
            // there is already a nodeid for this (dev, ino)
            // increment the refcount and return it
            node.refcount.fetch_add(1, Ordering::Relaxed);
            (*node.key(), node.flags)
        } else {
            // this (dev, ino) is new
            // create a new nodeid and return it
            let mut new_nodeid = self.next_nodeid.fetch_add(1, Ordering::Relaxed).into();

            let dev_info = self.get_dev_info(st.st_dev, || Ok(file_ref))?;

            // open fd if volfs is not supported
            let fd = if !dev_info.volfs && st.can_open() {
                debug!("open fd");

                // TODO: evict fds and cache as paths
                if self.num_open_fds.fetch_add(1, Ordering::Relaxed) >= MAX_PATH_FDS {
                    self.num_open_fds.fetch_sub(1, Ordering::Relaxed);
                    return Err(Errno::ENFILE.into());
                }

                // OFlag::from_bits_truncate truncates O_SYMLINK
                let oflag = OFlag::from_bits_retain(
                    // SYMLINK implies NOFOLLOW, but NOFOLLOW prevents actually opening the symlink
                    libc::O_EVTONLY | libc::O_CLOEXEC | libc::O_SYMLINK,
                );

                // must reopen even if we have fd, to get O_EVTONLY. dup can't do that
                let fd = match file_ref {
                    FileRef::Path(c_path) => nix::fcntl::open(c_path, oflag, Mode::empty()),
                    FileRef::Fd(fd) => {
                        // TODO: faster to ask caller for c_path here
                        nix::fcntl::open(Path::new(&get_path_by_fd(fd)?), oflag, Mode::empty())
                    }
                }?;
                Some(Arc::new(unsafe { OwnedFd::from_raw_fd(fd) }))
            } else {
                debug!("skip open");
                None
            };

            let mut node = NodeData {
                dev_ino,

                refcount: AtomicI64::new(1),
                last_open_ctime: AtomicI64::new(st.ctime_ns()),
                flags: parent_flags & NodeFlags::INHERITED_FLAGS,
                fd,
                nlink: st.st_nlink,

                parent_nodeid: parent.option(),
                name: SmolStr::from(name),
            };

            // flag to use clonefile instead of link, for package managers
            if name == LINK_AS_CLONE_DIR_JS || name == LINK_AS_CLONE_DIR_PY {
                node.flags |= NodeFlags::LINK_AS_CLONE;
            }

            // no sync IO on remote/network file systems
            if !dev_info.local {
                node.flags |= NodeFlags::NO_SYNC_IO;
            }

            // deadlock OK: we're not holding a ref, since lookup returned None
            let node_flags = node.flags;
            let inserted_nodeid = self.nodeids.insert(new_nodeid, dev_ino, node);
            if inserted_nodeid != new_nodeid {
                // we raced with another thread, which added a nodeid for this (dev, ino)
                // does the old nodeid still exist?
                let found_existing = if let Some(node) = self.nodeids.get(&inserted_nodeid) {
                    // no check_io: no IO after this point
                    // old nodeid exists. increment refcount so we can use it instead
                    node.refcount.fetch_add(1, Ordering::Relaxed);
                    true
                } else {
                    // just in case it's gone, keep our new nodeid. it wasn't a duplicate after all
                    false
                };

                if found_existing {
                    // old nodeid exists, and we incremented its refcount
                    // deadlock OK: we just released the read shard lock
                    self.nodeids.remove_main(&new_nodeid);
                    // use the old nodeid
                    new_nodeid = inserted_nodeid;
                }
            }

            (new_nodeid, node_flags)
        };

        debug!(
            "finish_lookup: dev={} ino={} ref={:?} -> nodeid={}",
            st.st_dev, st.st_ino, file_ref, nodeid
        );

        self.filter_stat(nodeid, &mut st);

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
        // race OK: primary key lookup only
        if let Some(node) = self.nodeids.get(&nodeid) {
            // no check_io: closing a read-only fd is OK and won't trigger flush

            // decrement the refcount
            if node.refcount.fetch_sub(count as i64, Ordering::Relaxed) == count as i64 {
                // count - count = 0
                // this nodeid is no longer in use

                // decrement open fds
                if node.fd.is_some() {
                    self.num_open_fds.fetch_sub(1, Ordering::Relaxed);
                }

                // remove from multikey alt mapping, so that next lookup with (dev,ino) creates a new nodeid
                // race OK: we make sure K2 will never map to a missing K1
                self.nodeids.remove_alt(&node.dev_ino);

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
        ctx: &Context,
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

        let data = self.get_handle(ctx, nodeid, handle)?;
        // race OK: FUSE won't FORGET until all handles are closed
        let (DevIno(dev, _), _, _) = self.get_nodeid(ctx, nodeid)?;

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

            let mut ino = unsafe { (*dentry).d_ino };
            if let Some(nfs_info) = self.cfg.nfs_info.as_ref() {
                // replace nfs mountpoint ino with /var/empty - that's what lookup returns
                if dev == nfs_info.dir_dev && ino == nfs_info.dir_inode {
                    ino = nfs_info.empty_dir_inode;
                }
            }

            // attempt to resolve (dev, ino) to the right nodeid
            let dt_ino = if let Some(node) = self.nodeids.get_alt(&DevIno(dev, ino)) {
                node.key().0
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
    ) -> io::Result<(
        Option<HandleId>,
        Option<(bindings::stat64, Duration)>,
        OpenOptions,
    )> {
        let flags = self.convert_open_flags(flags as i32);
        let (c_path, node_flags) = self.nodeid_to_path_and_data(ctx, nodeid)?;
        let file =
            unsafe { File::from_raw_fd(nix::fcntl::open(c_path.as_ref(), flags, Mode::empty())?) };
        // early stat to avoid broken handle state if it fails
        let st = fstat(&file, false)?;

        // Linux normally won't open fifos/devs, but guest might maliciously trick us into doing it
        // reading from one will cause us to hang on read, preventing VM stop
        if !st.can_open() {
            return Err(Errno::EOPNOTSUPP.into());
        }

        let (handle, opts) = self.finish_open(file, flags, nodeid, node_flags, st)?;
        let attr = self.finish_getattr(nodeid, st)?;
        Ok((Some(handle), Some(attr), opts))
    }

    fn do_release(&self, ctx: &Context, _nodeid: NodeId, handle: HandleId) -> io::Result<()> {
        if let dashmap::mapref::entry::Entry::Occupied(e) = self.handles.entry(handle) {
            // check_io needed: on NFS, close() will flush if fd was written to
            e.get().check_io(ctx)?;

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

        self.finish_getattr(nodeid, st)
    }

    fn finish_getattr(
        &self,
        nodeid: NodeId,
        mut st: bindings::stat64,
    ) -> io::Result<(bindings::stat64, Duration)> {
        self.filter_stat(nodeid, &mut st);
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
        let file_ref = self.get_file_ref(&ctx, nodeid, handle)?;

        if valid.contains(SetattrValid::MODE) {
            // TODO: store sticky bit in xattr. don't allow suid/sgid
            match file_ref.as_ref() {
                FileRef::Fd(fd) => {
                    fchmod(fd.as_raw_fd(), Mode::from_bits_truncate(attr.st_mode))?;
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
                FileRef::Fd(fd) => futimens(fd.as_raw_fd(), &atime, &mtime),
                FileRef::Path(path) => {
                    utimensat(None, path, &atime, &mtime, UtimensatFlags::NoFollowSymlink)
                }
            }?;
        }

        self.do_getattr(file_ref.as_ref(), nodeid)
    }

    fn do_unlink(
        &self,
        ctx: Context,
        parent: NodeId,
        name: &CStr,
        flags: libc::c_int,
    ) -> io::Result<()> {
        let c_path = self.name_to_path(&ctx, parent, &name.to_string_lossy())?;

        // Safe because this doesn't modify any memory and we check the return value.
        let res = unsafe { libc::unlinkat(AT_FDCWD, c_path.as_ptr(), flags) };

        if res == 0 {
            Ok(())
        } else {
            Err(io::Error::last_os_error())
        }
    }

    fn get_file_ref(
        &self,
        ctx: &Context,
        nodeid: NodeId,
        handle: Option<HandleId>,
    ) -> io::Result<OwnedFileRef<HandleData>> {
        if let Some(handle) = handle {
            let hd = self.get_handle(ctx, nodeid, handle)?;
            Ok(OwnedFileRef::Fd(hd))
        } else {
            let path = self.nodeid_to_path(ctx, nodeid)?;
            Ok(OwnedFileRef::Path(path))
        }
    }

    // this can't return EBADF, or any error, to the caller:
    // any errors are replaced with the original error, as this would've been a failed recovery attempt
    fn refresh_nodeid(&self, ctx: &Context, nodeid: NodeId) -> io::Result<()> {
        // try to look up new dev/ino at /.vol/$PARENT/$NAME
        // very uncommon case, so we release lock before access(2) and re-acquire it here
        //
        // instead of just using /.vol/$PARENT/$NAME as the new path, we resolve it to a new dev/ino because
        //   - more generic: don't need to pass path to with_nodeid_refresh closures
        //   - rename safety: it'll continue to refer to the correct file
        //   - plays well with other calls that might see the new dev/ino (will be recognized as same nodeid)
        // the one problem case is if the nodeid file was renamed, but that's ok:
        //   - linux should not be trying to access it by old name. that wouldn't work anyway
        //   - stale dev/ino is not possible if accessing by new name
        //   - if linux renamed it, we'll update the name
        // TODO: this breaks down with hard links. fix by storing SmallVec of links ((parent,name)) in NodeData
        let node = self.nodeids.get(&nodeid).ok_or_else(|| ebadf(nodeid))?;
        let old_devino = node.dev_ino;
        let parent = node.parent_nodeid.ok_or(Errno::ENOENT)?.get().into();
        // prevent deadlock with get_mut later, and with with_nodeid_refresh
        drop(node);

        // this can't recurse forever because root nodeid has no parent, and circular links are impossible
        self.with_nodeid_refresh(ctx, parent, || {
            // if this is a retry after refreshing parent, path_in_parent needs to be re-resolved
            // this is inefficient, but we need to get *another* read lock
            // can't get write lock yet: it could deadlock with name_to_path's read lock for parent (if same shard)
            // doesn't matter -- this path is an uncommon error recovery case
            let node = self.nodeids.get(&nodeid).ok_or_else(|| ebadf(nodeid))?;
            node.check_io(ctx)?;
            let path_in_parent = self.name_to_path(ctx, parent, &node.name)?;
            // we'll have to re-acquire the node ref later to get a write lock, so drop it to avoid doing I/O (lstat) with the lock held
            drop(node);

            debug!(?nodeid, ?old_devino, ?path_in_parent, "refresh_nodeid");
            let st = lstat(&path_in_parent, true)?;

            let new_devino = st.dev_ino();
            if new_devino == old_devino {
                // this could happen if two threads race on the ENOENT handling path:
                // one finishes refresh_nodeid before the other one starts and reads old_devino
                // this counts as a success, which is OK and can't loop forever because
                return Ok(());
            }

            // we got a new dev/ino
            // now begins the ritual of updating it

            // remove from alt map
            // race OK: since the old dev/ino doesn't currently exist on disk, no lookup/readdir can return it, so it doesn't need to be in alt map (and if it somehow does, it needs to be a new nodeid)
            // to avoid potential lock ordering issue with main map, do this without main lock held
            self.nodeids.remove_alt(&old_devino);

            // if another thread is racing on the same path, that's OK: it should get the same dev/ino result
            debug!(?new_devino, "refresh_nodeid: updating dev/ino");
            let mut node = self.nodeids.get_mut(&nodeid).ok_or_else(|| ebadf(nodeid))?;
            // no check_io: same node as above, and no IO after this point
            // update dev/ino used for deleting from alt map
            // no one else can read it right now
            node.dev_ino = new_devino;
            // avoid lock ordering issue with main map
            drop(node);

            // reinsert into alt map
            // race OK: another thread racing on this path will insert the same dev/ino
            let inserted_nodeid = self.nodeids.insert_alt(nodeid, new_devino);
            if inserted_nodeid != nodeid {
                // uh oh: someone else saw the new dev/ino in lookup/readdir
                // Linux now has the new nodeid in dcache, and old nodeid was marked as stale
                // stale means that FUSE will fail all future I/O on the old nodeid, so fail here too
                // TODO: any better way to handle this?
                error!(
                    ?nodeid,
                    ?inserted_nodeid,
                    "refresh_nodeid: race with new lookup"
                );
                return Err(Errno::EAGAIN.into());
            }

            Ok(())
        })
    }

    // if a "dentry swap" occurs, where the inode at a path/name changes, we get ENOENT
    // FSEvents monitor is racy and won't always notify us fast enough
    // example: rm -fr a; mkdir a; echo $RANDOM > a/F; orb cat a/F
    //
    // to fix it, we wrap all calls that can fail with ENOENT due to stale nodeid dev/ino
    // to disambiguate real ENOENT from stale dev/ino, we check whether the dev/ino still exists
    // (fsgetpath would eliminate ambiguity, but it's slower than even volfs str format + access)
    //
    // faster and more reliable to implement this on host side:
    // - ideally we get linux to revalidate it before open/..., but cache inval events will always be racy
    // - propagating a special error code to trigger revalidate = nasty core VFS hacks
    // - too expensive for FUSE to revalidate on every call
    fn with_nodeid_refresh<F, R>(&self, ctx: &Context, nodeid: NodeId, f: F) -> io::Result<R>
    where
        F: Fn() -> io::Result<R>,
    {
        match f() {
            Ok(r) => Ok(r),
            Err(e) if e.raw_os_error() == Some(libc::ENOENT) => {
                // ENOENT: this could be caused by
                //   - (if nodeid = parent) child name doesn't exist
                //   - (if nodeid = file) file was unlinked
                //   - dev/ino is stale
                //     - could be caused by parent, or file unlinked+replaced
                // to disambiguate, check whether the current dev/ino still exists
                let (DevIno(dev, ino), _, fd) = self.get_nodeid(ctx, nodeid)?;

                // stale dev/ino does not apply to fd-based nodeids, at least not in the same way: we'd have to reopen the fd
                // fsgetpath also won't work on such filesystems (ENOTSUP), so don't try
                if fd.is_some() {
                    return Err(e);
                }

                let dev_info = self.get_dev_info(dev, || self.nodeid_to_file_ref(ctx, nodeid))?;
                match fsgetpath_exists(dev_info.fsid, ino) {
                    Ok(true) => {
                        // dev/ino still exists:
                        // this is a real ENOENT, from child
                        // return the original error
                        Err(e)
                    }

                    Ok(false) => {
                        // dev/ino doesn't exist:
                        // this is a stale nodeid
                        // refresh it
                        match self.refresh_nodeid(ctx, nodeid) {
                            // retry if refreshed successfully
                            Ok(_) => {
                                debug!("retrying after refresh_nodeid");
                                f()
                            }
                            Err(e2) => {
                                // refresh failed: return the *original* error (ENOENT)
                                debug!(?e2, "refresh_nodeid failed");
                                Err(e)
                            }
                        }
                    }

                    // for any other error, ignore and return the original error
                    Err(e2) => {
                        debug!("failed to check if dev/ino exists: {:?}", e2);
                        Err(e)
                    }
                }
            }
            Err(e) => Err(e),
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
            for node in self.nodeids.iter_main() {
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

    fn statfs(&self, ctx: Context, nodeid: NodeId) -> io::Result<Statvfs> {
        self.with_nodeid_refresh(&ctx, nodeid, || {
            let c_path = self.nodeid_to_path(&ctx, nodeid)?;
            let stv = statvfs(c_path.as_ref())?;
            Ok(stv)
        })
    }

    fn lookup(&self, ctx: Context, parent: NodeId, name: &CStr) -> io::Result<Entry> {
        self.with_nodeid_refresh(&ctx, parent, || {
            debug!("lookup: {:?}", name);
            self.do_lookup(parent, &name.to_string_lossy(), &ctx)
        })
    }

    fn forget(&self, _ctx: Context, _nodeid: NodeId, _count: u64) {
        // no with_nodeid_refresh: this can't fail with ENOENT
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
    ) -> io::Result<(
        Option<HandleId>,
        Option<(bindings::stat64, Duration)>,
        OpenOptions,
    )> {
        self.with_nodeid_refresh(&ctx, nodeid, || {
            self.do_open(&ctx, nodeid, flags | libc::O_DIRECTORY as u32)
        })
    }

    fn releasedir(
        &self,
        ctx: Context,
        nodeid: NodeId,
        _flags: u32,
        handle: HandleId,
    ) -> io::Result<()> {
        // no with_nodeid_refresh: this can't fail with ENOENT
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
        self.with_nodeid_refresh(&ctx, parent, || {
            let name = &name.to_string_lossy();
            let c_path = self.name_to_path(&ctx, parent, name)?;

            // Safe because this doesn't modify any memory and we check the return value.
            let res = unsafe { libc::mkdir(c_path.as_ptr(), (mode & !umask) as u16) };
            if res == 0 {
                // Set security context
                if let Some(secctx) = &extensions.secctx {
                    set_secctx(FileRef::Path(&c_path), secctx, false)?
                };

                set_xattr_stat(
                    FileRef::Path(&c_path),
                    Some((ctx.uid, ctx.gid)),
                    Some(mode & !umask),
                )?;
                self.do_lookup(parent, name, &ctx)
            } else {
                Err(io::Error::last_os_error())
            }
        })
    }

    fn rmdir(&self, ctx: Context, parent: NodeId, name: &CStr) -> io::Result<()> {
        self.with_nodeid_refresh(&ctx, parent, || {
            self.do_unlink(ctx, parent, name, libc::AT_REMOVEDIR)
        })
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
        // no with_nodeid_refresh: we have a handle
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
        // no with_nodeid_refresh: we have a handle
        debug!(
            "readdirplus: nodeid={}, handle={}, size={}, offset={}",
            nodeid, handle, size, offset
        );

        // race OK: FUSE won't FORGET until all handles are closed
        let node = self.nodeids.get(&nodeid).ok_or_else(|| ebadf(nodeid))?;
        node.check_io(&ctx)?;
        let parent_flags = node.flags;
        let nlink = node.nlink;
        let DevIno(dev, ino) = node.dev_ino;
        // TODO: race still OK here because of FORGET, but need to fix
        let parent_fd_path = match node.fd.as_ref() {
            Some(f) => Some(get_path_by_fd(f.as_fd())?),
            None => None,
        };
        let parent_nodeid = node.parent_nodeid.map(|n| n.get()).unwrap_or(u64::MAX);
        drop(node);
        if size == 0 {
            return Ok(());
        }

        let data = self.get_handle(&ctx, nodeid, handle)?;

        // emit "." and ".." first. according to FUSE docs, kernel does this if we don't, but that's not true (and it breaks some apps)
        // skip dirstream lock if only reading . and ..
        if offset == 0 {
            match add_entry(
                DirEntry {
                    ino: nodeid.0,
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
                    ino: parent_nodeid,
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
            lseek(data.file.as_raw_fd(), 0, Whence::SeekSet)?;
            ds.offset = 0;
            ds.entries = None;
        }

        // read entries if not already done
        let entries = if let Some(entries) = ds.entries.as_ref() {
            entries
        } else {
            // reserve # entries = nlink - 2 ("." and "..")
            let capacity = nlink.saturating_sub(2);

            // for NFS loop prevention to work, use legacy impl on home dir
            // getattrlistbulk on home can sometimes stat on mount and cause deadlock
            let use_legacy = if let Some(nfs_info) = self.cfg.nfs_info.as_ref() {
                nfs_info.parent_dir_dev == dev && nfs_info.parent_dir_inode == ino
            } else {
                false
            };

            let entries = if use_legacy {
                attrlist::list_dir_legacy(
                    data.readdir_stream(&mut ds)?.as_ptr(),
                    capacity as usize,
                    |name| {
                        let (_, _, st) = self.begin_lookup(&ctx, nodeid, name)?;
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
                "list_dir: name={} dev={} ino={} offset={}",
                &entry.name,
                st.st_dev,
                st.st_ino,
                offset + 1 + (i as u64)
            );

            let result = if let Some(node) = self.nodeids.get_alt(&st.dev_ino()) {
                // don't return attrs for files with existing nodeids (i.e. inode struct on the linux side)
                // this prevents a race (cargo build [st_size], rm -rf [st_nlink? not sure]) where Linux is writing to a file that's growing in size, and something else calls readdirplus on its parent dir, causing FUSE to update the existing inode's attributes with a stale size, causing the readable portion of the file to be truncated when the next rustc process tries to read from the previous compilation output
                // it's OK for perf, because readdirplus covers two cases: (1) providing attrs to avoid lookup for a newly-seen file, and (2) updating invalidated attrs (>2h timeout, or set in inval_mask) on existing files
                // we only really care about the former case. for the latter case, inval_mask is rarely set in perf-critical contexts, and readdirplus is unlikely to help with the >2h timeout (would the first revalidation call be preceded by readdirplus?)
                // if the 2h-timeout case turns out to be important, we can record last-attr-update timestamp in NodeData and return attrs if expired. races won't happen 2 hours apart

                // this entry does, however, need a valid nodeid so we can fill dt_ino
                Ok((Some(*node.key()), Entry::default()))
            } else if entry.is_mountpoint {
                // mountpoints must be looked up again. getattrlistbulk returns the orig fs mountpoint dir
                self.do_lookup(nodeid, &entry.name, &ctx)
                    .map(|entry| (Some(entry.nodeid), entry))
            } else {
                // only do path lookup once
                let path = if let Some(ref path) = parent_fd_path {
                    CString::new(format!("{}/{}", path, entry.name)).map_err(|e| e.into())
                } else {
                    self.devino_to_path(st.dev_ino())
                };
                let path = match path {
                    Ok(path) => path,
                    Err(e) => {
                        error!("failed to get path: {e}");
                        continue;
                    }
                };

                self.finish_lookup(nodeid, parent_flags, &entry.name, *st, FileRef::Path(&path))
                    .map(|(entry, _)| (Some(entry.nodeid), entry))
            };

            // if lookup failed, return no entry, so linux will get the error on lookup
            let (entry_nodeid, lookup_entry) = result.unwrap_or((None, Entry::default()));
            let new_nodeid = lookup_entry.nodeid;
            let dir_entry = DirEntry {
                // can't be 0
                ino: entry_nodeid.map(|n| n.0).unwrap_or(ino),
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
    ) -> io::Result<(
        Option<HandleId>,
        Option<(bindings::stat64, Duration)>,
        OpenOptions,
    )> {
        self.with_nodeid_refresh(&ctx, nodeid, || self.do_open(&ctx, nodeid, flags))
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
        // no with_nodeid_refresh: this can't fail with ENOENT
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
        self.with_nodeid_refresh(&ctx, parent, || {
            let name = &name.to_string_lossy();
            let (c_path, _, parent_flags) = self.name_to_path_and_data(&ctx, parent, name)?;

            let flags = self.convert_open_flags(flags as i32);

            // Safe because this doesn't modify any memory and we check the return value. We don't
            // really check `flags` because if the kernel can't handle poorly specified flags then we
            // have much bigger problems.
            let fd = unsafe {
                File::from_raw_fd(nix::fcntl::open(
                    c_path.as_ref(),
                    flags | OFlag::O_CREAT,
                    Mode::from_bits_retain(mode as u16),
                )?)
            };

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
            let (entry, node_flags) =
                self.finish_lookup(parent, parent_flags, name, st, FileRef::Fd(fd.as_fd()))?;

            let (handle, opts) = self.finish_open(fd, flags, entry.nodeid, node_flags, st)?;
            Ok((entry, Some(handle), opts))
        })
    }

    fn unlink(&self, ctx: Context, parent: NodeId, name: &CStr) -> io::Result<()> {
        self.with_nodeid_refresh(&ctx, parent, || self.do_unlink(ctx, parent, name, 0))
    }

    fn read<W: io::Write + ZeroCopyWriter>(
        &self,
        ctx: Context,
        nodeid: NodeId,
        handle: HandleId,
        mut w: W,
        size: u32,
        offset: u64,
        _lock_owner: Option<u64>,
        _flags: u32,
    ) -> io::Result<usize> {
        // no with_nodeid_refresh: we have a handle
        let data = self.get_handle(&ctx, nodeid, handle)?;

        // This is safe because write_from uses preadv64, so the underlying file descriptor
        // offset is not affected by this operation.
        debug!("read: {:?}", nodeid);
        w.write_from(&data.file, size as usize, offset)
    }

    fn write<R: io::Read + ZeroCopyReader>(
        &self,
        ctx: Context,
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
        // no with_nodeid_refresh: we have a handle
        let data = self.get_handle(&ctx, nodeid, handle)?;

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

        // this is an unusual call: it can be on a handle OR nodeid
        // if it's on nodeid, we *do* need with_nodeid_refresh
        // if not, we don't
        if handle.is_some() {
            let file_ref = self.get_file_ref(&ctx, nodeid, handle)?;
            self.do_getattr(file_ref.as_ref(), nodeid)
        } else {
            self.with_nodeid_refresh(&ctx, nodeid, || {
                let file_ref = self.get_file_ref(&ctx, nodeid, handle)?;
                self.do_getattr(file_ref.as_ref(), nodeid)
            })
        }
    }

    fn setattr(
        &self,
        ctx: Context,
        nodeid: NodeId,
        attr: bindings::stat64,
        handle: Option<HandleId>,
        valid: SetattrValid,
    ) -> io::Result<(bindings::stat64, Duration)> {
        // this is another unusual mixed handle/nodeid call
        if handle.is_some() {
            self.do_setattr(ctx, nodeid, attr, handle, valid)
        } else {
            self.with_nodeid_refresh(&ctx, nodeid, || {
                self.do_setattr(ctx, nodeid, attr, handle, valid)
            })
        }
    }

    fn rename(
        &self,
        ctx: Context,
        olddir: NodeId,
        oldname: &CStr,
        newdir: NodeId,
        newname: &CStr,
        flags: u32,
    ) -> io::Result<()> {
        // this is ugly: we have two parent nodeids, and either one could be ENOENT
        self.with_nodeid_refresh(&ctx, olddir, || {
            self.with_nodeid_refresh(&ctx, newdir, || {
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

                let newname = newname.to_string_lossy();
                let old_cpath = self.name_to_path(&ctx, olddir, &oldname.to_string_lossy())?;
                let new_cpath = self.name_to_path(&ctx, newdir, &newname)?;

                let mut res =
                    unsafe { libc::renamex_np(old_cpath.as_ptr(), new_cpath.as_ptr(), mflags) };
                // ENOTSUP = not supported by FS (e.g. NFS). retry and simulate if only flag is RENAME_EXCL
                // GNU coreutils 'mv' uses RENAME_EXCL so this is common
                // (hard to simulate RENAME_SWAP)
                if res == -1 && Errno::last() == Errno::ENOTSUP && mflags == libc::RENAME_EXCL {
                    // EXCL means that target must not exist, so check it
                    match access(new_cpath.as_ref(), AccessFlags::F_OK) {
                        Ok(_) => return Err(Errno::EEXIST.into()),
                        Err(Errno::ENOENT) => {}
                        Err(e) => return Err(e.into()),
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
                            set_xattr_stat(
                                FileRef::Fd(fd.as_fd()),
                                None,
                                Some((libc::S_IFCHR | 0o600) as u32),
                            )?;
                        }
                    }

                    // update parent info
                    // TODO: avoid stat
                    let st = lstat(new_cpath.as_ref(), true)?;
                    if let Some(mut node) = self.nodeids.get_alt_mut(&st.dev_ino()) {
                        node.parent_nodeid = newdir.option();
                        node.name = SmolStr::new(newname);
                    }

                    Ok(())
                } else {
                    Err(io::Error::last_os_error())
                }
            })
        })
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
        self.with_nodeid_refresh(&ctx, parent, || {
            debug!(
                "mknod: parent={} name={:?} mode={:x} rdev={} umask={:x}",
                parent, name, mode, rdev, umask
            );

            let name = &name.to_string_lossy();
            let c_path = self.name_to_path(&ctx, parent, name)?;

            // since we run as a normal user, we can't call mknod() to create chr/blk devices
            // TODO: once we support mode overrides, represent them with empty files / sockets
            match mode as u16 & libc::S_IFMT {
                0 | libc::S_IFREG => {
                    // on Linux, mknod can be used to create regular files using fmt = S_IFREG or 0
                    unsafe {
                        OwnedFd::from_raw_fd(open(
                            c_path.as_ref(),
                            // match mknod behavior: EEXIST if already exists
                            OFlag::O_CREAT | OFlag::O_EXCL | OFlag::O_CLOEXEC,
                            // permissions only
                            Mode::from_bits_truncate(mode as u16),
                        )?);
                    }
                }
                libc::S_IFIFO => {
                    // FIFOs are actually safe because Linux just treats them as a device node, and will never issue VFS read call because they can't have data on real filesystems
                    // read/write blocking is all handled by the kernel
                    mkfifo(c_path.as_ref(), Mode::from_bits_truncate(mode as u16))?;
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
                FileRef::Path(&c_path),
                Some((ctx.uid, ctx.gid)),
                Some(mode & !umask),
            )?;

            self.do_lookup(parent, name, &ctx)
        })
    }

    fn link(
        &self,
        ctx: Context,
        nodeid: NodeId,
        newparent: NodeId,
        newname: &CStr,
    ) -> io::Result<Entry> {
        // this is also tricky -- we have two nodeids: one file, one dir
        self.with_nodeid_refresh(&ctx, nodeid, || {
            self.with_nodeid_refresh(&ctx, newparent, || {
                let orig_c_path = self.nodeid_to_path(&ctx, nodeid)?;
                let newname = &newname.to_string_lossy();
                let (link_c_path, _, parent_flags) =
                    self.name_to_path_and_data(&ctx, newparent, newname)?;

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
                            // NOFOLLOW is default; AT_SYMLINK_FOLLOW is opt-in
                            AtFlags::empty(),
                        )?;
                    }
                } else {
                    // only APFS supports clonefile. fall back to link
                    nix::unistd::linkat(
                        None,
                        orig_c_path.as_ref(),
                        None,
                        link_c_path.as_ref(),
                        // NOFOLLOW is default; AT_SYMLINK_FOLLOW is opt-in
                        AtFlags::empty(),
                    )?;
                }

                self.do_lookup(newparent, newname, &ctx)
            })
        })
    }

    fn symlink(
        &self,
        ctx: Context,
        linkname: &CStr,
        parent: NodeId,
        name: &CStr,
        extensions: Extensions,
    ) -> io::Result<Entry> {
        self.with_nodeid_refresh(&ctx, parent, || {
            let name = &name.to_string_lossy();
            let c_path = self.name_to_path(&ctx, parent, name)?;

            // Safe because this doesn't modify any memory and we check the return value.
            symlinkat(linkname, None, c_path.as_ref())?;

            // Set security context
            if let Some(secctx) = &extensions.secctx {
                set_secctx(FileRef::Path(&c_path), secctx, true)?
            };

            let mut entry = self.do_lookup(parent, name, &ctx)?;

            // update xattr stat, and make sure it's reflected by current stat
            let mode = libc::S_IFLNK | 0o777;
            set_xattr_stat(
                FileRef::Path(&c_path),
                Some((ctx.uid, ctx.gid)),
                Some(mode as u32),
            )?;
            entry.attr.st_mode = mode;

            Ok(entry)
        })
    }

    fn readlink(&self, ctx: Context, nodeid: NodeId) -> io::Result<Vec<u8>> {
        self.with_nodeid_refresh(&ctx, nodeid, || {
            let c_path = self.nodeid_to_path(&ctx, nodeid)?;

            let mut buf = vec![0; libc::PATH_MAX as usize];
            let res = unsafe {
                libc::readlink(
                    c_path.as_ptr(),
                    buf.as_mut_ptr() as *mut libc::c_char,
                    buf.len(),
                )
            };
            if res == -1 {
                return Err(io::Error::last_os_error());
            }

            buf.resize(res as usize, 0);
            Ok(buf)
        })
    }

    fn flush(
        &self,
        _ctx: Context,
        _nodeid: NodeId,
        _handle: HandleId,
        _lock_owner: u64,
    ) -> io::Result<()> {
        // no with_nodeid_refresh: this can't fail with ENOENT

        // returning ENOSYS causes no_flush=1 to be set, skipping future calls
        // we could emulate this with dup+close to trigger nfs_vnop_close on NFS,
        // but it's usually ok to just wait for last fd to be closed (i.e. RELEASE)
        // multi-fd is rare anyway
        Err(Errno::ENOSYS.into())
    }

    fn fsync(
        &self,
        ctx: Context,
        nodeid: NodeId,
        _datasync: bool,
        handle: HandleId,
    ) -> io::Result<()> {
        // no with_nodeid_refresh: we have a handle
        let data = self.get_handle(&ctx, nodeid, handle)?;

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
        // no with_nodeid_refresh: we have a handle
        self.fsync(ctx, nodeid, datasync, handle)
    }

    // access not implemented: we use default_permissions

    fn setxattr(
        &self,
        ctx: Context,
        nodeid: NodeId,
        name: &CStr,
        value: &[u8],
        flags: u32,
    ) -> io::Result<()> {
        self.with_nodeid_refresh(&ctx, nodeid, || {
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

            let c_path = self.nodeid_to_path(&ctx, nodeid)?;

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
                Err(io::Error::last_os_error())
            }
        })
    }

    fn getxattr(
        &self,
        ctx: Context,
        nodeid: NodeId,
        name: &CStr,
        size: u32,
    ) -> io::Result<GetxattrReply> {
        self.with_nodeid_refresh(&ctx, nodeid, || {
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

            let c_path = self.nodeid_to_path(&ctx, nodeid)?;

            // Safe because this will only modify the contents of `buf`
            let res = unsafe {
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
            };
            if res == -1 {
                return Err(io::Error::last_os_error());
            }

            if size == 0 {
                Ok(GetxattrReply::Count(res as u32))
            } else {
                buf.resize(res as usize, 0);
                Ok(GetxattrReply::Value(buf))
            }
        })
    }

    fn listxattr(&self, ctx: Context, nodeid: NodeId, size: u32) -> io::Result<ListxattrReply> {
        self.with_nodeid_refresh(&ctx, nodeid, || {
            if !self.cfg.xattr {
                return Err(Errno::ENOSYS.into());
            }

            let c_path = self.nodeid_to_path(&ctx, nodeid)?;

            // Safe because this will only modify the contents of `buf`.
            let buf = listxattr(&c_path)?;

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
                    if attr.is_empty()
                        || attr.starts_with(&STAT_XATTR_KEY[..STAT_XATTR_KEY.len() - 1])
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
        })
    }

    fn removexattr(&self, ctx: Context, nodeid: NodeId, name: &CStr) -> io::Result<()> {
        if !self.cfg.xattr {
            return Err(Errno::ENOSYS.into());
        }

        if name.to_bytes() == STAT_XATTR_KEY {
            return Err(Errno::EACCES.into());
        }

        let c_path = self.nodeid_to_path(&ctx, nodeid)?;

        // Safe because this doesn't modify any memory and we check the return value.
        let res = unsafe { libc::removexattr(c_path.as_ptr(), name.as_ptr(), 0) };

        if res == 0 {
            Ok(())
        } else {
            Err(io::Error::last_os_error())
        }
    }

    fn fallocate(
        &self,
        ctx: Context,
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

        let data = self.get_handle(&ctx, nodeid, handle)?;

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
        ctx: Context,
        nodeid: NodeId,
        handle: HandleId,
        offset: u64,
        whence: u32,
    ) -> io::Result<u64> {
        debug!(
            "lseek: nodeid={} handle={:?} offset={} whence={}",
            nodeid, handle, offset, whence
        );

        let data = self.get_handle(&ctx, nodeid, handle)?;

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
