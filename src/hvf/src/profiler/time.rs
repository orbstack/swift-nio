use mach2::mach_time::mach_absolute_time;

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
