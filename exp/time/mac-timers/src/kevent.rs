use std::{
    io,
    marker::PhantomData,
    mem::MaybeUninit,
    ptr, slice,
    sync::atomic::{AtomicU64, AtomicUsize, Ordering::*},
};

use libc::kevent64;
use smallvec::SmallVec;

const NOTE_MACHTIME: u32 = 0x00000100;
const KEVENT_FLAG_IMMEDIATE: u32 = 0x000001;

pub const CLOCK_EV_CAP: usize = 4;

/*
int
     kevent_qos(int kq, const struct kevent_qos_s *changelist, int nchanges,
         struct kevent_qos_s *eventlist, int nevents, void *data_out, size_t *data_available,
         unsigned int flags) 

     struct kevent_qos_s {
             uint64_t        ident;          /* identifier for this event */
             int16_t         filter;         /* filter for event */
             uint16_t        flags;          /* general flags */
             uint32_t        qos;            /* quality of service when servicing event */
             uint64_t        udata;          /* opaque user data identifier */
             uint32_t        fflags;         /* filter-specific flags */
             uint32_t        xflags;         /* extra filter-specific flags */
             int64_t         data;           /* filter-specific data */
             uint64_t        ext[4];         /* filter-specific extensions */
     };
    */

#[repr(C)]
struct kevent_qos_s {
    ident: u64,
    filter: i16,
    flags: u16,
    qos: u32,
    udata: u64,
    fflags: u32,
    xflags: u32,
    data: i64,
    ext: [u64; 4],
}

extern "C" {
    fn kevent_qos(
        kq: libc::c_int,
        changelist: *const kevent_qos_s,
        nchanges: libc::c_int,
        eventlist: *mut kevent_qos_s,
        nevents: libc::c_int,
        data_out: *mut libc::c_void,
        data_available: *mut libc::size_t,
        flags: libc::c_uint,
    ) -> libc::c_int;
}

// very unsafe: GCD uses this process-global workq kqueue
// const KEVENT_FLAG_WORKQ: u32 = 0x000020;

#[derive(Debug)]
pub struct Clock {
    kq: libc::c_int,
    ident_gen: AtomicU64,
}

impl Clock {
    pub fn new() -> io::Result<Self> {
        let kq = unsafe { libc::kqueue() };
        if kq < 0 {
            return Err(io::Error::last_os_error());
        }

        Ok(Self {
            kq,
            ident_gen: AtomicU64::new(0),
        })
    }

    pub fn trigger(&self, data: usize, timeout_usec: i64) -> io::Result<ClockTrigger<'_>> {
        unsafe {
            let res = kevent64(
                self.kq,
                // Changes
                &libc::kevent64_s {
                    ident: 1,
                    filter: libc::EVFILT_TIMER,
                    flags: libc::EV_ADD | libc::EV_ONESHOT,
                    fflags: libc::NOTE_ABSOLUTE | NOTE_MACHTIME,
                    data: timeout_usec,
                    udata: data as u64,
                    ext: [0; 2],
                },
                1,
                // Event buf
                ptr::null_mut(),
                0,
                // Timeout
                KEVENT_FLAG_IMMEDIATE,
                ptr::null_mut(),
            );

            if res < 0 {
                return Err(io::Error::last_os_error());
            }

            Ok(ClockTrigger {
                _ty: PhantomData,
                kq: self.kq,
                ident: 1,
            })
        }
    }

    pub fn wait(&self) -> io::Result<SmallVec<[usize; CLOCK_EV_CAP]>> {
        unsafe {
            let mut kevents = MaybeUninit::<[libc::kevent; 4]>::uninit();
            let res = libc::kevent(
                self.kq,
                // Changes
                ptr::null(),
                0,
                // Event buffer
                kevents.as_mut_ptr().cast(),
                4,
                // Timeout
                ptr::null_mut(),
            );

            if res < 0 {
                return Err(io::Error::last_os_error());
            }

            let kevents =
                slice::from_raw_parts(kevents.as_ptr().cast::<libc::kevent>(), res as usize);

            Ok(SmallVec::from_iter(kevents.iter().map(|v| v.data as usize)))
        }
    }
}

impl Drop for Clock {
    fn drop(&mut self) {
        // TODO
    }
}

#[derive(Debug)]
#[must_use]
pub struct ClockTrigger<'a> {
    _ty: PhantomData<&'a Clock>,
    kq: libc::c_int,
    ident: u64,
}

impl ClockTrigger<'_> {
    pub fn cancel(self) -> io::Result<bool> {
        unsafe {
            let res = kevent64(
                self.kq,
                // Changes
                &libc::kevent64_s {
                    ident: self.ident,
                    filter: libc::EVFILT_TIMER,
                    flags: libc::EV_ONESHOT,
                    fflags: libc::NOTE_ABSOLUTE | NOTE_MACHTIME,
                    data: i64::MAX,
                    udata: 0,
                    ext: [0; 2],
                },
                1,
                // Event buf
                ptr::null_mut(),
                0,
                // Timeout
                KEVENT_FLAG_IMMEDIATE,
                ptr::null_mut(),
            );

            if res < 0 {
                let err = io::Error::last_os_error();
                return if err.kind() == io::ErrorKind::NotFound {
                    Ok(false)
                } else {
                    Err(err)
                };
            }

            Ok(true)
        }
    }
}
