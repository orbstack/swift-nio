use std::sync::LockResult;

pub fn unpoison<T>(result: LockResult<T>) -> T {
    match result {
        Ok(guard) => guard,
        Err(err) => err.into_inner(),
    }
}

pub trait ExtensionFor<T: ?Sized> {}

impl<T: ?Sized> ExtensionFor<T> for T {}
