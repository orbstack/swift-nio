use std::{ffi::CString, io::{IoSlice, Read, Write}, mem::size_of, os::fd::{AsFd, AsRawFd, FromRawFd, OwnedFd}, sync::Mutex};

use aya::{include_bytes_aligned, programs::Fuse, BpfLoader, VerifierLogLevel};
use fuse_backend_rs::{transport::{Reader, FuseBuf}, abi::fuse_abi::{InHeader, InitIn, InitOut, KERNEL_VERSION, KERNEL_MINOR_VERSION, OutHeader}, api::filesystem::FsOptions};
use nix::{fcntl::{open, openat, OFlag}, sys::{socket::{sendmsg, ControlMessage, MsgFlags}, stat::Mode}};
use tracing::{trace, debug};
use vm_memory::ByteValued;

use crate::{client::generated::{androidfuse, fuse}, newmount::{fsconfig, fsmount, fsopen, move_mount, FSCONFIG_CMD_CREATE, FSCONFIG_SET_FLAG, FSCONFIG_SET_STRING, FSMOUNT_CLOEXEC, FSOPEN_CLOEXEC}};

#[cfg(debug_assertions)]
const VERIFIER_LOG_LEVEL: VerifierLogLevel = VerifierLogLevel::all();
#[cfg(not(debug_assertions))]
const VERIFIER_LOG_LEVEL: VerifierLogLevel = VerifierLogLevel::none();

mod ioctl {
    nix::ioctl_write_int!(fuse_set_mnt, 229, 125);
}

#[derive(Debug)]
pub struct WormholeFs {
    fuse_file: Mutex<std::fs::File>,
    wormhole_dir_fd: OwnedFd,
}

impl WormholeFs {
    pub fn new(backing_root: &str, wormhole_root: &str, fuse_root: Option<&str>) -> anyhow::Result<Self> {
        // open fuse device
        let fuse_fd = open("/dev/fuse", OFlag::O_RDWR | OFlag::O_CLOEXEC, Mode::empty())?;
        let fuse_file = unsafe { std::fs::File::from_raw_fd(fuse_fd) };
    
        // open root dir (mount, not backing)
        let root_fd = open(backing_root, OFlag::O_RDONLY | OFlag::O_DIRECTORY | OFlag::O_CLOEXEC, Mode::empty())?;
    
        // load bpf program
        let mut bpf = BpfLoader::new()
            .verifier_log_level(VERIFIER_LOG_LEVEL)
            .load(include_bytes_aligned!("../../wormholefs_bpf.o"))?;
        let bpf_prog: &mut Fuse = bpf.program_mut("fuse_wormholefs").unwrap().try_into()?;
        bpf_prog.load()?;

        // create detached mount
        let sb_fd = fsopen("fuse", FSOPEN_CLOEXEC)?;
        fsconfig(&sb_fd, FSCONFIG_SET_STRING, Some("source"), Some("wormhole"), 0)?;
        fsconfig(&sb_fd, FSCONFIG_SET_STRING, Some("subtype"), Some("orb.wormhole"), 0)?;
        fsconfig(&sb_fd, FSCONFIG_SET_STRING, Some("fd"), Some(&format!("{}", fuse_file.as_raw_fd())), 0)?;
        fsconfig(&sb_fd, FSCONFIG_SET_STRING, Some("user_id"), Some("0"), 0)?;
        fsconfig(&sb_fd, FSCONFIG_SET_STRING, Some("group_id"), Some("0"), 0)?;
        fsconfig(&sb_fd, FSCONFIG_SET_STRING, Some("rootmode"), Some("0040000"), 0)?;
        fsconfig(&sb_fd, FSCONFIG_SET_STRING, Some("root_dir"), Some(&format!("{}", root_fd)), 0)?;
        fsconfig(&sb_fd, FSCONFIG_SET_STRING, Some("root_bpf"), Some(&format!("{}", bpf_prog.fd()?.as_fd().as_raw_fd())), 0)?;
        fsconfig(&sb_fd, FSCONFIG_SET_FLAG, Some("default_permissions"), None, 0)?;
        fsconfig(&sb_fd, FSCONFIG_SET_FLAG, Some("allow_other"), None, 0)?;
        fsconfig(&sb_fd, FSCONFIG_CMD_CREATE, None, None, 0)?;
        let fuse_mount_fd = fsmount(&sb_fd, FSMOUNT_CLOEXEC, 0)?;

        if let Some(fuse_root) = fuse_root {
            std::fs::create_dir_all(fuse_root)?;
            move_mount(&fuse_mount_fd, None, fuse_root)?;
        } else {
            // send fd via SCM_RIGHTS. stdin should be a unix stream socket
            let msg = 0u64.to_ne_bytes();
            let fds = [fuse_mount_fd.as_raw_fd()];
            let cmsg = ControlMessage::ScmRights(&fds);
            sendmsg::<()>(0, &[IoSlice::new(&msg)], &[cmsg], MsgFlags::empty(), None)?;
        }

        // set canonical_mnt for file handles
        // the ioctl only takes real file/dir fds, not O_PATH (which fsmount returns)
        let fuse_mount_dirfd = unsafe { OwnedFd::from_raw_fd(openat(fuse_mount_fd.as_raw_fd(), ".", OFlag::O_RDONLY | OFlag::O_DIRECTORY | OFlag::O_CLOEXEC, Mode::empty())?) };
        unsafe { ioctl::fuse_set_mnt(fuse_file.as_raw_fd(), fuse_mount_dirfd.as_raw_fd() as u64)? };

        // open wormhole dir
        let wormhole_dir_fd = unsafe { OwnedFd::from_raw_fd(open(wormhole_root, OFlag::O_RDONLY | OFlag::O_DIRECTORY | OFlag::O_CLOEXEC, Mode::empty())?) };

        let client = Self {
            fuse_file: Mutex::new(fuse_file),
            wormhole_dir_fd,
        };

        Ok(client)
    }

