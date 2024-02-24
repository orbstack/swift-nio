use std::{error::Error, ffi::CString, io::{IoSlice, Read, Write}, mem::size_of, os::fd::{AsFd, AsRawFd, FromRawFd, OwnedFd}, sync::Mutex};

use aya::{programs::Fuse, BpfLoader, VerifierLogLevel};
use fuse_backend_rs::{transport::{Reader, FuseBuf}, abi::fuse_abi::{InHeader, InitIn, InitOut, KERNEL_VERSION, KERNEL_MINOR_VERSION, OutHeader}, api::filesystem::FsOptions};
use nix::{fcntl::{open, OFlag}, mount::{mount, MsFlags}, sys::stat::Mode};
use tracing::{trace, debug, trace_span};
use vm_memory::ByteValued;

use crate::client::generated::{androidfuse, fuse};

mod ioctl {
    nix::ioctl_write_int!(fuse_set_mnt, 229, 125);
}

#[derive(Debug)]
pub struct WormholeFs {
    fuse_file: Mutex<std::fs::File>,
    wormhole_dir_fd: OwnedFd,
}

impl WormholeFs {
    pub fn new(backing_root: &str, wormhole_root: &str, fuse_root: &str) -> Result<Self, Box<dyn Error>> {
        // open fuse device
        let fuse_fd = open("/dev/fuse", OFlag::O_RDWR | OFlag::O_CLOEXEC, Mode::empty())?;
        let fuse_file = unsafe { std::fs::File::from_raw_fd(fuse_fd) };
    
        // open root dir (mount, not backing)
        std::fs::create_dir_all(fuse_root)?;
        let root_fd = open(backing_root, OFlag::O_RDONLY | OFlag::O_DIRECTORY | OFlag::O_CLOEXEC, Mode::empty())?;
    
        // load bpf program
        let mut bpf = BpfLoader::new()
            .verifier_log_level(VerifierLogLevel::all())
            .load_file("wormholefs_bpf.o")?;
        let bpf_prog: &mut Fuse = bpf.program_mut("fuse_wormholefs").unwrap().try_into()?;
        bpf_prog.load()?;
        let options = format!("fd={},user_id=0,group_id=0,rootmode=0040000,root_dir={},root_bpf={},default_permissions,allow_other", fuse_file.as_raw_fd(), root_fd, bpf_prog.fd()?.as_fd().as_raw_fd());

        // mount
        mount(Some("wormhole"), fuse_root, Some("fuse.orb.wormhole"), MsFlags::empty(), Some(options.as_str()))?;
    
        let fuse_root_fd = unsafe { OwnedFd::from_raw_fd(open(fuse_root, OFlag::O_RDONLY | OFlag::O_DIRECTORY | OFlag::O_CLOEXEC, Mode::empty())?) };
        // set canonical_mnt
        unsafe { ioctl::fuse_set_mnt(fuse_file.as_raw_fd(), fuse_root_fd.as_raw_fd() as u64)? };

        // open wormhole dir
        let wormhole_dir_fd = unsafe { OwnedFd::from_raw_fd(open(wormhole_root, OFlag::O_RDONLY | OFlag::O_DIRECTORY | OFlag::O_CLOEXEC, Mode::empty())?) };

        let client = Self {
            fuse_file: Mutex::new(fuse_file),
            wormhole_dir_fd,
        };

        Ok(client)
    }

    pub fn read_fuse_events(&self) -> Result<(), Box<dyn Error>> {
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
                    let _span = trace_span!("init").entered();
            
                    let init_in: InitIn = reader.read_obj()?;
                    debug!("init: {:?}", init_in);

                    // validate
                    if init_in.major != KERNEL_VERSION || init_in.minor < KERNEL_MINOR_VERSION {
                        panic!("kernel version mismatch");
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
                    let _span = trace_span!("lookup-post").entered();
            
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
                    panic!("unknown opcode");
                },
            }
        }

        Ok(())
    }
}
