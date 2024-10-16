use nix::errno::Errno;
use nix::libc::{cc_t, tcflag_t, NCCS};
use serde::{Deserialize, Serialize};

pub mod asyncfile;
pub mod flock;
pub mod newmount;
pub mod paths;

fn _err<T: IsMinusOne>(ret: T) -> nix::Result<T> {
    if ret.is_minus_one() {
        Err(Errno::last())
    } else {
        Ok(ret)
    }
}

pub fn err<T: IsMinusOne + Copy>(ret: T) -> nix::Result<T> {
    loop {
        match _err(ret) {
            Err(Errno::EINTR) => {}
            other => return other,
        }
    }
}

pub trait IsMinusOne {
    fn is_minus_one(&self) -> bool;
}

impl IsMinusOne for i64 {
    fn is_minus_one(&self) -> bool {
        *self == -1
    }
}

impl IsMinusOne for i32 {
    fn is_minus_one(&self) -> bool {
        *self == -1
    }
}

impl IsMinusOne for isize {
    fn is_minus_one(&self) -> bool {
        *self == -1
    }
}

#[derive(Serialize, Deserialize, Debug, Clone)]
pub struct TermiosParams {
    pub input_flags: tcflag_t,
    pub output_flags: tcflag_t,
    pub control_flags: tcflag_t,
    pub local_flags: tcflag_t,
    pub control_chars: [cc_t; NCCS],
}
