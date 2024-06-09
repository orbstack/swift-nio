// based on https://github.com/comex/os-unfair-lock/blob/master/src/lib.rs
// License: 0BSD

#![cfg(any(target_os = "macos", target_os = "ios"))]
#![cfg_attr(feature = "nightly", feature(coerce_unsized, unsize))]

use std::cell::UnsafeCell;
use std::default::Default;
use std::fmt::{self, Debug, Display, Formatter};
use std::marker::PhantomData;
use std::ops::{Deref, DerefMut, Drop};
use std::sync::{LockResult, TryLockError, TryLockResult};

pub struct Mutex<T: ?Sized> {
    pub lock: UnsafeCell<libc::os_unfair_lock>,
    pub cell: UnsafeCell<T>,
}

struct CantSendMutexGuardBetweenThreads;

pub struct MutexGuard<'a, T: ?Sized> {
    pub mutex: &'a Mutex<T>,
    // could just be *const (), but this produces a better error message
    pd: PhantomData<*const CantSendMutexGuardBetweenThreads>,
}

unsafe impl<T: ?Sized + Send> Sync for Mutex<T> {}
unsafe impl<T: ?Sized + Send> Send for Mutex<T> {}

impl<T: ?Sized> Mutex<T> {
    #[inline]
    pub const fn new(value: T) -> Self
    where
        T: Sized,
    {
        Mutex {
            lock: UnsafeCell::new(libc::OS_UNFAIR_LOCK_INIT),
            cell: UnsafeCell::new(value),
        }
    }

    #[inline]
    pub fn lock(&self) -> LockResult<MutexGuard<'_, T>> {
        unsafe {
            libc::os_unfair_lock_lock(self.lock.get());
        }
        Ok(MutexGuard {
            mutex: self,
            pd: PhantomData,
        })
    }

    #[inline]
    pub fn try_lock(&self) -> TryLockResult<MutexGuard<'_, T>> {
        let ok = unsafe { libc::os_unfair_lock_trylock(self.lock.get()) };
        if ok {
            Ok(MutexGuard {
                mutex: self,
                pd: PhantomData,
            })
        } else {
            Err(TryLockError::WouldBlock)
        }
    }

    #[inline]
    pub fn assert_owner(&self) {
        unsafe {
            libc::os_unfair_lock_assert_owner(self.lock.get());
        }
    }

    #[inline]
    pub fn assert_not_owner(&self) {
        unsafe {
            libc::os_unfair_lock_assert_not_owner(self.lock.get());
        }
    }

    #[inline]
    pub fn into_inner(self) -> T
    where
        T: Sized,
    {
        self.cell.into_inner()
    }
}

// It's (potentially) Sync but not Send, because os_unfair_lock_unlock must be called from the
// locking thread.
unsafe impl<'a, T: ?Sized + Sync> Sync for MutexGuard<'a, T> {}

impl<'a, T: ?Sized> Deref for MutexGuard<'a, T> {
    type Target = T;
    #[inline]
    fn deref(&self) -> &T {
        unsafe { &*self.mutex.cell.get() }
    }
}

impl<'a, T: ?Sized> DerefMut for MutexGuard<'a, T> {
    #[inline]
    fn deref_mut(&mut self) -> &mut T {
        unsafe { &mut *self.mutex.cell.get() }
    }
}

impl<'a, T: ?Sized> Drop for MutexGuard<'a, T> {
    #[inline]
    fn drop(&mut self) {
        unsafe {
            libc::os_unfair_lock_unlock(self.mutex.lock.get());
        }
    }
}

// extra impls: Mutex

impl<T: ?Sized + Default> Default for Mutex<T> {
    #[inline]
    fn default() -> Self {
        Mutex::new(T::default())
    }
}

impl<T: ?Sized + Debug> Debug for Mutex<T> {
    #[inline]
    fn fmt(&self, f: &mut Formatter<'_>) -> fmt::Result {
        self.lock().fmt(f)
    }
}

impl<T: ?Sized + Display> Display for Mutex<T> {
    #[inline]
    fn fmt(&self, f: &mut Formatter<'_>) -> fmt::Result {
        self.lock().unwrap().fmt(f)
    }
}

impl<T> From<T> for Mutex<T> {
    #[inline]
    fn from(t: T) -> Mutex<T> {
        Mutex::new(t)
    }
}

#[cfg(feature = "nightly")]
impl<T, U> core::ops::CoerceUnsized<Mutex<U>> for Mutex<T> where T: core::ops::CoerceUnsized<U> {}

// extra impls: MutexGuard

impl<'a, T: ?Sized + Debug> Debug for MutexGuard<'a, T> {
    #[inline]
    fn fmt(&self, f: &mut Formatter<'_>) -> fmt::Result {
        (**self).fmt(f)
    }
}

impl<'a, T: ?Sized + Display> Display for MutexGuard<'a, T> {
    #[inline]
    fn fmt(&self, f: &mut Formatter<'_>) -> fmt::Result {
        (**self).fmt(f)
    }
}

#[cfg(feature = "nightly")]
impl<'a, T: ?Sized, U: ?Sized> core::ops::CoerceUnsized<MutexGuard<'a, U>> for MutexGuard<'a, T> where
    T: core::marker::Unsize<U>
{
}

#[cfg(test)]
mod tests {
    use super::Mutex;
    const TEST_CONST: Mutex<u32> = Mutex::new(42);
    #[test]
    fn basics() {
        let m = TEST_CONST;
        *m.lock().unwrap() += 1;
        {
            let mut g = m.try_lock().unwrap();
            *g += 1;
            assert!(m.try_lock().is_err());
        }
        m.assert_not_owner();
        assert_eq!(*m.lock().unwrap(), 44);
        assert_eq!(m.into_inner(), 44);
    }
    #[test]
    #[cfg(feature = "nightly")]
    fn unsize() {
        use super::MutexGuard;
        let m: Mutex<[u8; 1]> = Mutex::new([100]);
        (&m as &Mutex<[u8]>).lock()[0] += 1;
        (m.lock() as MutexGuard<'_, [u8]>)[0] += 1;
        let n: Mutex<&'static [u8; 1]> = Mutex::new(&[200]);
        let _: Mutex<&'static [u8]> = n;
    }
}
