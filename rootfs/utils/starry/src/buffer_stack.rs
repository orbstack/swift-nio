use std::{cell::{RefCell, RefMut}, mem::MaybeUninit};

pub struct BufferStack {
    inner: RefCell<Vec<MaybeUninit<[u8; Self::SIZE]>>>,
}

impl BufferStack {
    // in a 4096-char PATH_MAX, 2048 is the max possible depth: min 1 char per dir name + 1 char for '/'
    const MAX_DEPTH: usize = 2048;
    pub const SIZE: usize = 32768;

    pub fn next(&self) -> BufferStackGuard {
        let mut vec = self.inner.borrow_mut();
        if vec.len() == Self::MAX_DEPTH {
            panic!("too many nested directories: maximum depth is {}", Self::MAX_DEPTH);
        }

        vec.push(MaybeUninit::uninit());
        BufferStackGuard {
            stack: self,
        }
    }
}

impl Default for BufferStack {
    fn default() -> Self {
        Self {
            inner: RefCell::new(Vec::with_capacity(Self::MAX_DEPTH)),
        }
    }
}

pub struct BufferStackGuard<'a> {
    stack: &'a BufferStack,
}

impl BufferStackGuard<'_> {
    pub fn get(&self) -> RefMut<'_, MaybeUninit<[u8; BufferStack::SIZE]>> {
        RefMut::map(self.stack.inner.borrow_mut(), |vec| vec.last_mut().unwrap())
    }
}

impl Drop for BufferStackGuard<'_> {
    fn drop(&mut self) {
        self.stack.inner.borrow_mut().pop();
    }
}
