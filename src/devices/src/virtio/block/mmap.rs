use std::{
    fs::File,
    io,
    ops::Range,
    os::fd::AsRawFd,
    sync::{
        atomic::{AtomicU64, Ordering},
        Arc, OnceLock,
    },
};

use anyhow::anyhow;
use arc_swap::ArcSwap;
use libc::{pthread_sigmask, raise, sigaction, sigaddset, sigemptyset, STDOUT_FILENO};
use mach2::{
    kern_return::KERN_SUCCESS,
    traps::mach_task_self,
    vm::mach_vm_map,
    vm_inherit::VM_INHERIT_NONE,
    vm_prot::VM_PROT_NONE,
    vm_statistics::VM_FLAGS_ANYWHERE,
    vm_types::{mach_vm_address_t, mach_vm_size_t},
};
use nix::{errno::Errno, unistd::write};
use utils::Mutex;

use crate::virtio::descriptor_utils::Iovec;

static MMAP_RANGES: RangeRegistry = RangeRegistry {
    inner: OnceLock::new(),
    write_lock: Mutex::new(()),
    old_action: OnceLock::new(),
};

// 3 write calls to avoid malloc: prefix, image, suffix
const SIGBUS_PREFIX: &[u8] = b"\n\nI/O error reading from disk image '";
const SIGBUS_SUFFIX: &[u8] =
    b"': Input/output error.\nIf you're using external storage, make sure the connection is reliable.\n";

// Swift recognizes this error code
// sync with vmgr/types/types.go
const EXIT_CODE_IO_ERROR: i32 = 100 + (9 - 4);

pub const CHUNK_SIZE: usize = 16 * 1024 * 1024 * 1024; // GiB

const VM_FLAGS_4GB_CHUNK: i32 = 4;

type SigactionHandler =
    unsafe extern "C" fn(libc::c_int, *const libc::siginfo_t, *const libc::ucontext_t);

#[derive(Debug, Clone, PartialEq, Eq)]
struct MmapRange {
    range: Range<usize>,
    file_path: String,
}

impl MmapRange {
    pub fn new(start: usize, len: usize, file_path: String) -> Self {
        Self {
            range: start..(start + len),
            file_path,
        }
    }

    fn contains(&self, addr: &usize) -> bool {
        self.range.contains(addr)
    }
}

struct RangeRegistry {
    // must not malloc/free/lock due to async signal safety
    inner: OnceLock<ArcSwap<Vec<MmapRange>>>,
    // mutator lock -- copy+add/remove+swap isn't atomic
    write_lock: Mutex<()>,

    // old signal handler
    old_action: OnceLock<sigaction>,
}

impl RangeRegistry {
    fn get_or_init(&self) -> &ArcSwap<Vec<MmapRange>> {
        self.inner.get_or_init(|| {
            Self::install_signal_handler().unwrap();
            ArcSwap::new(Arc::new(Vec::new()))
        })
    }

    pub fn add(&self, range: MmapRange) {
        let _guard = self.write_lock.lock();
        let ranges = self.get_or_init().load();
        let mut new_ranges = (**ranges).clone();
        new_ranges.push(range);
        self.get_or_init().store(Arc::new(new_ranges));
    }

    pub fn remove(&self, range: Range<usize>) {
        let _guard = self.write_lock.lock();
        let ranges = self.get_or_init().load();
        let mut new_ranges = (**ranges).clone();
        new_ranges.retain(|r| r.range != range);
        self.get_or_init().store(Arc::new(new_ranges));
    }

