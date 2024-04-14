// Copyright 2019 The Chromium OS Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

use std::collections::btree_map;
use std::collections::BTreeMap;
use std::ffi::{CStr, CString};
use std::fs::set_permissions;
use std::fs::File;
use std::fs::Permissions;
use std::io;
use std::os::fd::OwnedFd;
use std::os::unix::fs::PermissionsExt;
use std::os::unix::io::{AsRawFd, FromRawFd, RawFd};
use std::path::Path;
use std::ptr::slice_from_raw_parts;
use std::str::FromStr;
use std::sync::atomic::{AtomicBool, AtomicU64, Ordering};
use std::sync::{Arc, Mutex, RwLock};
use std::time::Duration;

use nix::fcntl::OFlag;
use nix::sys::stat::fchmod;
use nix::sys::stat::Mode;
use nix::sys::statvfs::statvfs;
use nix::sys::statvfs::Statvfs;
use nix::unistd::ftruncate;
use nix::NixPath;
use vm_memory::ByteValued;

use crate::virtio::fs::filesystem::SecContext;
use crate::virtio::fs::multikey::MultikeyBTreeMap;
use crate::virtio::linux_errno::nix_linux_error;
use crate::virtio::rosetta::get_rosetta_data;
use crate::virtio::NfsInfo;

use super::super::super::linux_errno::{linux_error, LINUX_ERANGE};
use super::super::bindings;
use super::super::filesystem::{
    Context, DirEntry, Entry, Extensions, FileSystem, FsOptions, GetxattrReply, ListxattrReply,
    OpenOptions, SetattrValid, ZeroCopyReader, ZeroCopyWriter,
};
use super::super::fuse;

// _IOC(_IOC_READ, 0x61, 0x22, 0x45)
const IOCTL_ROSETTA: u32 = 0x8045_6122;

const XATTR_KEY: &[u8] = b"user.orbstack.override_stat\0";

const UID_MAX: u32 = u32::MAX - 1;

// 2 hours
// we invalidate via krpc
const DEFAULT_CACHE_TTL: Duration = Duration::from_secs(2 * 60 * 60);

type NodeId = u64;
type Handle = u64;

struct DirStream {
    stream: u64,
    offset: i64,
}

