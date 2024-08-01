use std::{mem::MaybeUninit, ops::Sub};

use mach2::mach_time::{mach_absolute_time, mach_timebase_info};
use once_cell::race::OnceBox;

static TIMEBASE: OnceBox<mach_timebase_info> = OnceBox::new();

#[derive(Debug, Copy, Clone)]
pub struct MachAbsoluteTime(u64);

impl MachAbsoluteTime {
    pub fn now() -> Self {
        Self(unsafe { mach_absolute_time() })
    }

    pub fn dummy() -> Self {
        Self(0)
    }
}

impl Sub for MachAbsoluteTime {
    type Output = MachAbsoluteDuration;

    fn sub(self, rhs: Self) -> Self::Output {
        MachAbsoluteDuration(self.0 - rhs.0)
    }
}

#[derive(Debug, Copy, Clone)]
pub struct MachAbsoluteDuration(u64);

impl MachAbsoluteDuration {
    pub fn nanos(&self) -> u64 {
        let timebase = TIMEBASE.get_or_init(|| {
            let mut timebase = MaybeUninit::<mach_timebase_info>::uninit();
            unsafe {
                mach_timebase_info(timebase.as_mut_ptr());
                Box::new(timebase.assume_init())
            }
        });

        self.0 * timebase.numer as u64 / timebase.denom as u64
    }

    pub fn micros(&self) -> u64 {
        self.nanos() / 1_000
    }

    pub fn millis(&self) -> u64 {
        self.nanos() / 1_000_000
    }

    pub fn millis_f64(&self) -> f64 {
        self.nanos() as f64 / 1_000_000.0
    }

    pub fn seconds(&self) -> u64 {
        self.nanos() / 1_000_000_000
    }
}
