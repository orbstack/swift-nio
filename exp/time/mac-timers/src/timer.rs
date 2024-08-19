use std::borrow::Borrow;
use std::mem::size_of_val;
use mach2::{
    mach_port::{mach_port_allocate, mach_port_deallocate},
    mach_time::mach_absolute_time,
    message::{mach_msg, mach_msg_size_t, MACH_MSG_TIMEOUT_NONE, MACH_RCV_MSG},
    port::{mach_port_name_t, mach_port_t, MACH_PORT_NULL, MACH_PORT_RIGHT_PORT_SET},
    traps::current_task,
};
use thiserror::Error;

use crate::{
    bindings::{
        mach_port_move_member, mk_timer_arm, mk_timer_cancel, mk_timer_create, mk_timer_destroy,
        mk_timer_expire_msg,
    },
    KernelRes,
};

#[derive(Debug)]
pub struct ClockSet<T> {
    task: mach_port_t,
    port_set: mach_port_name_t,
    clocks: Vec<T>,
}

// FIXME: Technically, this should be some sort of `StableDeref`.
impl<T: Borrow<Clock>> ClockSet<T> {
    pub fn new() -> Result<Self, KernelRes> {
        unsafe {
            let task = current_task();
            let mut port_set = 0 as mach_port_name_t;
            KernelRes::new(mach_port_allocate(
                task,
                MACH_PORT_RIGHT_PORT_SET,
                &mut port_set,
            ))
            .as_result()?;

            Ok(Self {
                task,
                port_set,
                clocks: Vec::new(),
            })
        }
    }

    pub fn add(&mut self, clock: T) -> Result<(), KernelRes> {
        KernelRes::new(unsafe {
            mach_port_move_member(self.task, clock.borrow().timer, self.port_set)
        })
        .as_result()?;
        self.clocks.push(clock);
        Ok(())
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
                self.port_set,         // receive name
                MACH_MSG_TIMEOUT_NONE, // timeout
                MACH_PORT_NULL,        // notify port
            )
        };
    }
}

impl<T> Drop for ClockSet<T> {
    fn drop(&mut self) {
        if let Err(err) =
            KernelRes::new(unsafe { mach_port_deallocate(self.task, self.port_set) }).as_result()
        {
            eprintln!("Failed to destroy clock set: {err:?}");
        }
    }
}

#[derive(Debug, Hash, Eq, PartialEq)]
pub struct Clock {
    timer: mach_port_name_t,
}

impl Clock {
    pub fn new() -> Result<Self, ClockCreateError> {
        let timer = unsafe { mk_timer_create() };

        if timer == MACH_PORT_NULL {
            Err(ClockCreateError)
        } else {
            Ok(Self { timer })
        }
    }

    pub fn arm_until(&self, timeout: u64) -> Result<(), KernelRes> {
        KernelRes::new(unsafe { mk_timer_arm(self.timer, timeout) }).as_result()
    }

    pub fn arm_for(&self, timeout: u64) -> Result<(), KernelRes> {
        self.arm_until(unsafe { mach_absolute_time() } + timeout)
    }

    pub fn cancel(&self) -> Result<(), KernelRes> {
        KernelRes::new(unsafe { mk_timer_cancel(self.timer, std::ptr::null_mut()) }).as_result()?;
        Ok(())
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
}

impl Drop for Clock {
    fn drop(&mut self) {
        if let Err(err) = KernelRes::new(unsafe { mk_timer_destroy(self.timer) }).as_result() {
            eprintln!("Failed to destroy clock: {err:?}");
        }
    }
}

#[derive(Debug, Clone, Error)]
#[non_exhaustive]
#[error("failed to create clock")]
pub struct ClockCreateError;
