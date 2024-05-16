// Copyright 2019 The Chromium OS Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

use std::ffi::{CStr, CString, OsStr};
use std::fs::set_permissions;
use std::fs::File;
use std::fs::Permissions;
use std::io;
use std::mem::{self, ManuallyDrop};
use std::os::fd::{AsFd, BorrowedFd, OwnedFd};
use std::os::unix::ffi::OsStrExt;
use std::os::unix::fs::PermissionsExt;
use std::os::unix::io::{AsRawFd, FromRawFd};
use std::os::unix::net::UnixDatagram;
use std::path::Path;
use std::ptr::slice_from_raw_parts;
use std::str::FromStr;
use std::sync::atomic::{AtomicBool, AtomicU64, Ordering};
use std::sync::Arc;
use std::time::Duration;

use bitflags::bitflags;
use libc::{AT_FDCWD, MAXPATHLEN};
use nix::errno::Errno;
use nix::fcntl::OFlag;
use nix::sys::stat::fchmod;
use nix::sys::stat::{futimens, utimensat, Mode, UtimensatFlags};
use nix::sys::statfs::{fstatfs, statfs};
use nix::sys::statvfs::statvfs;
use nix::sys::statvfs::Statvfs;
use nix::sys::time::TimeSpec;
use nix::sys::uio::pwrite;
use nix::unistd::{access, truncate, LinkatFlags};
use nix::unistd::{ftruncate, symlinkat};
use nix::unistd::{mkfifo, AccessFlags};
use smallvec::SmallVec;
use utils::Mutex;
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

// 2 hours - we invalidate via krpc
const DEFAULT_CACHE_TTL: Duration = Duration::from_secs(2 * 60 * 60);

const NSEC_PER_SEC: u64 = 1_000_000_000;
// maxfilesperproc=10240 on 8 GB x86
// must keep our own fd limit to avoid breaking vmgr
const MAX_PATH_FDS: u64 = 8000;

const CLONE_NOFOLLOW: u32 = 0x0001;

const FALLOC_FL_KEEP_SIZE: u32 = 0x01;
const FALLOC_FL_PUNCH_HOLE: u32 = 0x02;
const FALLOC_FL_KEEP_SIZE_AND_PUNCH_HOLE: u32 = FALLOC_FL_KEEP_SIZE | FALLOC_FL_PUNCH_HOLE;

type NodeId = u64;
type HandleId = u64;

struct DirStream {
    stream: *mut libc::DIR,
    offset: i64,
    // OK because this is only for opened files
    entries: Option<SmallVec<[AttrlistEntry; INLINE_ENTRIES]>>,
}

// libc::DIR is Send but not Sync
unsafe impl Send for DirStream {}

struct HandleData {
    nodeid: NodeId,
    file: ManuallyDrop<File>,
    dirstream: Mutex<DirStream>,
}

impl AsFd for HandleData {
    fn as_fd(&self) -> BorrowedFd<'_> {
        self.file.as_fd()
    }
}

