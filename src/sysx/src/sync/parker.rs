use std::{
    sync::atomic::{AtomicI8, Ordering::*},
    time::Duration,
};

use mach2::{
    clock_types::mach_timespec_t,
    kern_return::{kern_return_t, KERN_ABORTED, KERN_OPERATION_TIMED_OUT, KERN_SUCCESS},
    mach_types::{semaphore_t, task_t},
    semaphore::semaphore_create,
    sync_policy::SYNC_POLICY_FIFO,
    traps::mach_task_self,
};

const EMPTY: i8 = 0;
const NOTIFIED: i8 = 1;
const PARKED: i8 = -1;

extern "C" {
    // mach2 bindings are wrong for these
    fn semaphore_wait(semaphore: semaphore_t) -> kern_return_t;
    fn semaphore_timedwait(semaphore: semaphore_t, wait_time: mach_timespec_t) -> kern_return_t;
    fn semaphore_signal(semaphore: semaphore_t) -> kern_return_t;
    fn semaphore_destroy(task: task_t, semaphore: semaphore_t) -> kern_return_t;
}

#[derive(Debug, PartialEq, Eq)]
pub enum ParkResult {
    Unparked,
    TimedOut,
}

#[derive(Debug)]
pub struct Parker {
    semaphore: semaphore_t,
    state: AtomicI8,
}

unsafe impl Send for Parker {}
unsafe impl Sync for Parker {}

impl Default for Parker {
    fn default() -> Self {
        let mut semaphore: semaphore_t = 0;
        let ret =
            unsafe { semaphore_create(mach_task_self(), &mut semaphore, SYNC_POLICY_FIFO, 0) };
        assert_eq!(ret, KERN_SUCCESS, "failed to create Parker");

        Self {
            semaphore,
            state: AtomicI8::new(EMPTY),
        }
    }
}

impl Parker {
    // specialize for timed vs. untimed
    #[inline]
    fn wait(&self, timeout: Option<Duration>) -> ParkResult {
        loop {
            let ret = match timeout {
                Some(timeout) => {
                    let seconds = timeout.as_secs();
                    let nanos = timeout.subsec_nanos();
                    unsafe {
                        semaphore_timedwait(
                            self.semaphore,
                            mach_timespec_t {
                                tv_sec: seconds as u32,
                                tv_nsec: nanos as i32,
                            },
                        )
                    }
                }
                None => unsafe { semaphore_wait(self.semaphore) },
            };
            match ret {
                // woke up due to semaphore_signal
                KERN_SUCCESS => return ParkResult::Unparked,
                // interrupted by signal or thread_abort
                KERN_ABORTED => continue,
                // timed out
                KERN_OPERATION_TIMED_OUT => return ParkResult::TimedOut,
                // KERN_TERMINATED = semaphore destroyed (should never happen)
                // others = unexpected
                _ => panic!("semaphore_wait failed: {}", ret),
            }
        }
    }

    pub fn park(&self) {
        if self.state.fetch_sub(1, Acquire) == NOTIFIED {
            return;
        }

        while self.wait(None) != ParkResult::Unparked {}

        self.state.swap(EMPTY, Acquire);
    }

    pub fn park_timeout(&self, timeout: Duration) -> ParkResult {
        if self.state.fetch_sub(1, Acquire) == NOTIFIED {
            return ParkResult::Unparked;
        }

        let result = self.wait(Some(timeout));

        let state = self.state.swap(EMPTY, Acquire);
        if state == NOTIFIED && result == ParkResult::TimedOut {
            // If the state was NOTIFIED but semaphore_wait returned without
            // decrementing the count because of a timeout, it means another
            // thread is about to call semaphore_signal. We must wait for that
            // to happen to ensure the semaphore count is reset.
            while self.wait(None) != ParkResult::Unparked {}
            ParkResult::Unparked
        } else {
            // Either a timeout occurred and we reset the state before any thread
            // tried to wake us up, or we were woken up and reset the state,
            // making sure to observe the state change with acquire ordering.
            // Either way, the semaphore counter is now zero again.
            result
        }
    }

    pub fn unpark(&self) {
        let state = self.state.swap(NOTIFIED, Release);
        if state == PARKED {
            unsafe {
                semaphore_signal(self.semaphore);
            }
        }
    }
}

impl Drop for Parker {
    fn drop(&mut self) {
        unsafe { semaphore_destroy(mach_task_self(), self.semaphore) };
    }
}
