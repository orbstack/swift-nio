use std::{error::Error, ffi::c_int, io::{Read, Write}, mem::size_of, os::{fd::{AsFd, AsRawFd, FromRawFd, OwnedFd}, raw::c_void, unix::thread::JoinHandleExt}, sync::{atomic::{AtomicU64, Ordering}, Arc}, time::{Duration, Instant}};

use hdrhistogram::Histogram;
use libc::{clockid_t, kevent64_s, pthread_kill, pthread_mach_thread_np, CLOCK_UPTIME_RAW, EVFILT_MACHPORT, EVFILT_USER, EV_ADD, EV_CLEAR, EV_ENABLE, NOTE_FFCOPY, NOTE_FFNOP, NOTE_TRIGGER};
use mach2::{kern_return::{kern_return_t, KERN_ABORTED, KERN_SUCCESS}, mach_port::{mach_port_allocate, mach_port_insert_right}, mach_time::{mach_absolute_time, mach_timebase_info, mach_wait_until}, mach_types::thread_act_t, message::{mach_msg, mach_msg_header_t, MACH_MSGH_BITS, MACH_MSG_TYPE_COPY_SEND, MACH_MSG_TYPE_MAKE_SEND, MACH_RCV_MSG, MACH_RCV_OVERWRITE, MACH_SEND_MSG, MACH_SEND_NO_BUFFER, MACH_SEND_TIMED_OUT, MACH_SEND_TIMEOUT}, port::{mach_port_limits_t, mach_port_t, MACH_PORT_NULL, MACH_PORT_RIGHT_RECEIVE}, traps::mach_task_self, vm_types::natural_t};
use mio::{Events, Poll, Token, Waker};
use nix::errno::Errno;
use tokio::{io::{AsyncReadExt, AsyncWriteExt}, net::unix::pipe::pipe, sync::Notify};

mod park;

const NS: u64 = 1;
const US: u64 = 1000 * NS;
const MS: u64 = 1000 * US;
const SEC: u64 = 1000 * MS;

const DURATION: u64 = 10 * SEC;
const BUCKET_SIZE: usize = 3;

const MACH_PORT_LIMITS_INFO: u32 = 1;
const MACH_PORT_LIMITS_INFO_COUNT: u32 = std::mem::size_of::<mach_port_limits_t>() as u32 / std::mem::size_of::<natural_t>() as u32;

const IDENT_WAKER: u64 = 1;
const KEVENT_FLAG_IMMEDIATE: u32 = 1;

const WAKE_TOKEN: Token = Token(1);

extern "C" {
    fn os_sync_wake_by_address_any(addr: *const c_void, size: usize, flags: u32) -> c_int;
    fn os_sync_wait_on_address(addr: *mut c_void, value: u64, size: usize, flags: u32) -> c_int;
    fn clock_gettime_nsec_np(clock_id: clockid_t) -> u64;

    fn mach_port_set_attributes(task: mach_port_t, name: mach_port_t, flavor: u32, info: *const c_void, count: u32) -> c_int;
}

fn now_ns() -> u64 {
    unsafe { clock_gettime_nsec_np(CLOCK_UPTIME_RAW) }
}

fn nsec_to_mabs(nsec: u64) -> u64 {
    let mut info = mach_timebase_info::default();
    unsafe { mach_timebase_info(&mut info) };
    nsec * info.denom as u64 / info.numer as u64
}

#[derive(Debug, Copy, Clone)]
enum Method {
    Futex,
    Pipe,
    StdThreadPark,
    DispatchSemaphoreParker,
    MioKqueueEvfiltUser,
    PthreadMutexCondvar,
    KqueueEvfiltUser,
    KqueueEvfiltUserSignal,
    MachPort,
    KqueueMachPort,
    MachWaitUntil,
    MachWaitUntilSignal,
}

use std::{sync::{Condvar, Mutex}};

extern "C" {
    fn thread_abort(thread: thread_act_t) -> kern_return_t;
    fn thread_abort_safely(thread: thread_act_t) -> kern_return_t;
}

#[derive(Debug, Default)]
pub struct Parker {
    state: Mutex<bool>,
    condvar: Condvar,
}

impl Parker {
    pub fn park(&self) {
        let mut state = self.state.lock().unwrap();

        while !*state {
            state = self.condvar.wait(state).unwrap();
        }

        *state = false;
    }