impl Drop for HandleData {
    fn drop(&mut self) {
        let ds = self.dirstream.lock().unwrap();
        if !ds.stream.is_null() {
            // this is a dir, and it had a stream open
            // closedir *closes* the fd passed to fdopendir (which is the fd that File holds)
            // so this invalidates the OwnedFd ownership
            unsafe { libc::closedir(ds.stream as *mut libc::DIR) };
        } else {
            // this is a file, or a dir with no stream open
            // manually drop File to close OwnedFd
            unsafe { ManuallyDrop::drop(&mut self.file) };
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

#[derive(Clone, Copy, Debug)]
enum FileRef<'a> {
    Path(&'a CStr),
    Fd(BorrowedFd<'a>),
}

enum OwnedFileRef<F: AsFd> {
    Fd(Arc<F>),
    Path(CString),
}

impl<F: AsFd> OwnedFileRef<F> {
    fn as_ref(&self) -> FileRef {
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
    let mut path_buf = [0u8; MAXPATHLEN as usize];
    // safe: F_GETPATH is limited to MAXPATHLEN
    let ret = unsafe { libc::fcntl(fd.as_raw_fd(), libc::F_GETPATH, &mut path_buf) };
    if ret == -1 {
        return Err(io::Error::last_os_error());
    }

    // cstr to find length
    let cstr = CStr::from_bytes_until_nul(&path_buf).map_err(|_| einval())?;
    // safe: kernel guarantees UTF-8
    Ok(unsafe { String::from_utf8_unchecked(cstr.to_bytes().to_vec()) })
}

fn listxattr(c_path: &CStr) -> nix::Result<Vec<u8>> {
    fn do_listxattr(c_path: &CStr, mut buf: Option<&mut [u8]>) -> nix::Result<usize> {
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
        if ret == -1 {
            return Err(nix::Error::last());
        }

        Ok(ret as usize)
    }

    let mut buf = vec![0u8; 512];
    match do_listxattr(c_path, Some(&mut buf)) {
        Ok(size) => {
            buf.truncate(size);
            Ok(buf)
        }
        Err(Errno::ERANGE) => {
            // get the size we need
            let size = do_listxattr(c_path, None)?;
            let mut buf = vec![0u8; size];
            match do_listxattr(c_path, Some(&mut buf)) {
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
    nodeid: NodeId,
    dev_ino: DevIno,

    // state
    refcount: AtomicU64,
    // for CTO consistency: clear cache on open if ctime has changed
    // must only be updated on open
    last_open_ctime: AtomicU64,

    // cached stat info
    flags: NodeFlags, // for flags propagated to children
    nlink: u16,       // for getattrlistbulk buffer size

    // open fd, if volfs is not supported
    // Arc makes sure this fd won't be closed while a FS call is using it
    // open-fd nodes are the slow case anyway, so this is OK for perf
    fd: Option<Arc<OwnedFd>>,
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

    handles: FxDashMap<HandleId, Arc<HandleData>>,
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
                last_open_ctime: AtomicU64::new(st_ctime(&st)),
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

    fn get_nodeid(&self, nodeid: NodeId) -> io::Result<(DevIno, NodeFlags, Option<Arc<OwnedFd>>)> {
        // race OK: primary key lookup only
        let node = self.nodeids.get(&nodeid).ok_or_else(ebadf)?;
        Ok((node.dev_ino, node.flags, node.fd.clone()))
    }

    // note: /.vol (volfs) is undocumented and deprecated
    // but worst-case scenario: we can use public APIs (fsgetpath) to get the path,
    // and also cache O_EVTONLY fds and paths.
    // lstat realpath=681.85ns, volfs=895.88ns, fsgetpath=1.1478us, lstat+fsgetpath=1.8592us
    // TODO: unify with name_to_path(NodeId, Option<N>)
    fn nodeid_to_file_ref(&self, nodeid: NodeId) -> io::Result<OwnedFileRef<OwnedFd>> {
        let ((dev, ino), _, fd) = self.get_nodeid(nodeid)?;
        if let Some(fd) = fd {
            Ok(OwnedFileRef::Fd(fd))
        } else {
            let path = format!("/.vol/{}/{}", dev, ino);
            let cstr = CString::new(path).map_err(|_| einval())?;
            Ok(OwnedFileRef::Path(cstr))
        }
    }

    fn nodeid_to_path(&self, nodeid: NodeId) -> io::Result<CString> {
        match self.nodeid_to_file_ref(nodeid)? {
            OwnedFileRef::Path(path) => Ok(path),
            OwnedFileRef::Fd(fd) => {
                // to minimize race window and support renames, get latest path from fd
                // this also allows minimal opens (EVTONLY | RDONLY)
                // TODO: all handlers should support Fd or Path. this is just lowest-effort impl
                let path = get_path_by_fd(fd)?;
                let cstr = CString::new(path).map_err(|_| einval())?;
                Ok(cstr)
            }
        }
    }

    fn name_to_path_and_data(
        &self,
        parent: NodeId,
        name: &str,
    ) -> io::Result<(CString, DevIno, NodeFlags)> {
        // deny ".." to prevent root escape
        if name == ".." {
            return Err(linux_error(std::io::Error::from_raw_os_error(libc::ENOENT)));
        }

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
            let mut new_nodeid = self.next_nodeid.fetch_add(1, Ordering::Relaxed);

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
                Some(Arc::new(unsafe { OwnedFd::from_raw_fd(fd) }))
            } else {
                debug!("skip open");
                None
            };

            let mut node = NodeData {
                nodeid: new_nodeid,
                dev_ino,
                refcount: AtomicU64::new(1),
                last_open_ctime: AtomicU64::new(st_ctime(&st)),
                flags: parent_flags,
                fd,
                nlink: st.st_nlink,
            };

            // flag to use clonefile instead of link, for package managers
            if name == LINK_AS_CLONE_DIR_JS || name == LINK_AS_CLONE_DIR_PY {
                node.flags |= NodeFlags::LINK_AS_CLONE;
            }

            // deadlock OK: we're not holding a ref, since lookup returned None
            let inserted_nodeid = self.nodeids.insert(new_nodeid, dev_ino, node);
            if inserted_nodeid != new_nodeid {
                // we raced with another thread, which added a nodeid for this (dev, ino)
                // does the old nodeid still exist?
                let found_existing = if let Some(node) = self.nodeids.get(&inserted_nodeid) {
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

            new_nodeid
        };

        // root generation must be zero
        // for other inodes, we ignore st_gen because getattrlistbulk (readdirplus) doesn't support it, so returning it here would break revalidate
        st.st_gen = 0;

        Ok(Entry {
            nodeid,
            generation: 0,
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

        let data = self
            .handles
            .get(&handle)
            .filter(|hd| hd.nodeid == nodeid)
            .map(|v| v.clone())
            .ok_or_else(ebadf)?;
        // race OK: FUSE won't FORGET until all handles are closed
        let (dev, _) = self.nodeids.get(&nodeid).ok_or_else(ebadf)?.dev_ino;

        let mut ds = data.dirstream.lock().unwrap();

        // dir stream is opened lazily in case client calls opendir() then releasedir() without ever reading entries
        let dir_stream = if ds.stream.is_null() {
            let dir = unsafe { libc::fdopendir(data.file.as_raw_fd()) };
            if dir.is_null() {
                return Err(linux_error(io::Error::last_os_error()));
            }
            ds.stream = dir;
            dir
        } else {
            ds.stream
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

    fn do_open(&self, nodeid: NodeId, flags: u32) -> io::Result<(Option<HandleId>, OpenOptions)> {
        let flags = self.parse_open_flags(flags as i32);

        let file = self.open_nodeid(nodeid, flags)?;
        // early stat to avoid broken handle state if it fails
        let st = fstat(&file, false)?;

        let handle = self.next_handle.fetch_add(1, Ordering::Relaxed);
        let data = HandleData {
            nodeid,
            file: ManuallyDrop::new(file),
            dirstream: Mutex::new(DirStream {
                stream: std::ptr::null_mut(),
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
                    // fstat() is the slow part, so should be faster to release and re-acquire map ref here
                    if let Some(node) = self.nodeids.get(&nodeid) {
                        let ctime = st_ctime(&st);
                        if node.last_open_ctime.swap(ctime, Ordering::Relaxed) == ctime {
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

    fn do_release(&self, nodeid: NodeId, handle: HandleId) -> io::Result<()> {
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

    fn do_getattr(
        &self,
        file_ref: FileRef,
        ctx: Context,
    ) -> io::Result<(bindings::stat64, Duration)> {
        let mut st = match file_ref {
            FileRef::Path(c_path) => lstat(&c_path, false)?,
            FileRef::Fd(fd) => fstat(fd, false)?,
        };

        // TODO: remove on perms
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

    fn get_file_ref(
        &self,
        nodeid: NodeId,
        handle: Option<HandleId>,
    ) -> io::Result<OwnedFileRef<HandleData>> {
        if let Some(handle) = handle {
            let hd = self
                .handles
                .get(&handle)
                .filter(|hd| hd.nodeid == nodeid)
                .map(|v| v.clone())
                .ok_or_else(ebadf)?;

            Ok(OwnedFileRef::Fd(hd))
        } else {
            let path = self.nodeid_to_path(nodeid)?;
            Ok(OwnedFileRef::Path(path))
        }
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
    type Handle = HandleId;

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
        /*
        for handle in self.handles.iter() {
            warn!("leaked handle: nodeid={}", handle.nodeid);
        }
        for node in self.nodeids.iter_main() {
            if node.nodeid == fuse::ROOT_ID {
                continue;
            }

            warn!(
                "leaked node: nodeid={} refcount={}",
                node.nodeid,
                node.refcount.load(Ordering::Relaxed)
            );
        }
        */

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
    ) -> io::Result<(Option<HandleId>, OpenOptions)> {
        self.do_open(nodeid, flags | libc::O_DIRECTORY as u32)
    }

    fn releasedir(
        &self,
        _ctx: Context,
        nodeid: NodeId,
        _flags: u32,
        handle: HandleId,
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
        handle: HandleId,
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
        handle: HandleId,
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

        let mut ds = data.dirstream.lock().unwrap();

        // read entries if not already done
        let entries = if let Some(entries) = ds.entries.as_ref() {
            entries
        } else {
            // reserve # entries = nlink - 2 ("." and "..")
            let capacity = nlink.saturating_sub(2);
            let entries = attrlist::list_dir(data.file.as_fd(), capacity as usize)?;
            ds.entries = Some(entries);
            ds.entries.as_ref().unwrap()
        };

        if offset >= entries.len() as u64 {
            return Ok(());
        }

        for (i, entry) in entries[offset as usize..].iter().enumerate() {
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

            let lookup_entry = if self.nodeids.contains_alt_key(&(st.st_dev, st.st_ino)) {
                // don't return attrs for files with existing nodeids (i.e. inode struct on the linux side)
                // this prevents a race (cargo build [st_size], rm -rf [st_nlink? not sure]) where Linux is writing to a file that's growing in size, and something else calls readdirplus on its parent dir, causing FUSE to update the existing inode's attributes with a stale size, causing the readable portion of the file to be truncated when the next rustc process tries to read from the previous compilation output
                // it's OK for perf, because readdirplus covers two cases: (1) providing attrs to avoid lookup for a newly-seen file, and (2) updating invalidated attrs (>2h timeout, or set in inval_mask) on existing files
                // we only really care about the former case. for the latter case, inval_mask is rarely set in perf-critical contexts, and readdirplus is unlikely to help with the >2h timeout (would the first revalidation call be preceded by readdirplus?)
                // if the 2h-timeout case turns out to be important, we can record last-attr-update timestamp in NodeData and return attrs if expired. races won't happen 2 hours apart
                Ok(Entry::default())
            } else if entry.is_mountpoint {
                // mountpoints must be looked up again. getattrlistbulk returns the orig fs mountpoint dir
                self.do_lookup(nodeid, &entry.name, &ctx)
            } else {
                // only do path lookup once
                let path = if let Some(ref path) = parent_fd_path {
                    CString::new(format!("{}/{}", path, entry.name)).map_err(|_| einval())
                } else {
                    self.devino_to_path((st.st_dev, st.st_ino))
                };
                let path = match path {
                    Ok(path) => path,
                    Err(e) => {
                        error!("failed to get path: {e}");
                        continue;
                    }
                };

                self.finish_lookup(parent_flags, &entry.name, *st, FileRef::Path(&path), &ctx)
            };

            // if lookup failed, return no entry, so linux will get the error on lookup
            let lookup_entry = lookup_entry.unwrap_or_default();

            let dir_entry = DirEntry {
                ino: st.st_ino,
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
    ) -> io::Result<(Option<HandleId>, OpenOptions)> {
        self.do_open(nodeid, flags)
    }

    fn release(
        &self,
        _ctx: Context,
        nodeid: NodeId,
        _flags: u32,
        handle: HandleId,
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
    ) -> io::Result<(Entry, Option<HandleId>, OpenOptions)> {
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
            file: ManuallyDrop::new(File::from(fd)),
            dirstream: Mutex::new(DirStream {
                stream: std::ptr::null_mut(),
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
                    if node.last_open_ctime.swap(ctime, Ordering::Relaxed) == ctime {
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
        handle: HandleId,
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
        let data = self
            .handles
            .get(&handle)
            .filter(|hd| hd.nodeid == nodeid)
            .map(|v| v.clone())
            .ok_or_else(ebadf)?;

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
        _ctx: Context,
        nodeid: NodeId,
        handle: Option<HandleId>,
    ) -> io::Result<(bindings::stat64, Duration)> {
        debug!("getattr: nodeid={} handle={:?}", nodeid, handle);
        let file_ref = self.get_file_ref(nodeid, handle)?;
        self.do_getattr(file_ref.as_ref(), _ctx)
    }

    fn setattr(
        &self,
        _ctx: Context,
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
                std::u32::MAX
            };
            let gid = if valid.contains(SetattrValid::GID) {
                attr.st_gid
            } else {
                // Cannot use -1 here because these are unsigned values.
                std::u32::MAX
            };

            set_xattr_stat(file_ref.as_ref(), Some((uid, gid)), None)?;
        }

        if valid.contains(SetattrValid::SIZE) {
            debug!(
                "ftruncate: nodeid={} handle={:?} size={}",
                nodeid, handle, attr.st_size
            );

            match file_ref.as_ref() {
                FileRef::Fd(fd) => ftruncate(fd.as_raw_fd(), attr.st_size),
                FileRef::Path(path) => truncate(path, attr.st_size),
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
            match file_ref.as_ref() {
                FileRef::Fd(fd) => futimens(fd.as_raw_fd(), &atime, &mtime),
                FileRef::Path(path) => utimensat(
                    None,
                    path.as_ref(),
                    &atime,
                    &mtime,
                    UtimensatFlags::NoFollowSymlink,
                ),
            }
            .map_err(nix_linux_error)?;
        }

        self.do_getattr(file_ref.as_ref(), _ctx)
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
        rdev: u32,
        umask: u32,
        extensions: Extensions,
    ) -> io::Result<Entry> {
        debug!(
            "mknod: parent={} name={:?} mode={:x} rdev={} umask={:x}",
            parent, name, mode, rdev, umask
        );

        let name = &name.to_string_lossy();
        let c_path = self.name_to_path(parent, name)?;

        // since we run as a normal user, we can't call mknod() to create chr/blk devices
        // TODO: once we support mode overrides, represent them with empty files / sockets
        match mode as u16 & libc::S_IFMT {
            libc::S_IFIFO => {
                // FIFOs are actually safe because Linux just treats them as a device node, and will never issue VFS read call because they can't have data on real filesystems
                // read/write blocking is all handled by the kernel
                mkfifo(c_path.as_ref(), Mode::from_bits_truncate(mode as u16))
                    .map_err(nix_linux_error)?;
            }
            libc::S_IFSOCK => {
                // we use datagram because it doesn't call listen
                let _ = UnixDatagram::bind(OsStr::from_bytes(c_path.to_bytes()))
                    .map_err(linux_error)?;
            }
            libc::S_IFCHR | libc::S_IFBLK => {
                return Err(linux_error(io::Error::from_raw_os_error(libc::EPERM)));
            }
            _ => {
                return Err(linux_error(io::Error::from_raw_os_error(libc::EINVAL)));
            }
        }

        // Set security context
        if let Some(secctx) = extensions.secctx {
            set_secctx(FileRef::Path(&c_path), secctx, false)?
        };

        if let Err(e) = set_xattr_stat(
            FileRef::Path(&c_path),
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
        _handle: HandleId,
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
        handle: HandleId,
    ) -> io::Result<()> {
        let data = self
            .handles
            .get(&handle)
            .filter(|hd| hd.nodeid == nodeid)
            .map(|v| v.clone())
            .ok_or_else(ebadf)?;

        // use barrier fsync to preserve semantics and avoid DB corruption
        // Safe because this doesn't modify any memory and we check the return value.
        let res = unsafe { libc::fcntl(data.file.as_raw_fd(), libc::F_BARRIERFSYNC, 0) };

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

        let c_path = self.nodeid_to_path(nodeid)?;

        // Safe because this will only modify the contents of `buf`.
        let buf = listxattr(&c_path).map_err(nix_linux_error)?;

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
        handle: HandleId,
        mode: u32,
        offset: u64,
        length: u64,
    ) -> io::Result<()> {
        debug!(
            "fallocate: nodeid={} handle={:?} mode={} offset={} length={}",
            nodeid, handle, mode, offset, length
        );

        let data = self
            .handles
            .get(&handle)
            .filter(|hd| hd.nodeid == nodeid)
            .map(|v| v.clone())
            .ok_or_else(ebadf)?;

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
                        return Err(linux_error(io::Error::last_os_error()));
                    }
                }

                // only change size if requested, and if new size is *greater*
                if mode & FALLOC_FL_KEEP_SIZE == 0 && new_end > st.st_size as u64 {
                    let res = unsafe { libc::ftruncate(file.as_raw_fd(), new_end as i64) };
                    if res == -1 {
                        return Err(linux_error(io::Error::last_os_error()));
                    }
                }

                Ok(())
            }

            FALLOC_FL_KEEP_SIZE_AND_PUNCH_HOLE => {
                let zero_start = offset as libc::off_t;
                let zero_end = (offset + length) as libc::off_t;

                // macOS requires FS block size alignment
                // Linux zeroes partial blocks
                let st = fstat(file.as_fd(), true)?;
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
                        return Err(linux_error(io::Error::last_os_error()));
                    }
                }

                // zero the starting block
                let zero_start_len = hole_start - zero_start;
                if zero_start_len > 0 {
                    let zero_start_buf = vec![0u8; zero_start_len as usize];
                    pwrite(file.as_raw_fd(), &zero_start_buf, zero_start)
                        .map_err(nix_linux_error)?;
                }

                // zero the ending block
                let zero_end_len = zero_end - hole_end;
                if zero_end_len > 0 {
                    let zero_end_buf = vec![0u8; zero_end_len as usize];
                    pwrite(file.as_raw_fd(), &zero_end_buf, hole_end).map_err(nix_linux_error)?;
                }

                Ok(())
            }

            // don't think it's possible to emulate FALLOC_FL_ZERO_RANGE
            // most programs (e.g. mkfs.ext4) will fall back to FALLOC_FL_PUNCH_HOLE
            _ => Err(linux_error(io::Error::from_raw_os_error(libc::EOPNOTSUPP))),
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

        // Safe because this doesn't modify any memory and we check the return value.
        let res = unsafe {
            libc::lseek(
                data.file.as_raw_fd(),
                offset as bindings::off64_t,
                mwhence as libc::c_int,
            )
        };
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
