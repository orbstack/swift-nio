use std::cell::{Ref, RefCell};

pub struct PathStack {
    inner: RefCell<Vec<u8>>,
}

impl PathStack {
    pub fn push(&self, segment: &[u8]) -> PathStackGuard {
        let mut buf = self.inner.borrow_mut();
        let old_len = buf.len();
        if !buf.is_empty() {
            buf.push(b'/');
        }
        buf.extend_from_slice(segment);
        PathStackGuard { stack: self, old_len }
    }
}

impl Default for PathStack {
    fn default() -> Self {
        Self {
            inner: RefCell::new(Vec::with_capacity(libc::PATH_MAX as usize)),
        }
    }
}

pub struct PathStackGuard<'a> {
    stack: &'a PathStack,
    old_len: usize,
}

impl<'a> PathStackGuard<'a> {
    pub fn get(&self) -> Ref<'_, Vec<u8>> {
        self.stack.inner.borrow()
    }
}

impl<'a> Drop for PathStackGuard<'a> {
    fn drop(&mut self) {
        self.stack.inner.borrow_mut().truncate(self.old_len);
    }
}