    pub fn park_timeout(&self, timeout: Duration) {
        let mut state = self.state.lock().unwrap();
        if !*state {
            state = self.condvar.wait_timeout(state, timeout).unwrap().0;
        }
        *state = false;
    }

    pub fn unpark(&self) {
        *self.state.lock().unwrap() = true;
        self.condvar.notify_all();
    }
}


fn default_kevent() -> kevent64_s {
    kevent64_s {
        ident: 0,
        filter: 0,
        flags: 0,
        fflags: 0,
        data: 0,
        udata: 0,
        ext: [0; 2],
    }
}

struct Kqueue(OwnedFd);

impl Kqueue {
    fn new() -> std::io::Result<Self> {
        let fd = unsafe { libc::kqueue() };
        if fd == -1 {
            return Err(std::io::Error::last_os_error());
        }
        Ok(Kqueue(unsafe { OwnedFd::from_raw_fd(fd) }))
    }

    fn _kevent(
        &self,
        changes: &[kevent64_s],
        events_buf: &mut [kevent64_s],
        flags: u32,
    ) -> nix::Result<usize> {
        let ret = unsafe {
            libc::kevent64(
                self.0.as_raw_fd(),
                changes.as_ptr(),
                changes.len() as libc::c_int,
                events_buf.as_mut_ptr(),
                events_buf.len() as libc::c_int,
                flags,
                std::ptr::null(),
            )
        };
        if ret == -1 {
            return Err(Errno::last());
        }

        Ok(ret as usize)
    }

    fn kevent(
        &self,
        changes: &[kevent64_s],
        events_buf: &mut [kevent64_s],
        flags: u32,
    ) -> nix::Result<usize> {
        loop {
            match self._kevent(changes, events_buf, flags) {
                Ok(n) => return Ok(n),
                Err(Errno::EINTR) => continue,
                Err(e) => return Err(e),
            }
        }
    }
}

fn send_to_mach_port(waker: mach_port_t) {
    let mut msg = mach_msg_header_t {
        msgh_bits: MACH_MSGH_BITS(MACH_MSG_TYPE_COPY_SEND, 0),
        msgh_size: size_of::<mach_msg_header_t>() as u32,
        msgh_remote_port: waker,
        msgh_local_port: MACH_PORT_NULL,
        msgh_voucher_port: 0,
        msgh_id: 0,
    };
    let ret = unsafe { mach_msg(&mut msg, MACH_SEND_MSG | MACH_SEND_TIMEOUT, msg.msgh_size, 0, MACH_PORT_NULL, 0, MACH_PORT_NULL) };
    match ret {
        KERN_SUCCESS => {}
        MACH_SEND_TIMED_OUT => {}
        MACH_SEND_NO_BUFFER => {}
        _ => panic!("mach_msg failed: {}", ret),
    }
}

fn recv_from_mach_port(waker: mach_port_t) {
    let mut msg = mach_msg_header_t::default();
    let ret = unsafe { mach_msg(&mut msg, MACH_RCV_MSG | MACH_RCV_OVERWRITE, 0, size_of::<mach_msg_header_t>() as u32, waker, 0, MACH_PORT_NULL) };
    match ret {
        KERN_SUCCESS => {}
        _ => panic!("mach_msg failed: {}", ret),
    }
}

fn print_histogram(label: &str, histogram: &Histogram<u64>) {
    println!("{}:", label);
    println!("    mean: {:.1} us", histogram.mean() / 1000.0);
    println!("    min: {:.1} us", histogram.min() as f64 / 1000.0);
    println!("    max: {:.1} us", histogram.max() as f64 / 1000.0);
    println!("    p50: {:.1} us", histogram.value_at_quantile(0.5) as f64 / 1000.0);
    println!("    p90: {:.1} us", histogram.value_at_quantile(0.9) as f64 / 1000.0);
    println!("    p99: {:.1} us", histogram.value_at_quantile(0.99) as f64 / 1000.0);
    println!("    p99.9: {:.1} us", histogram.value_at_quantile(0.999) as f64 / 1000.0);
}

fn signal_handler(_: c_int) {
    // do nothing
}