struct HandleData {
    nodeid: NodeId,
    file: RwLock<File>,
    dirstream: Mutex<DirStream>,
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

#[derive(Clone)]
enum StatFile<'a> {
    Path(&'a CString),
    Fd(RawFd),
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

fn get_xattr_stat(file: StatFile) -> io::Result<Option<(u32, u32, u32)>> {
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
fn set_xattr_stat(file: StatFile, owner: Option<(u32, u32)>, mode: Option<u32>) -> io::Result<()> {
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

fn lstat(c_path: &CString, host: bool) -> io::Result<bindings::stat64> {
    let mut st = nix::sys::stat::lstat(c_path.as_ref()).map_err(nix_linux_error)?;

    if !host {
        if let Some((uid, gid, mode)) = get_xattr_stat(StatFile::Path(c_path))? {
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

fn openat<T: AsRawFd, P: ?Sized + NixPath>(
    fd: T,
    path: &P,
    oflag: OFlag,
    mode: Mode,
) -> io::Result<OwnedFd> {
    let fd = nix::fcntl::openat(fd.as_raw_fd(), path, oflag, mode).map_err(nix_linux_error)?;
    Ok(unsafe { OwnedFd::from_raw_fd(fd) })
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
    refcount: AtomicU64,
}

type DevIno = (i32, u64);

/// A file system that simply "passes through" all requests it receives to the underlying file
/// system. To keep the implementation simple it servers the contents of its root directory. Users
/// that wish to serve only a specific directory should set up the environment so that that
/// directory ends up as the root of the file system process. One way to accomplish this is via a
/// combination of mount namespaces and the pivot_root system call.
pub struct PassthroughFs {
    root_dev_ino: DevIno,

    nodeids: Mutex<MultikeyBTreeMap<NodeId, DevIno, NodeData>>,
    next_nodeid: AtomicU64,

    handles: RwLock<BTreeMap<Handle, Arc<HandleData>>>,
    next_handle: AtomicU64,

    // Whether writeback caching is enabled for this directory. This will only be true when
    // `cfg.writeback` is true and `init` was called with `FsOptions::WRITEBACK_CACHE`.
    writeback: AtomicBool,
    cfg: Config,
}

impl PassthroughFs {
    pub fn new(cfg: Config) -> io::Result<PassthroughFs> {
        let st = nix::sys::stat::stat(Path::new(&cfg.root_dir))?;

        Ok(PassthroughFs {
            root_dev_ino: (st.st_dev, st.st_ino),

            nodeids: Mutex::new(MultikeyBTreeMap::new()),
            next_nodeid: AtomicU64::new(fuse::ROOT_ID + 1),

            handles: RwLock::new(BTreeMap::new()),
            next_handle: AtomicU64::new(1),

            writeback: AtomicBool::new(false),
            cfg,
        })
    }

    fn resolve_nodeid(&self, nodeid: NodeId) -> io::Result<DevIno> {
        if nodeid == fuse::ROOT_ID {
            // we don't keep a refcount for root
            return Ok(self.root_dev_ino);
        }

        let nodeids = self.nodeids.lock().unwrap();
        let node = nodeids.get(&nodeid).ok_or_else(ebadf)?;
        Ok(node.dev_ino)
    }

    fn nodeid_to_path_and_ino(&self, nodeid: NodeId) -> io::Result<(CString, u64)> {
        let (device, inode) = self.resolve_nodeid(nodeid)?;

        let cstr = CString::new(format!("/.vol/{}/{}", device, inode)).map_err(|_| einval())?;
        debug!("nodeid_to_path: {}", cstr.to_string_lossy());
        Ok((cstr, inode))
    }

    fn nodeid_to_path(&self, nodeid: NodeId) -> io::Result<CString> {
        Ok(self.nodeid_to_path_and_ino(nodeid)?.0)
    }

    fn name_to_path(&self, parent: NodeId, name: &CStr) -> io::Result<CString> {
        let (parent_device, parent_inode) = self.resolve_nodeid(parent)?;

        let cstr = CString::new(format!(
            "/.vol/{}/{}/{}",
            parent_device,
            parent_inode,
            name.to_string_lossy()
        ))
        .map_err(|_| einval())?;
        debug!("name_to_path: {}", cstr.to_string_lossy());
        Ok(cstr)
    }

    fn open_nodeid(&self, nodeid: NodeId, mut flags: i32) -> io::Result<File> {
        // When writeback caching is enabled, the kernel may send read requests even if the
        // userspace program opened the file write-only. So we need to ensure that we have opened
        // the file for reading as well as writing.
        let writeback = self.writeback.load(Ordering::Relaxed);
        if writeback && flags & libc::O_ACCMODE == libc::O_WRONLY {
            flags &= !libc::O_ACCMODE;
            flags |= libc::O_RDWR;
        }

        // When writeback caching is enabled the kernel is responsible for handling `O_APPEND`.
        // However, this breaks atomicity as the file may have changed on disk, invalidating the
        // cached copy of the data in the kernel and the offset that the kernel thinks is the end of
        // the file. Just allow this for now as it is the user's responsibility to enable writeback
        // caching only for directories that are not shared. It also means that we need to clear the
        // `O_APPEND` flag.
        if writeback && flags & libc::O_APPEND != 0 {
            flags &= !libc::O_APPEND;
        }

        let (c_path, ino) = self.nodeid_to_path_and_ino(nodeid)?;

        let fd = unsafe {
            libc::open(
                c_path.as_ptr(),
                (flags | libc::O_CLOEXEC) & (!libc::O_NOFOLLOW) & (!libc::O_EXLOCK),
            )
        };
        if fd == -1 {
            // if ino == 2 (standard unix root inode) and errno == ENOENT,
            // it means the FS doesn't support volfs
            let error = io::Error::last_os_error();
            return if ino == 2 && error.kind() == io::ErrorKind::NotFound {
                Err(linux_error(io::Error::from_raw_os_error(libc::EOPNOTSUPP)))
            } else {
                Err(linux_error(error))
            };
        }

        // Safe because we just opened this fd.
        Ok(unsafe { File::from_raw_fd(fd) })
    }

    fn do_lookup(&self, parent: NodeId, name: &CStr, ctx: &Context) -> io::Result<Entry> {
        let mut c_path = self.name_to_path(parent, name)?;
        // looking up nfs mountpoint should return a dummy empty dir
        // for simplicity we can always just use /var/empty
        if let Some(nfs_info) = self.cfg.nfs_info.as_ref() {
            if nfs_info.parent_dir_inode == parent && nfs_info.dir_name == name.to_string_lossy() {
                c_path = CString::new("/var/empty")?;
            }
        }

        let mut st = lstat(&c_path, false)?;
        // TODO: remove on perms
        st.st_uid = ctx.uid;
        st.st_gid = ctx.gid;

        debug!(
            "do_lookup: ino={} path={}",
            st.st_ino,
            c_path.to_str().unwrap()
        );

        let mut nodeids = self.nodeids.lock().unwrap();
        let dev_ino = (st.st_dev, st.st_ino);
        let nodeid = if let Some(node) = nodeids.get_alt(&dev_ino) {
            // there is already a nodeid for this (dev, ino)
            // increment the refcount and return it
            node.refcount.fetch_add(1, Ordering::Relaxed);
            node.nodeid
        } else {
            // this (dev, ino) is new
            // create a new nodeid and return it
            let nodeid = self.next_nodeid.fetch_add(1, Ordering::Relaxed);
            nodeids.insert(
                nodeid,
                dev_ino,
                NodeData {
                    nodeid,
                    dev_ino,
                    refcount: AtomicU64::new(1),
                },
            );
            nodeid
        };

        Ok(Entry {
            nodeid,
            generation: st.st_gen as u64,
            attr: st,
            attr_timeout: self.cfg.attr_timeout,
            entry_timeout: self.cfg.entry_timeout,
        })
    }

    fn do_forget(&self, nodeid: NodeId, count: u64) {
        debug!("do_forget: nodeid={} count={}", nodeid, count);
        let mut nodeids = self.nodeids.lock().unwrap();
        if let Some(node) = nodeids.get(&nodeid) {
            // decrement the refcount
            node.refcount.fetch_sub(count, Ordering::Relaxed);

            if node.refcount.load(Ordering::Relaxed) == 0 {
                // this nodeid is no longer in use
                nodeids.remove(&nodeid);
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
            .read()
            .unwrap()
            .get(&handle)
            .filter(|hd| hd.nodeid == nodeid)
            .map(Arc::clone)
            .ok_or_else(ebadf)?;

        let mut ds = data.dirstream.lock().unwrap();

        let dir_stream = if ds.stream == 0 {
            let dir = unsafe { libc::fdopendir(data.file.write().unwrap().as_raw_fd()) };
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
                if ino == nfs_info.dir_inode {
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
                    warn!(
                        "virtio-fs: error adding entry {}: {:?}",
                        std::str::from_utf8(&name).unwrap(),
                        e
                    );
                    break;
                }
            }
        }

        Ok(())
    }

    fn do_open(&self, nodeid: NodeId, flags: u32) -> io::Result<(Option<Handle>, OpenOptions)> {
        let flags = self.parse_open_flags(flags as i32);

        let file = RwLock::new(self.open_nodeid(nodeid, flags)?);

        let handle = self.next_handle.fetch_add(1, Ordering::Relaxed);
        let data = HandleData {
            nodeid,
            file,
            dirstream: Mutex::new(DirStream {
                stream: 0,
                offset: 0,
            }),
        };

        self.handles.write().unwrap().insert(handle, Arc::new(data));

        let mut opts = OpenOptions::empty();
        match self.cfg.cache_policy {
            // We only set the direct I/O option on files.
            CachePolicy::Never => opts.set(OpenOptions::DIRECT_IO, flags & libc::O_DIRECTORY == 0),
            CachePolicy::Always => {
                if flags & libc::O_DIRECTORY == 0 {
                    opts |= OpenOptions::KEEP_CACHE;
                } else {
                    opts |= OpenOptions::CACHE_DIR;
                }
            }
            _ => {}
        };

        Ok((Some(handle), opts))
    }

    fn do_release(&self, nodeid: NodeId, handle: Handle) -> io::Result<()> {
        let mut handles = self.handles.write().unwrap();

        if let btree_map::Entry::Occupied(e) = handles.entry(handle) {
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
        let c_path = self.nodeid_to_path(parent)?;

        let fd = unsafe { libc::open(c_path.as_ptr(), libc::O_NOFOLLOW | libc::O_CLOEXEC) };
        if fd == -1 {
            return Err(io::Error::last_os_error());
        }

        // Safe because this doesn't modify any memory and we check the return value.
        let res = unsafe { libc::unlinkat(fd, name.as_ptr(), flags) };
        unsafe { libc::close(fd) };

        if res == 0 {
            Ok(())
        } else {
            Err(linux_error(io::Error::last_os_error()))
        }
    }

    fn parse_open_flags(&self, flags: i32) -> i32 {
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

        mflags
    }
}

fn set_secctx(file: StatFile, secctx: SecContext, symlink: bool) -> io::Result<()> {
    let options = if symlink { libc::XATTR_NOFOLLOW } else { 0 };
    let ret = match file {
        StatFile::Path(path) => unsafe {
            libc::setxattr(
                path.as_ptr(),
                secctx.name.as_ptr(),
                secctx.secctx.as_ptr() as *const libc::c_void,
                secctx.secctx.len(),
                0,
                options,
            )
        },
        StatFile::Fd(fd) => unsafe {
            libc::fsetxattr(
                fd,
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

    fn init(&self, capable: FsOptions) -> io::Result<FsOptions> {
        // Safe because this doesn't modify any memory and there is no need to check the return
        // value because this system call always succeeds. We need to clear the umask here because
        // we want the client to be able to set all the bits in the mode.
        unsafe { libc::umask(0o000) };

        let mut opts = FsOptions::empty();
        if self.cfg.writeback && capable.contains(FsOptions::WRITEBACK_CACHE) {
            opts |= FsOptions::WRITEBACK_CACHE;
            self.writeback.store(true, Ordering::Relaxed);
        }
        Ok(opts)
    }

    fn destroy(&self) {
        self.handles.write().unwrap().clear();
    }

    fn statfs(&self, _ctx: Context, nodeid: NodeId) -> io::Result<Statvfs> {
        let c_path = self.nodeid_to_path(nodeid)?;
        statvfs(c_path.as_ref()).map_err(nix_linux_error)
    }

    fn lookup(&self, _ctx: Context, parent: NodeId, name: &CStr) -> io::Result<Entry> {
        debug!("lookup: {:?}", name);
        self.do_lookup(parent, name, &_ctx)
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
        let data = self
            .handles
            .read()
            .unwrap()
            .get(&handle)
            .filter(|hd| hd.nodeid == nodeid)
            .map(Arc::clone)
            .ok_or_else(ebadf)?;

        let ds = data.dirstream.lock().unwrap();
        if ds.stream != 0 {
            unsafe { libc::closedir(ds.stream as *mut libc::DIR) };
        }

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
        let c_path = self.name_to_path(parent, name)?;

        // Safe because this doesn't modify any memory and we check the return value.
        let res = unsafe { libc::mkdir(c_path.as_ptr(), (mode & !umask) as u16) };
        if res == 0 {
            // Set security context
            if let Some(secctx) = extensions.secctx {
                set_secctx(StatFile::Path(&c_path), secctx, false)?
            };

            set_xattr_stat(
                StatFile::Path(&c_path),
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
        _ctx: Context,
        nodeid: NodeId,
        handle: Handle,
        size: u32,
        offset: u64,
        mut add_entry: F,
    ) -> io::Result<()>
    where
        F: FnMut(DirEntry, Entry) -> io::Result<usize>,
    {
        self.do_readdir(nodeid, handle, size, offset, |dir_entry| {
            // Safe because the kernel guarantees that the buffer is nul-terminated. Additionally,
            // the kernel will pad the name with '\0' bytes up to 8-byte alignment and there's no
            // way for us to know exactly how many padding bytes there are. This would cause
            // `CStr::from_bytes_with_nul` to return an error because it would think there are
            // interior '\0' bytes. We trust the kernel to provide us with properly formatted data
            // so we'll just skip the checks here.
            let name = unsafe { CStr::from_bytes_with_nul_unchecked(dir_entry.name) };
            let entry = self.do_lookup(nodeid, name, &_ctx)?;

            add_entry(dir_entry, entry)
        })
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
        let c_path = self.name_to_path(parent, name)?;

        let flags = self.parse_open_flags(flags as i32);

        // Safe because this doesn't modify any memory and we check the return value. We don't
        // really check `flags` because if the kernel can't handle poorly specified flags then we
        // have much bigger problems.
        let fd = unsafe {
            libc::open(
                c_path.as_ptr(),
                flags | libc::O_CREAT | libc::O_CLOEXEC | libc::O_NOFOLLOW,
                mode,
            )
        };
        if fd == -1 {
            return Err(linux_error(io::Error::last_os_error()));
        }

        if let Err(e) = set_xattr_stat(
            StatFile::Fd(fd),
            Some((ctx.uid, ctx.gid)),
            Some(libc::S_IFREG as u32 | (mode & !(umask & 0o777))),
        ) {
            unsafe { libc::close(fd) };
            return Err(e);
        }

        // Set security context
        if let Some(secctx) = extensions.secctx {
            set_secctx(StatFile::Fd(fd), secctx, false)?
        };

        // Safe because we just opened this fd.
        let file = RwLock::new(unsafe { File::from_raw_fd(fd) });

        let entry = self.do_lookup(parent, name, &ctx)?;

        let handle = self.next_handle.fetch_add(1, Ordering::Relaxed);
        let data = HandleData {
            nodeid: entry.nodeid,
            file,
            dirstream: Mutex::new(DirStream {
                stream: 0,
                offset: 0,
            }),
        };

        self.handles.write().unwrap().insert(handle, Arc::new(data));

        let mut opts = OpenOptions::empty();
        match self.cfg.cache_policy {
            CachePolicy::Never => opts |= OpenOptions::DIRECT_IO,
            CachePolicy::Always => opts |= OpenOptions::KEEP_CACHE,
            _ => {}
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
            .read()
            .unwrap()
            .get(&handle)
            .filter(|hd| hd.nodeid == nodeid)
            .map(Arc::clone)
            .ok_or_else(ebadf)?;

        // This is safe because write_from uses preadv64, so the underlying file descriptor
        // offset is not affected by this operation.
        let f = data.file.read().unwrap();
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
            .read()
            .unwrap()
            .get(&handle)
            .filter(|hd| hd.nodeid == nodeid)
            .map(Arc::clone)
            .ok_or_else(ebadf)?;

        // This is safe because read_to uses pwritev64, so the underlying file descriptor
        // offset is not affected by this operation.
        let f = data.file.read().unwrap();
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
            Handle(Arc<HandleData>, RawFd),
            FilePath,
        }

        // If we have a handle then use it otherwise get a new fd from the nodeid.
        let data = if let Some(handle) = handle {
            let hd = self
                .handles
                .read()
                .unwrap()
                .get(&handle)
                .filter(|hd| hd.nodeid == nodeid)
                .map(Arc::clone)
                .ok_or_else(ebadf)?;

            let fd = hd.file.write().unwrap().as_raw_fd();
            Data::Handle(hd, fd)
        } else {
            Data::FilePath
        };

        if valid.contains(SetattrValid::MODE) {
            // TODO: store sticky bit in xattr
            match data {
                Data::Handle(_, fd) => {
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

            set_xattr_stat(StatFile::Path(&c_path), Some((uid, gid)), None)?;
        }

        if valid.contains(SetattrValid::SIZE) {
            // Safe because this doesn't modify any memory and we check the return value.
            match data {
                Data::Handle(_, fd) => ftruncate(fd, attr.st_size),
                _ => {
                    // There is no `ftruncateat` so we need to get a new fd and truncate it.
                    let f = self.open_nodeid(nodeid, libc::O_NONBLOCK | libc::O_RDWR)?;
                    ftruncate(f.as_raw_fd(), attr.st_size)
                }
            }
            .map_err(nix_linux_error)?;
        }

        if valid.intersects(SetattrValid::ATIME | SetattrValid::MTIME) {
            let mut tvs = [
                libc::timespec {
                    tv_sec: 0,
                    tv_nsec: libc::UTIME_OMIT,
                },
                libc::timespec {
                    tv_sec: 0,
                    tv_nsec: libc::UTIME_OMIT,
                },
            ];

            if valid.contains(SetattrValid::ATIME_NOW) {
                tvs[0].tv_nsec = libc::UTIME_NOW;
            } else if valid.contains(SetattrValid::ATIME) {
                tvs[0].tv_sec = attr.st_atime;
                tvs[0].tv_nsec = attr.st_atime_nsec;
            }

            if valid.contains(SetattrValid::MTIME_NOW) {
                tvs[1].tv_nsec = libc::UTIME_NOW;
            } else if valid.contains(SetattrValid::MTIME) {
                tvs[1].tv_sec = attr.st_mtime;
                tvs[1].tv_nsec = attr.st_mtime_nsec;
            }

            // Safe because this doesn't modify any memory and we check the return value.
            let res = match data {
                Data::Handle(_, fd) => unsafe { libc::futimens(fd, tvs.as_ptr()) },
                Data::FilePath => unsafe {
                    let fd = libc::open(c_path.as_ptr(), libc::O_SYMLINK | libc::O_CLOEXEC);
                    let res = libc::futimens(fd, tvs.as_ptr());
                    libc::close(fd);
                    res
                },
            };
            if res == -1 {
                return Err(io::Error::last_os_error());
            }
        }

        self.do_getattr(nodeid, _ctx)
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

        let old_cpath = self.name_to_path(olddir, oldname)?;
        let new_cpath = self.name_to_path(newdir, newname)?;

        let res = unsafe { libc::renamex_np(old_cpath.as_ptr(), new_cpath.as_ptr(), mflags) };
        if res == 0 {
            if ((flags as i32) & bindings::LINUX_RENAME_WHITEOUT) != 0 {
                let fd = unsafe {
                    libc::open(
                        old_cpath.as_ptr(),
                        libc::O_CREAT | libc::O_CLOEXEC | libc::O_NOFOLLOW,
                        0o600,
                    )
                };
                if fd > 0 {
                    if let Err(e) =
                        set_xattr_stat(StatFile::Fd(fd), None, Some((libc::S_IFCHR | 0o600) as u32))
                    {
                        unsafe { libc::close(fd) };
                        return Err(e);
                    }
                    unsafe { libc::close(fd) };
                }
            }

            let entry = self.do_lookup(newdir, newname, &ctx)?;
            self.forget(ctx, entry.nodeid, 1);

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
        let c_path = self.name_to_path(parent, name)?;

        let fd = unsafe {
            libc::open(
                c_path.as_ptr(),
                libc::O_CREAT | libc::O_CLOEXEC | libc::O_NOFOLLOW,
                0o600,
            )
        };
        if fd == -1 {
            Err(linux_error(io::Error::last_os_error()))
        } else {
            // Set security context
            if let Some(secctx) = extensions.secctx {
                set_secctx(StatFile::Fd(fd), secctx, false)?
            };

            if let Err(e) = set_xattr_stat(
                StatFile::Fd(fd),
                Some((ctx.uid, ctx.gid)),
                Some(mode & !umask),
            ) {
                unsafe { libc::close(fd) };
                return Err(e);
            }

            unsafe { libc::close(fd) };
            self.do_lookup(parent, name, &ctx)
        }
    }

    fn link(
        &self,
        _ctx: Context,
        nodeid: NodeId,
        newparent: NodeId,
        newname: &CStr,
    ) -> io::Result<Entry> {
        let orig_c_path = self.nodeid_to_path(nodeid)?;
        let link_c_path = self.name_to_path(newparent, newname)?;

        // Safe because this doesn't modify any memory and we check the return value.
        let res = unsafe { libc::link(orig_c_path.as_ptr(), link_c_path.as_ptr()) };
        if res == 0 {
            self.do_lookup(newparent, newname, &_ctx)
        } else {
            Err(linux_error(io::Error::last_os_error()))
        }
    }

    fn symlink(
        &self,
        ctx: Context,
        linkname: &CStr,
        parent: NodeId,
        name: &CStr,
        extensions: Extensions,
    ) -> io::Result<Entry> {
        let c_path = self.name_to_path(parent, name)?;

        // Safe because this doesn't modify any memory and we check the return value.
        let res = unsafe { libc::symlink(linkname.as_ptr(), c_path.as_ptr()) };
        if res == 0 {
            // Set security context
            if let Some(secctx) = extensions.secctx {
                set_secctx(StatFile::Path(&c_path), secctx, true)?
            };

            let mut entry = self.do_lookup(parent, name, &ctx)?;
            let mode = libc::S_IFLNK | 0o777;
            set_xattr_stat(
                StatFile::Path(&c_path),
                Some((ctx.uid, ctx.gid)),
                Some(mode as u32),
            )?;
            entry.attr.st_uid = ctx.uid;
            entry.attr.st_gid = ctx.gid;
            entry.attr.st_mode = mode;
            Ok(entry)
        } else {
            Err(linux_error(io::Error::last_os_error()))
        }
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
            .read()
            .unwrap()
            .get(&handle)
            .filter(|hd| hd.nodeid == nodeid)
            .map(Arc::clone)
            .ok_or_else(ebadf)?;

        let fd = data.file.write().unwrap().as_raw_fd();

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

        if name.to_bytes() == XATTR_KEY {
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

        if name.to_bytes() == XATTR_KEY {
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
                if attr.starts_with(&XATTR_KEY[..XATTR_KEY.len() - 1]) {
                    clean_size -= XATTR_KEY.len();
                }
            }

            Ok(ListxattrReply::Count(clean_size as u32))
        } else {
            let mut clean_buf = Vec::new();

            for attr in buf.split(|c| *c == 0) {
                if attr.is_empty() || attr.starts_with(&XATTR_KEY[..XATTR_KEY.len() - 1]) {
                    continue;
                }

                clean_buf.extend_from_slice(attr);
                clean_buf.push(0);
            }

            clean_buf.shrink_to_fit();

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

        if name.to_bytes() == XATTR_KEY {
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
            .read()
            .unwrap()
            .get(&handle)
            .filter(|hd| hd.nodeid == nodeid)
            .map(Arc::clone)
            .ok_or_else(ebadf)?;

        let fd = data.file.write().unwrap().as_raw_fd();

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
            .read()
            .unwrap()
            .get(&handle)
            .filter(|hd| hd.nodeid == nodeid)
            .map(Arc::clone)
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

        let fd = data.file.write().unwrap().as_raw_fd();

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