    fn install_signal_handler() -> anyhow::Result<()> {
        // make sure sigaltstack was set up by either Go or Rust
        let mut stack: libc::stack_t = unsafe { std::mem::zeroed() };
        let ret = unsafe { libc::sigaltstack(std::ptr::null(), &mut stack) };
        Errno::result(ret)?;
        if stack.ss_flags & libc::SS_DISABLE != 0 {
            return Err(anyhow!("no sigaltstack"));
        }

        // fetch old signal handler first
        let mut old_action: sigaction = unsafe { std::mem::zeroed() };
        let ret = unsafe { sigaction(libc::SIGBUS, std::ptr::null(), &mut old_action) };
        Errno::result(ret)?;

        // we can only forward to old handlers that use signal stack
        if !matches!(old_action.sa_sigaction, libc::SIG_DFL | libc::SIG_IGN)
            && old_action.sa_flags & libc::SA_ONSTACK == 0
        {
            return Err(anyhow!("old handler doesn't use signal stack"));
        }

        // save old handler first to prevent race
        MMAP_RANGES
            .old_action
            .set(old_action)
            .map_err(|_| anyhow!("old handler already set"))?;

        // install new signal handler
        let new_action = sigaction {
            sa_sigaction: Self::signal_handler as usize,
            // Go requires SA_ONSTACK
            // SA_RESTART makes little sense for SIGBUS, but doesn't hurt to have
            // no SA_NODEFER: SIGBUS in the SIGBUS handler is definitely bad, so just crash
            // can't use signal_hook: it doesn't set SA_ONSTACK
            sa_flags: libc::SA_ONSTACK | libc::SA_SIGINFO | libc::SA_RESTART,
            // copy mask from old handler
            sa_mask: old_action.sa_mask,
        };
        let ret = unsafe { sigaction(libc::SIGBUS, &new_action, std::ptr::null_mut()) };
        Errno::result(ret)?;

        Ok(())
    }

    // signals are awful, but Mach exception ports aren't much better..
    // async signal safety mostly still applies to in-process exception port handlers,
    // and it'd have to save and forward to default ux_handler port
    unsafe extern "C" fn signal_handler(
        signum: libc::c_int,
        info: *const libc::siginfo_t,
        uap: *const libc::ucontext_t,
    ) {
        // TODO: unlikely, but this could call free if it got the last ref...
        if let Some(ranges) = MMAP_RANGES.inner.get().map(|r| r.load()) {
            let addr = (*info).si_addr as usize;
            if let Some(range) = ranges.iter().find(|r| r.contains(&addr)) {
                // address is in mmap range
                // print error message and exit
                _ = write(STDOUT_FILENO, SIGBUS_PREFIX);
                _ = write(STDOUT_FILENO, range.file_path.as_bytes());
                _ = write(STDOUT_FILENO, SIGBUS_SUFFIX);

                // async-signal-safe variant that doesn't run atexit handlers
                libc::_exit(EXIT_CODE_IO_ERROR);
            }
        }

        // malloc-safe: this will never see an in-progress set
        if let Some(old) = MMAP_RANGES.old_action.get() {
            // not in mmap range
            // forward to existing handler
            // TODO: refactor into generic signal forwarding impl
            match old.sa_sigaction {
                libc::SIG_DFL => {
                    // default handler: terminate, but forward to OS to get correct exit status

                    // uninstall our signal handler
                    // TODO: this is wrong if signum's default sigaction != SIG_DFL. our handler won't run again. doesn't matter for SIGBUS
                    let new_action = sigaction {
                        sa_sigaction: libc::SIG_DFL,
                        sa_flags: libc::SA_RESTART,
                        sa_mask: old.sa_mask,
                    };
                    sigaction(signum, &new_action, std::ptr::null_mut());

                    // unmask the signal
                    let mut mask: libc::sigset_t = std::mem::zeroed();
                    sigemptyset(&mut mask);
                    sigaddset(&mut mask, signum);
                    pthread_sigmask(libc::SIG_UNBLOCK, &mask, std::ptr::null_mut());

                    // re-raise signal
                    raise(signum);
                }

                libc::SIG_IGN => {
                    // ignore: do nothing
                }

                _ => {
                    // call old handler
                    // have to use transmute to cast
                    let old_handler =
                        std::mem::transmute::<usize, SigactionHandler>(old.sa_sigaction);
                    // TODO: this could overflow the signal stack if it's doesn't get optimized to a tail call. not setting SA_NODEFER makes it very unlikely, but it's still possible
                    old_handler(signum, info, uap);
                }
            }
        }
    }
}

struct AtomicBitmap(Vec<AtomicU64>);

impl AtomicBitmap {
    pub fn new(num_bits: usize) -> Self {
        let num_chunks = num_bits.div_ceil(64);
        let mut v = Vec::with_capacity(num_chunks);
        v.resize_with(num_chunks, || AtomicU64::new(0));
        AtomicBitmap(v)
    }

