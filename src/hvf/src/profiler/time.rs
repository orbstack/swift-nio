use std::{
    fmt::Debug,
    mem::MaybeUninit,
    ops::{Add, AddAssign, Sub},
};

use mach2::mach_time::{mach_absolute_time, mach_timebase_info};
use once_cell::race::OnceBox;

static TIMEBASE: OnceBox<mach_timebase_info> = OnceBox::new();

#[derive(Debug, Copy, Clone, PartialEq, PartialOrd, Eq, Ord)]
pub struct MachAbsoluteTime(pub u64);

impl MachAbsoluteTime {
    pub fn now() -> Self {
        Self(unsafe { mach_absolute_time() })
    }

    pub fn dummy() -> Self {
        Self(0)
    }
}

impl Sub<MachAbsoluteTime> for MachAbsoluteTime {
    type Output = MachAbsoluteDuration;

    fn sub(self, rhs: Self) -> Self::Output {
        MachAbsoluteDuration(self.0 - rhs.0)
    }
}

impl Sub<MachAbsoluteDuration> for MachAbsoluteTime {
    type Output = Self;

    fn sub(self, rhs: MachAbsoluteDuration) -> Self::Output {
        Self(self.0 - rhs.0)
    }
}

impl Add<MachAbsoluteDuration> for MachAbsoluteTime {
    type Output = Self;

    fn add(self, rhs: MachAbsoluteDuration) -> Self::Output {
        Self(self.0 + rhs.0)
    }
}

impl AddAssign<MachAbsoluteDuration> for MachAbsoluteTime {
    fn add_assign(&mut self, rhs: MachAbsoluteDuration) {
        self.0 += rhs.0;
    }
}

#[derive(Copy, Clone)]
pub struct MachAbsoluteDuration(u64);

impl MachAbsoluteDuration {
    fn timebase() -> &'static mach_timebase_info {
        TIMEBASE.get_or_init(|| {
            let mut timebase = MaybeUninit::<mach_timebase_info>::uninit();
            unsafe {
                mach_timebase_info(timebase.as_mut_ptr());
                Box::new(timebase.assume_init())
            }
        })
    }

    pub fn nanos(&self) -> u64 {
        let timebase = Self::timebase();
        self.0 * timebase.numer as u64 / timebase.denom as u64
    }

    pub fn from_nanos(nanos: u64) -> Self {
        let timebase = Self::timebase();
        Self(nanos * timebase.denom as u64 / timebase.numer as u64)
    }

    pub fn from_duration(dur: std::time::Duration) -> Self {
        Self::from_nanos(dur.as_nanos() as u64)
    }

    pub fn as_duration(&self) -> std::time::Duration {
        std::time::Duration::from_nanos(self.nanos())
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

    pub fn dummy() -> Self {
        Self(0)
    }
}

impl Debug for MachAbsoluteDuration {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        self.as_duration().fmt(f)
    }
}