    pub fn read_fuse_events(&self) -> anyhow::Result<()> {
        let mut buf = [0u8; 65536];
        let mut fuse_file = self.fuse_file.lock().unwrap();
        loop {
            let n = fuse_file.read(&mut buf)?;
            if n < size_of::<fuse::fuse_in_header>() {
                break;
            }

            let mut reader: Reader<()> = Reader::from_fuse_buffer(FuseBuf::new(&mut buf[..n]))?;
            let in_header: InHeader = reader.read_obj()?;
            trace!("got {} bytes, opcode: {} (no filter = {})", n, in_header.opcode, in_header.opcode & androidfuse::FUSE_OPCODE_FILTER);

            match in_header.opcode {
                x if x == fuse::fuse_opcode_FUSE_INIT => {
                    let init_in: InitIn = reader.read_obj()?;
                    debug!("init: {:?}", init_in);

                    // validate
                    if init_in.major != KERNEL_VERSION || init_in.minor < KERNEL_MINOR_VERSION {
                        panic!("kernel version mismatch: {:?}", init_in);
                    }

                    let options = FsOptions::ASYNC_READ | FsOptions::POSIX_LOCKS | FsOptions::ATOMIC_O_TRUNC | FsOptions::BIG_WRITES | FsOptions::SPLICE_WRITE | FsOptions::SPLICE_MOVE | FsOptions::SPLICE_READ | FsOptions::FLOCK_LOCKS | FsOptions::HAS_IOCTL_DIR | FsOptions::AUTO_INVAL_DATA | FsOptions::DO_READDIRPLUS | FsOptions::READDIRPLUS_AUTO | FsOptions::ASYNC_DIO | FsOptions::PARALLEL_DIROPS | FsOptions::HANDLE_KILLPRIV | FsOptions::POSIX_ACL | FsOptions::ABORT_ERROR | FsOptions::MAX_PAGES | FsOptions::CACHE_SYMLINKS | FsOptions::MAP_ALIGNMENT | FsOptions::INIT_EXT;
                    let flags = options.bits() & (init_in.flags as u64);

                    let init_out = InitOut {
                        major: KERNEL_VERSION,
                        minor: KERNEL_MINOR_VERSION,
                        max_readahead: 4096,
                        flags: flags as u32,
                        max_background: u16::MAX,
                        congestion_threshold: u16::MAX / 4 * 3,
                        max_write: 32768,
                        time_gran: 1, // nanoseconds
                        max_pages: 256,
                        map_alignment: 4096,
                        flags2: (flags >> 32) as u32,
                        ..Default::default()
                    };
                    let out_header = OutHeader {
                        len: size_of::<OutHeader>() as u32 + size_of::<InitOut>() as u32,
                        error: 0,
                        unique: in_header.unique,
                    };
                    
                    fuse_file.write_vectored(&[IoSlice::new(out_header.as_slice()), IoSlice::new(init_out.as_slice())])?;
                },

                x if x == fuse::fuse_opcode_FUSE_LOOKUP => {
                    // read everything:
                    // 1. name + null terminator
                    let name_len = in_header.len - size_of::<fuse::fuse_in_header>() as u32; // incl. null
                    let mut name_buf = vec![0u8; name_len as usize];
                    reader.read_exact(&mut name_buf)?;
                    let name = CString::new(name_buf[..name_buf.len()-1].to_vec())?.into_string()?;
                    trace!("lookup: {:?} | nodeid={} name={}", in_header, in_header.nodeid, name);

                    let febo = androidfuse::fuse_entry_bpf_out {
                        backing_fd: self.wormhole_dir_fd.as_raw_fd() as u64,
                        backing_action: androidfuse::FUSE_ACTION_REPLACE as u64,
                        bpf_fd: 0,
                        bpf_action: androidfuse::FUSE_ACTION_REMOVE as u64,
                    };

                    // we have to fill all of feo, and then add febo to attach backing inode
                    let feo = fuse::fuse_entry_out {
                        // if not 0, kernel will issue FORGET requests
                        nodeid: 0,
                        generation: 0,
                        // cache forever
                        entry_valid: u64::MAX,
                        attr_valid: u64::MAX,
                        entry_valid_nsec: 0,
                        attr_valid_nsec: 0,
                        attr: fuse::fuse_attr {
                            // random hard-coded inode
                            ino: 17938278336429386884,
                            size: 40,
                            blocks: 0,
                            atime: 1,
                            mtime: 1,
                            ctime: 1,
                            atimensec: 0,
                            mtimensec: 0,
                            ctimensec: 0,
                            mode: 0o40755,
                            nlink: 1,
                            uid: 0,
                            gid: 0,
                            rdev: 0,
                            blksize: 4096,
                            flags: 0,
                        },
                    };

                    // reply with all this
                    let out_header = OutHeader {
                        len: size_of::<OutHeader>() as u32 + size_of::<fuse::fuse_entry_out>() as u32 + size_of::<androidfuse::fuse_entry_bpf_out>() as u32,
                        error: 0,
                        unique: in_header.unique,
                    };
                    fuse_file.write_vectored(&[IoSlice::new(out_header.as_slice()), IoSlice::new(feo.as_slice()), IoSlice::new(febo.as_slice())])?;
                }

                x if x == fuse::fuse_opcode_FUSE_INTERRUPT => {
                    debug!("interrupt: {:?}", in_header);

                    // do nothing and return success
                    let out_header = OutHeader {
                        len: size_of::<OutHeader>() as u32,
                        error: 0,
                        unique: in_header.unique,
                    };
                    fuse_file.write(&out_header.as_slice())?;
                },

                _ => {
                    panic!("unknown opcode: {:?}", in_header);
                },
            }
        }

        Ok(())
    }
}
