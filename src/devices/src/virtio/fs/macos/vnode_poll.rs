use std::{
    marker::PhantomData,
    mem::{size_of, MaybeUninit},
    os::fd::{AsRawFd, FromRawFd, OwnedFd, RawFd},
    sync::Arc,
    time::Duration,
};

use anyhow::anyhow;
use bitflags::bitflags;
use libc::{
    kevent64_s, EVFILT_USER, EVFILT_VNODE, EV_ADD, EV_CLEAR, EV_DELETE, EV_ENABLE, NOTE_EXTEND,
    NOTE_FFCOPY, NOTE_TRIGGER,
};
use nix::{errno::Errno, sys::event::kqueue};
use zerocopy::AsBytes;

use crate::virtio::FsCallbacks;

use super::passthrough::get_path_by_fd;

const DEBOUNCE_DURATION: Duration = Duration::from_millis(100);

const IDENT_STOP: u64 = 1;

const KEVENT_FLAG_IMMEDIATE: u32 = 1;

pub struct VnodePoller<E: Into<u64> + From<u64>> {
    kqueue: OwnedFd,
    callbacks: Arc<dyn FsCallbacks>,
    _event_id: PhantomData<E>,
}

enum EventResult {
    Krpc(String),
    Stop,
}

// ... we're responsible for prepending this
const VIRTIOFS_MOUNTPOINT: &str = "/mnt/mac";

const KRPC_MSG_NOTIFYPROXY_INJECT: u32 = 1;

#[derive(AsBytes)]
#[repr(C)]
struct KrpcHeader {
    len: u32,
    typ: u32,
    np: KrpcNotifyproxyInject,
}

#[derive(AsBytes)]
#[repr(C)]
struct KrpcNotifyproxyInject {
    count: u64,
}

bitflags! {
    pub struct NpFlag: u32 {
        const NP_FLAG_CREATE = 1 << 0;
        const NP_FLAG_MODIFY = 1 << 1;
        const NP_FLAG_STAT_ATTR = 1 << 2;
        const NP_FLAG_REMOVE = 1 << 3;
        const NP_FLAG_DIR_CHANGE = 1 << 4;
        const NP_FLAG_RENAME = 1 << 5;
    }
}

impl<E: Into<u64> + From<u64>> VnodePoller<E> {
    pub fn new(callbacks: Arc<dyn FsCallbacks>) -> nix::Result<Self> {
        let fd = kqueue()?;
        let fd = unsafe { OwnedFd::from_raw_fd(fd) };

        let poller = VnodePoller {
            kqueue: fd,
            callbacks,
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
            // we only care about tail -f case. EXTEND should be less chatty than WRITE
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

    pub fn stop(&self) -> nix::Result<()> {
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

    pub fn main_loop(&self) -> anyhow::Result<()> {
        loop {
            let events_buf = MaybeUninit::<[kevent64_s; 512]>::uninit();
            let mut events_buf = unsafe { events_buf.assume_init() };

            match self.kevent(&[], &mut events_buf, 0) {
                Ok(0) => break,
                Err(Errno::EINTR) => continue,
                Err(e) => return Err(e.into()),

                Ok(n) => {
                    let mut krpc_events = Vec::with_capacity(n as usize);

                    for event in &events_buf[0..n] {
                        match self.process_event(event)? {
                            Some(EventResult::Stop) => return Ok(()),
                            Some(EventResult::Krpc(path)) => {
                                let flags = NpFlag::NP_FLAG_MODIFY | NpFlag::NP_FLAG_STAT_ATTR;
                                let event = (flags, path);
                                krpc_events.push(event);
                            }
                            None => {}
                        }
                    }

                    if !krpc_events.is_empty() {
                        self.send_krpc_events(&krpc_events);
                    }
                }
            }

            // TODO: stopping shouldn't wait for debounce
            std::thread::sleep(DEBOUNCE_DURATION);
        }

        Ok(())
    }

    fn process_event(&self, event: &kevent64_s) -> anyhow::Result<Option<EventResult>> {
        match event.filter {
            // shutdown
            EVFILT_USER => return Ok(Some(EventResult::Stop)),
            EVFILT_VNODE => {
                if event.fflags & NOTE_EXTEND != 0 {
                    let fd = event.ident as RawFd;
                    let nodeid = event.udata as u64;

                    // TODO: just use nodeid for krpc
                    let path = VIRTIOFS_MOUNTPOINT.to_string() + &get_path_by_fd(fd)?;
                    debug!("vnode EXTEND: nodeid={} fd={} path={}", nodeid, fd, path);
                    return Ok(Some(EventResult::Krpc(path)));
                }
            }
            _ => return Err(anyhow!("unknown event filter")),
        }

        Ok(None)
    }

    fn send_krpc_events(&self, krpc_events: &[(NpFlag, String)]) {
        let total_len =
            // 8 byte header
            size_of::<KrpcHeader>()
            // u64 desc for each event
            + 8 * krpc_events.len()
            // total path len
            + krpc_events
                .iter()
                .map(|(_, path)| path.len())
                .sum::<usize>()
            // null terminator for each path
            + krpc_events.len();

        let mut buf: Vec<u8> = Vec::with_capacity(total_len);

        let header = KrpcHeader {
            len: total_len as u32 - 8,
            typ: KRPC_MSG_NOTIFYPROXY_INJECT,
            np: KrpcNotifyproxyInject {
                count: krpc_events.len() as u64,
            },
        };
        buf.extend_from_slice(header.as_bytes());

        // write descs (flags+len)
        for (flags, path) in krpc_events {
            let len = path.len() as u32;
            let desc = ((len as u64) << 32) | (flags.bits() as u64);
            buf.extend_from_slice(&desc.to_le_bytes());
        }

        // write paths
        for (_, path) in krpc_events {
            buf.extend_from_slice(path.as_bytes());
            buf.push(0);
        }

        debug!("send_krpc_events: len={} -> {:?}", buf.len(), buf);
        self.callbacks.send_krpc_events(&buf);
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
