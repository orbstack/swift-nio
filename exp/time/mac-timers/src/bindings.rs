#![allow(non_camel_case_types)]

use mach2::{
    clock_types::clock_id_t,
    kern_return::kern_return_t,
    mach_types::{clock_serv_t, host_name_port_t, host_t, ipc_space_t},
    message::mach_msg_header_t,
    port::mach_port_name_t,
};

extern "C" {
    pub fn mach_host_self() -> host_name_port_t;

    pub fn mach_port_move_member(
        task: ipc_space_t,
        member: mach_port_name_t,
        after: mach_port_name_t,
    ) -> kern_return_t;

    pub fn mk_timer_create() -> mach_port_name_t;

    pub fn mk_timer_destroy(name: mach_port_name_t) -> kern_return_t;

    pub fn mk_timer_arm(name: mach_port_name_t, expire_time: u64) -> kern_return_t;

    pub fn mk_timer_cancel(name: mach_port_name_t, result_time: *mut u64) -> kern_return_t;

    pub fn host_get_clock_service(
        host: host_t,
        clock_id: clock_id_t,
        clock_serv: *mut clock_serv_t,
    ) -> kern_return_t;
}

#[derive(Debug, Copy, Clone, Default)]
#[repr(packed(4))]
pub struct mk_timer_expire_msg {
    pub header: mach_msg_header_t,
    pub _unused: [u64; 3],
}
