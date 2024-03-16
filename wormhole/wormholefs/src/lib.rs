use nix::errno::Errno;

pub mod newmount;
pub mod flock;

pub fn err<T: IsMinusOne>(ret: T) -> nix::Result<T> {
    if ret.is_minus_one() {
        Err(Errno::last().into())
    } else {
        Ok(ret)
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