    pub fn test(&self, bit: usize) -> bool {
        let chunk = bit / 64;
        let offset = bit % 64;
        self.0[chunk].load(Ordering::Relaxed) & (1 << offset) != 0
    }

    pub fn set(&self, bit: usize) {
        let chunk = bit / 64;
        let offset = bit % 64;
        self.0[chunk].fetch_or(1 << offset, Ordering::Relaxed);
    }
}

pub struct MappedFile {
    file: File,
    base_addr: *const u8,
    size: usize,
    mapped_bitmap: AtomicBitmap,
}

unsafe impl Send for MappedFile {}
unsafe impl Sync for MappedFile {}

impl MappedFile {
    pub fn new(file: File, size: usize, file_path: String) -> io::Result<Self> {
        // reserve contiguous address space for performance and to allow a compact bitmap
        // use 4G chunks to minimize regions
        let mut base_addr: mach_vm_address_t = 0;
        let ret = unsafe {
            mach_vm_map(
                mach_task_self(),
                &mut base_addr,
                size as mach_vm_size_t,
                0,
                VM_FLAGS_4GB_CHUNK | VM_FLAGS_ANYWHERE,
                0,
                0,
                0,
                // this mapping should never actually be used
                VM_PROT_NONE,
                VM_PROT_NONE,
                VM_INHERIT_NONE,
            )
        };
        if ret != KERN_SUCCESS {
            return Err(io::Error::new(
                io::ErrorKind::Other,
                format!("mach error: {}", ret),
            ));
        }

        // address space reserved. register signal handler
        MMAP_RANGES.add(MmapRange::new(base_addr as usize, size, file_path));

        let num_chunks = size.div_ceil(CHUNK_SIZE);
        Ok(MappedFile {
            file,
            base_addr: base_addr as *const u8,
            size,
            mapped_bitmap: AtomicBitmap::new(num_chunks),
        })
    }

    pub fn file(&self) -> &File {
        &self.file
    }

    pub fn read_to_iovec(&self, off: usize, iov: &Iovec) -> io::Result<usize> {
        // bounds check
        let len = iov.len();
        let src = self.get_host_addr(off, len)?;
        unsafe { std::ptr::copy_nonoverlapping(src, iov.addr_mut(), len) }
        Ok(len)
    }

    pub fn get_host_addr(&self, off: usize, len: usize) -> io::Result<*const u8> {
        // make sure all chunks included in this are mapped
        self.ensure_mapped(off, len)?;
        Ok(unsafe { self.base_addr.add(off) })
    }

    pub fn ensure_mapped(&self, off: usize, len: usize) -> io::Result<()> {
        // len=0 would cause the -1 below to overflow
        if len == 0 {
            return Ok(());
        }

        let end_off = off.saturating_add(len) - 1;
        if end_off > self.size {
            return Err(io::Error::new(
                io::ErrorKind::InvalidInput,
                "read out of bounds",
            ));
        }

        let start_chunk = off / CHUNK_SIZE;
        let end_chunk = end_off / CHUNK_SIZE;

        for chunk in start_chunk..=end_chunk {
            // race OK, no need for synchronization:
            // mapping the same file+offset to the same address doesn't change anything
            if !self.mapped_bitmap.test(chunk) {
                let file_off = chunk * CHUNK_SIZE;
                let map_size = CHUNK_SIZE.min(self.size - file_off);
                let map_addr = unsafe { self.base_addr.add(chunk * CHUNK_SIZE) };
                let addr = unsafe {
                    libc::mmap(
                        map_addr as *mut _,
                        map_size,
                        libc::PROT_READ,
                        libc::MAP_FILE | libc::MAP_FIXED | libc::MAP_SHARED,
                        self.file.as_raw_fd(),
                        file_off as libc::off_t,
                    )
                };
                if addr == libc::MAP_FAILED {
                    return Err(io::Error::last_os_error());
                }

                self.mapped_bitmap.set(chunk);
            }
        }

        Ok(())
    }
}

impl Drop for MappedFile {
    fn drop(&mut self) {
        unsafe {
            libc::munmap(self.base_addr as *mut libc::c_void, self.size);
        }

        MMAP_RANGES.remove(self.base_addr as usize..(self.base_addr as usize + self.size));
    }
}
