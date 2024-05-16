use std::{
    marker::PhantomData,
    mem::MaybeUninit,
    os::fd::{AsFd, AsRawFd, FromRawFd, OwnedFd, RawFd},
    time::Duration,
};

use libc::{
    kevent64_s, EVFILT_USER, EVFILT_VNODE, EV_ADD, EV_CLEAR, EV_DELETE, EV_ENABLE, NOTE_EXTEND,
    NOTE_FFCOPY, NOTE_TRIGGER, NOTE_WRITE,
};
use nix::{errno::Errno, sys::event::kqueue};

const DEBOUNCE_DURATION: Duration = Duration::from_millis(100);

const IDENT_STOP: u64 = 1;

const KEVENT_FLAG_IMMEDIATE: u32 = 1;

pub struct VnodePoller<E: Into<u64> + From<u64>> {
    kqueue: OwnedFd,
    _event_id: PhantomData<E>,
}

impl<E: Into<u64> + From<u64>> VnodePoller<E> {
    pub fn new() -> nix::Result<Self> {
        let fd = kqueue()?;
        let fd = unsafe { OwnedFd::from_raw_fd(fd) };

        let poller = VnodePoller {
            kqueue: fd,
            _event_id: PhantomData,
        };

        // register waker
        let waker = kevent64_s {
            ident: IDENT_STOP,
            filter: EVFILT_USER,
            flags: EV_ADD | EV_CLEAR,
            fflags: NOTE_FFCOPY,
            ..default_kevent()
        };
        poller.kevent(&[waker], &mut [], KEVENT_FLAG_IMMEDIATE)?;

        Ok(poller)
    }

    // this should be handle id, but how to stop dupe registrations per nodeid? don't worry about it?
    pub fn register<F: AsRawFd>(&self, fd: F, event_id: E) -> nix::Result<()> {
        let new_event = kevent64_s {
            ident: fd.as_raw_fd() as u64,
            filter: EVFILT_VNODE,
            flags: EV_ADD | EV_CLEAR,
            // we only care about tail -f case
            // TODO: NOTE_DELETE for closing auto-opened fds -- or better to use fsevents for that?
            fflags: NOTE_EXTEND,
            udata: event_id.into(),
            ..default_kevent()
        };
        self.kevent(&[new_event], &mut [], KEVENT_FLAG_IMMEDIATE)?;
        Ok(())
    }

    pub fn unregister<F: AsRawFd>(&self, fd: F, event_id: E) -> nix::Result<()> {
        let new_event = kevent64_s {
            ident: fd.as_raw_fd() as u64,
            filter: EVFILT_VNODE,
            flags: EV_DELETE,
            fflags: NOTE_EXTEND,
            udata: event_id.into(),
            ..default_kevent()
        };
        self.kevent(&[new_event], &mut [], KEVENT_FLAG_IMMEDIATE)?;
        Ok(())
    }

    fn wake_user(&self) -> nix::Result<()> {
        let waker = kevent64_s {
            ident: IDENT_STOP,
            filter: EVFILT_USER,
            flags: EV_ENABLE,
            fflags: NOTE_FFCOPY | NOTE_TRIGGER,
            ..default_kevent()
        };
        self.kevent(&[waker], &mut [], KEVENT_FLAG_IMMEDIATE)?;

        Ok(())
    }

    pub fn main_loop(&self) -> nix::Result<()> {
        loop {
            let events_buf = MaybeUninit::<[kevent64_s; 512]>::uninit();
            let mut events_buf = unsafe { events_buf.assume_init() };

            match self.kevent(&[], &mut events_buf, 0) {
                Ok(0) => break,
                Err(Errno::EINTR) => continue,
                Err(e) => return Err(e),

                Ok(n) => {
                    for event in &events_buf[0..n] {
                        if self.process_event(event)? {
                            return Ok(());
                        }
                    }
                }
            }

            // TODO: stopping shouldn't wait for debounce
            std::thread::sleep(DEBOUNCE_DURATION);
        }

        Ok(())
    }

    fn process_event(&self, event: &kevent64_s) -> nix::Result<bool> {
        match event.filter {
            // shutdown
            EVFILT_USER => return Ok(true),
            EVFILT_VNODE => {
                if event.fflags & NOTE_EXTEND != 0 {
                    let fd = event.ident;
                    let nodeid = event.udata as u64;
                    // println!("vnode WRITE: nodeid={} fd={}", nodeid, fd);
                }
            }
            _ => return Err(Errno::EINVAL),
        }

        Ok(false)
    }

    fn kevent(
        &self,
        changes: &[kevent64_s],
        events_buf: &mut [kevent64_s],
        flags: u32,
    ) -> nix::Result<usize> {
        let ret = unsafe {
            libc::kevent64(
                self.kqueue.as_raw_fd(),
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
