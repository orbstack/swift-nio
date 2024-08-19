use std::{marker::PhantomData, mem::transmute};

use crate::libdispatch::{_dispatch_source_type_timer, dispatch_async_f, dispatch_object_t, dispatch_queue_create, dispatch_queue_t, dispatch_release, dispatch_resume, dispatch_source_cancel, dispatch_source_create, dispatch_source_set_event_handler_f, dispatch_source_set_timer, dispatch_source_t, dispatch_time, DISPATCH_TIME_NOW};

pub struct Queue {
    queue: dispatch_queue_t,
}

impl Queue {
    pub fn new() -> Self {
        Self {
            queue: unsafe { dispatch_queue_create(c"dev.orbstack.test".as_ptr(), std::ptr::null_mut()) },
        }
    }
}

impl Default for Queue {
    fn default() -> Self {
        Self::new()
    }
}

impl Drop for Queue {
    fn drop(&mut self) {
        unsafe { dispatch_release(transmute::<dispatch_queue_t, dispatch_object_t>(self.queue)) };
    }
}

unsafe extern "C" fn event_handler(_: *mut std::ffi::c_void) {
    println!("Timer fired");
}

pub struct Timer<'a> {
    queue: &'a Queue,
    source: dispatch_source_t,
}

impl<'a> Timer<'a> {
    pub fn new(queue: &'a Queue) -> Self {
        let source = unsafe { dispatch_source_create(&_dispatch_source_type_timer, 0, 0, queue.queue) };
        unsafe {
            dispatch_source_set_event_handler_f(source, Some(event_handler));
        }
        Self { queue, source }
    }

    pub fn arm(&self, timeout_ns: i64) {
        let deadline = unsafe { dispatch_time(DISPATCH_TIME_NOW as u64, timeout_ns) }; 
        unsafe {
            dispatch_source_set_timer(self.source, deadline, 0, 0);
            dispatch_resume(transmute::<dispatch_source_t, dispatch_object_t>(self.source));
        }
    }

    pub fn cancel(&self) {
        unsafe {
            dispatch_source_cancel(self.source);
        }
    }
}

impl Drop for Timer<'_> {
    fn drop(&mut self) {
        unsafe { dispatch_release(transmute::<dispatch_source_t, dispatch_object_t>(self.source)) };
    }
}
