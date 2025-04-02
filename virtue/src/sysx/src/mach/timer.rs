use std::borrow::Borrow;

use mach2::{
    kern_return::kern_return_t,
    mach_port::{mach_port_allocate, mach_port_deallocate},
    mach_types::ipc_space_t,
    message::{mach_msg, mach_msg_header_t, mach_msg_size_t, MACH_MSG_TIMEOUT_NONE, MACH_RCV_MSG},
    port::{mach_port_name_t, mach_port_t, MACH_PORT_NULL, MACH_PORT_RIGHT_PORT_SET},
    traps::current_task,
};
use thiserror::Error;

use super::{
    error::{MachError, MachResult},
    time::{MachAbsoluteDuration, MachAbsoluteTime},
};

// === Bindings === //

extern "C" {
    fn mach_port_move_member(
        task: ipc_space_t,
        member: mach_port_name_t,
        after: mach_port_name_t,
    ) -> kern_return_t;

    fn mk_timer_create() -> mach_port_name_t;

    fn mk_timer_destroy(name: mach_port_name_t) -> kern_return_t;

    fn mk_timer_arm(name: mach_port_name_t, expire_time: u64) -> kern_return_t;

    fn mk_timer_cancel(name: mach_port_name_t, result_time: *mut u64) -> kern_return_t;
}

#[derive(Debug, Copy, Clone, Default)]
#[repr(C, packed(4))]
#[allow(non_camel_case_types)]
pub struct mk_timer_expire_msg {
    pub header: mach_msg_header_t,
    pub _unused: [u64; 3],
}

// === TimerSet === //

#[derive(Debug)]
pub struct TimerSet<T> {
    task: mach_port_t,
    port_set: mach_port_name_t,
    timers: Vec<T>,
}

impl<T: Borrow<Timer>> TimerSet<T> {
    pub fn new() -> MachResult<Self> {
        unsafe {
            let task = current_task();
            let mut port_set = 0 as mach_port_name_t;
            MachError::result(mach_port_allocate(
                task,
                MACH_PORT_RIGHT_PORT_SET,
                &mut port_set,
            ))?;

            Ok(Self {
                task,
                port_set,
                timers: Vec::new(),
            })
        }
    }

    pub fn add(&mut self, timer: T) -> MachResult<()> {
        MachError::result(unsafe {
            mach_port_move_member(self.task, timer.borrow().timer, self.port_set)
        })?;
        self.timers.push(timer);
        Ok(())
    }

    pub fn wait(&self) -> TimerId {
        let mut msg = mk_timer_expire_msg::default();
        let msg_sz = size_of_val(&msg) as mach_msg_size_t;
        unsafe {
            mach_msg(
                &mut msg.header,       // message pointer
                MACH_RCV_MSG,          // options
                0,                     // send size
                msg_sz,                // receive size
                self.port_set,         // receive name
                MACH_MSG_TIMEOUT_NONE, // timeout
                MACH_PORT_NULL,        // notify port
            )
        };

        TimerId(msg.header.msgh_local_port)
    }
}

impl<T> Drop for TimerSet<T> {
    fn drop(&mut self) {
        if let Err(err) =
            MachError::result(unsafe { mach_port_deallocate(self.task, self.port_set) })
        {
            eprintln!("Failed to destroy timer set: {err:?}");
        }
    }
}

// === Timer === //

#[derive(Debug, Hash, Eq, PartialEq)]
pub struct TimerId(mach_port_name_t);

#[derive(Debug, Hash, Eq, PartialEq)]
pub struct Timer {
    timer: mach_port_name_t,
}

unsafe impl Send for Timer {}
unsafe impl Sync for Timer {}

impl Timer {
    pub fn new() -> Result<Self, TimerCreateError> {
        let timer = unsafe { mk_timer_create() };

        if timer == MACH_PORT_NULL {
            Err(TimerCreateError)
        } else {
            Ok(Self { timer })
        }
    }

    pub fn arm_until(&self, timeout: MachAbsoluteTime) -> MachResult<()> {
        MachError::result(unsafe { mk_timer_arm(self.timer, timeout.0) })
    }

    pub fn arm_for(&self, timeout: MachAbsoluteDuration) -> MachResult<()> {
        self.arm_until(MachAbsoluteTime::now() + timeout)
    }

    pub fn cancel(&self) -> MachResult<u64> {
        let mut res_time = 0u64;
        MachError::result(unsafe { mk_timer_cancel(self.timer, &mut res_time) })?;
        Ok(res_time)
    }

    pub fn wait(&self) {
        let mut msg = mk_timer_expire_msg::default();
        let msg_sz = size_of_val(&msg) as mach_msg_size_t;
        unsafe {
            mach_msg(
                &mut msg.header,       // message pointer
                MACH_RCV_MSG,          // options
                0,                     // send size
                msg_sz,                // receive size
                self.timer,            // receive name
                MACH_MSG_TIMEOUT_NONE, // timeout
                MACH_PORT_NULL,        // notify port
            )
        };
    }

    pub fn id(&self) -> TimerId {
        TimerId(self.timer)
    }
}

impl Drop for Timer {
    fn drop(&mut self) {
        if let Err(err) = MachError::result(unsafe { mk_timer_destroy(self.timer) }) {
            eprintln!("Failed to destroy timer: {err:?}");
        }
    }
}

#[derive(Debug, Clone, Error)]
#[non_exhaustive]
#[error("failed to create timer")]
pub struct TimerCreateError;
