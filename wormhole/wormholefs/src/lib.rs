use nix::errno::Errno;

pub mod newmount;
pub mod flock;

fn _err<T: IsMinusOne>(ret: T) -> nix::Result<T> {
    if ret.is_minus_one() {
        Err(Errno::last().into())
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
