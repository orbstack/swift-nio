use std::mem;

use mach2::{
    clock::clock_alarm,
    clock_types::{alarm_type_t, clock_id_t, mach_timespec_t, SYSTEM_CLOCK, TIME_RELATIVE},
    mach_port::{mach_port_allocate, mach_port_destroy},
    mach_types::{clock_serv_port_t, clock_serv_t},
    message::{mach_msg, mach_msg_header_t, mach_msg_size_t, MACH_MSG_TIMEOUT_NONE, MACH_RCV_MSG},
    port::{
        mach_port_name_t, mach_port_t, MACH_PORT_NULL, MACH_PORT_RIGHT_PORT_SET,
        MACH_PORT_RIGHT_RECEIVE, MACH_PORT_RIGHT_SEND,
    },
    traps::current_task,
};

use crate::{
    bindings::{host_get_clock_service, mach_host_self, mach_port_move_member},
    KernelRes,
};

#[derive(Debug)]
pub struct Clock {
    task: mach_port_t,
    system_clock: clock_serv_port_t,
    port_set: mach_port_name_t,
}

impl Clock {
    pub fn new() -> Result<Self, KernelRes> {
        unsafe {
            let task = current_task();
            let host = mach_host_self();

            // Get the system clock (this is the only clock which can be alarmed)
            let mut system_clock = 0 as clock_serv_t;
            KernelRes::new(host_get_clock_service(
                host,
                SYSTEM_CLOCK as clock_id_t,
                &mut system_clock,
            ))
            .as_result()?;

            // Create a set of ports we can be woken up from
            let mut port_set = 0 as mach_port_name_t;
            KernelRes::new(mach_port_allocate(
                task,
                MACH_PORT_RIGHT_PORT_SET,
                &mut port_set,
            ))
            .as_result()?;

            Ok(Self {
                task,
                system_clock,
                port_set,
            })
        }
    }

    pub fn trigger(&self, after: mach_timespec_t) -> Result<ClockTrigger, KernelRes> {
        unsafe {
            // Create a port to send the alarm signal to.
            let mut port = 0 as mach_port_name_t;
            KernelRes::new(mach_port_allocate(
                self.task,
                MACH_PORT_RIGHT_SEND | MACH_PORT_RIGHT_RECEIVE,
                &mut port,
            ))
            .as_result()?;

            // Add the port to the set.
            KernelRes::new(mach_port_move_member(self.task, port, self.port_set)).as_result()?;

            // Alarm the clock
            KernelRes::new(clock_alarm(
                self.system_clock,
                TIME_RELATIVE as alarm_type_t,
                after,
                port,
            ))
            .as_result()?;

            Ok(ClockTrigger {
                task: self.task,
                port,
            })
        }
    }

    pub fn wait(&self) {
        let mut msg = mach_msg_header_t::default();
        let msg_sz = mem::size_of::<mach_msg_header_t>() as mach_msg_size_t;
        unsafe {
            mach_msg(
                &mut msg,              // message pointer
                MACH_RCV_MSG,          // options
                0,                     // send size
                msg_sz,                // receive size
                self.port_set,         // receive name
                MACH_MSG_TIMEOUT_NONE, // timeout
                MACH_PORT_NULL,        // notify port
            );
        }
    }
}

impl Drop for Clock {
    fn drop(&mut self) {
        unsafe { mach_port_destroy(self.task, self.port_set) };
    }
}

#[derive(Debug)]
#[must_use]
pub struct ClockTrigger {
    task: mach_port_t,
    port: mach_port_name_t,
}

impl Drop for ClockTrigger {
    fn drop(&mut self) {
        unsafe { mach_port_destroy(self.task, self.port) };
    }
}