#[inline]
fn test_method(method: Method) -> Result<(), Box<dyn Error>> {
    println!();
    println!();
    println!("=========== {:?} ============", method);

    let (mut rfd, mut wfd) = nix::unistd::pipe()?;
    let mut rfile = unsafe { std::fs::File::from_raw_fd(rfd.as_raw_fd()) };
    let mut wfile = unsafe { std::fs::File::from_raw_fd(wfd.as_raw_fd()) };

    // 0 = abort
    let last_ts = Arc::new(AtomicU64::new(0));
    let last_ts_clone = last_ts.clone();

    let mut poll = Poll::new()?;
    let waker = Arc::new(Waker::new(poll.registry(), WAKE_TOKEN)?);

    let futex_val = Box::into_raw(Box::new(0u64));
    let futex_val_raw = futex_val as u64;

    let pthread_parker = Arc::new(Parker::default());

    let mut mach_port: mach_port_t = 0;
    let ret = unsafe { mach_port_allocate(mach_task_self(), MACH_PORT_RIGHT_RECEIVE, &mut mach_port) };
    if ret != 0 {
        panic!("mach_port_allocate failed: {}", ret);
    }
    let ret = unsafe { mach_port_insert_right(mach_task_self(), mach_port, mach_port, MACH_MSG_TYPE_MAKE_SEND) };
    if ret != 0 {
        panic!("mach_port_insert_right failed: {}", ret);
    }
    let limits = mach_port_limits_t {
        mpl_qlimit: 1,
    };
    let ret = unsafe { mach_port_set_attributes(mach_task_self(), mach_port, MACH_PORT_LIMITS_INFO, &limits as *const _ as *const c_void, MACH_PORT_LIMITS_INFO_COUNT) };
    if ret != 0 {
        panic!("mach_port_set_attributes failed: {}", ret);
    }

    let kq = Arc::new(Kqueue::new()?);

    // register waker
    kq.kevent(&[kevent64_s {
        ident: IDENT_WAKER,
        filter: EVFILT_USER,
        flags: EV_ADD | EV_CLEAR,
        fflags: 0,
        ..default_kevent()
    }], &mut [], KEVENT_FLAG_IMMEDIATE)?;

    // register mach port
    let mut machport_buf = [0u8; 1024];
    kq.kevent(&[kevent64_s {
        ident: mach_port as u64,
        filter: EVFILT_MACHPORT,
        flags: EV_ADD | EV_ENABLE,
        fflags: MACH_RCV_MSG as u32 | MACH_RCV_OVERWRITE as u32,
        ext: [machport_buf.as_mut_ptr() as u64, machport_buf.len() as u64],
        ..default_kevent()
    }], &mut [], KEVENT_FLAG_IMMEDIATE)?;

    // set SIGURG handler
    let sigurg = libc::SIGURG;
    let sa = libc::sigaction {
        sa_sigaction: signal_handler as usize,
        sa_mask: 0,
        sa_flags: 0,
    };
    let ret = unsafe { libc::sigaction(sigurg, &sa, std::ptr::null_mut()) };
    if ret != 0 {
        panic!("sigaction failed: {}", ret);
    }

    let parker = Arc::new(park::Parker::default());

    // spawn reader
    let parker_clone = parker.clone();
    let pthread_parker_clone = pthread_parker.clone();
    let kq_clone = kq.clone();
    let handle = std::thread::spawn(move || {
        let futex_val = futex_val_raw as *mut u64;
        let last_ts = last_ts_clone;

        // in usec
        let mut histogram = Histogram::<u64>::new_with_bounds(1, u64::MAX, 3).unwrap();

        let mut buf = [0u8; 8];
        let mut events = Events::with_capacity(2);
        let mut events_buf = [default_kevent(), default_kevent()];
        loop {
            match method {
                Method::Futex => {
                    unsafe {
                        os_sync_wait_on_address(futex_val as *mut c_void, 0, 8, 0);
                    }
                }
                Method::Pipe => {
                    let n = rfile.read(&mut buf).unwrap();
                    if n == 0 {
                        break;
                    }
                }
                Method::StdThreadPark => {
                    std::thread::park();
                }
                Method::DispatchSemaphoreParker => {
                    parker_clone.park();
                }
                Method::MioKqueueEvfiltUser => {
                    poll.poll(&mut events, None).unwrap();
                    let _ = events.iter().next().unwrap();
                }
                Method::PthreadMutexCondvar => {
                    pthread_parker_clone.park();
                }
                Method::KqueueEvfiltUser | Method::KqueueMachPort => {
                    kq_clone.kevent(&[], &mut events_buf, 0).unwrap();
                }
                Method::KqueueEvfiltUserSignal => {
                    match kq_clone._kevent(&[], &mut events_buf, 0) {
                        Ok(_) => {}
                        Err(Errno::EINTR) => {}
                        Err(e) => panic!("kevent failed: {}", e),
                    }
                }
                Method::MachPort => {
                    recv_from_mach_port(mach_port);
                }
                Method::MachWaitUntil | Method::MachWaitUntilSignal => {
                    let ret = unsafe { mach_wait_until(u64::MAX) };
                    if ret != 0 && ret != KERN_ABORTED {
                        panic!("mach_wait_until failed: {}", ret);
                    }
                }
            }
            let send_ts = last_ts.swap(1, Ordering::Relaxed);
            if send_ts == 0 {
                break;
            }
            if send_ts == 1 {
                // spurious wakeup
                continue;
            }
            let recv_ts = now_ns();
            let latency = (recv_ts - send_ts);
            histogram.record(latency).unwrap();
        }

        print_histogram("WAKEE", &histogram);
    });

    let thread = handle.thread();
    let pthread = handle.as_pthread_t();
    let mach_thread = unsafe { pthread_mach_thread_np(pthread) };
    if mach_thread == MACH_PORT_NULL {
        panic!("pthread_mach_thread_np failed");
    }
    let mut do_wake = || {
        match method {
            Method::Futex => {
                unsafe {
                    os_sync_wake_by_address_any(futex_val as *const c_void, 8, 0);
                }
            }
            Method::Pipe => {
                let _ = wfile.write(&1u64.to_le_bytes()).unwrap();
            }
            Method::StdThreadPark => {
                thread.unpark();
            }
            Method::DispatchSemaphoreParker => {
                parker.unpark();
            }
            Method::MioKqueueEvfiltUser => {
                waker.wake().unwrap();
            }
            Method::PthreadMutexCondvar => {
                pthread_parker.unpark();
            }
            Method::KqueueEvfiltUser => {
                kq.kevent(&[kevent64_s {
                    ident: IDENT_WAKER,
                    filter: EVFILT_USER,
                    flags: EV_ENABLE,
                    fflags: NOTE_FFNOP | NOTE_TRIGGER,
                    ..default_kevent()
                }], &mut [], KEVENT_FLAG_IMMEDIATE).unwrap();
            }
            Method::KqueueMachPort | Method::MachPort => {
                send_to_mach_port(mach_port);
            }
            Method::MachWaitUntil => {
                let ret = unsafe { thread_abort(mach_thread) };
                if ret != 0 {
                    panic!("thread_abort failed: {}", ret);
                }
            }
            Method::KqueueEvfiltUserSignal | Method::MachWaitUntilSignal => {
                let ret = unsafe { pthread_kill(pthread, libc::SIGURG) };
                if ret != 0 {
                    panic!("pthread_kill failed: {}", ret);
                }
            }
        }
    };
    let start_ts = now_ns();
    let mut waker_histogram = Histogram::<u64>::new_with_bounds(1, u64::MAX, 3).unwrap();
    loop {
        let send_ts = now_ns();
        if send_ts - start_ts > DURATION {
            break;
        }
        last_ts.store(send_ts, Ordering::Relaxed);
        let before_wake = now_ns();
        do_wake();
        let after_wake = now_ns();
        waker_histogram.record(after_wake - before_wake).unwrap();

        // avoid cluttering spindump with nanosleep / semaphore overhead
        // std::thread::sleep(Duration::from_millis(1));
        let deadline = unsafe { mach_absolute_time() } + nsec_to_mabs(Duration::from_millis(1).as_nanos() as u64);
        let ret = unsafe { mach_wait_until(deadline) };
        if ret != 0 {
            panic!("mach_wait_until failed: {}", ret);
        }
    }

    // stop
    last_ts.store(0, Ordering::Relaxed);
    do_wake();
    handle.join().unwrap();

    print_histogram("WAKER", &waker_histogram);

    Ok(())
}


fn main() -> Result<(), Box<dyn Error>> {
    // test_method(Method::Futex)?;
    // test_method(Method::Pipe)?;
    test_method(Method::StdThreadPark)?;
    test_method(Method::DispatchSemaphoreParker)?;
    // test_method(Method::PthreadMutexCondvar)?;
    // test_method(Method::MioKqueueEvfiltUser)?;
    // test_method(Method::KqueueEvfiltUser)?;
    // test_method(Method::KqueueEvfiltUserSignal)?;
    // test_method(Method::KqueueMachPort)?;
    // test_method(Method::MachWaitUntil)?;
    // test_method(Method::MachWaitUntilSignal)?;

    // broken
    // test_method(Method::MachPort)?;

    Ok(())
}
