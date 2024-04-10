use std::sync::{Arc, Condvar, Mutex, MutexGuard};

use once_cell::sync::Lazy;

static ROSETTA_DATA: Lazy<Arc<Mutex<Vec<u8>>>> = Lazy::new(|| Arc::new(Mutex::new(Vec::new())));

static ROSETTA_INIT_DONE_COND: Lazy<Arc<(Mutex<bool>, Condvar)>> = Lazy::new(|| {
    let (lock, cvar) = (Mutex::new(false), Condvar::new());
    Arc::new((lock, cvar))
});

pub fn set_rosetta_data(data: &[u8]) {
    ROSETTA_DATA.lock().unwrap().extend_from_slice(data);

    let (lock, cvar) = &**ROSETTA_INIT_DONE_COND;
    let mut flag = lock.lock().unwrap();
    *flag = true;
    cvar.notify_all();
}

pub fn get_rosetta_data() -> MutexGuard<'static, Vec<u8>> {
    let (lock, cvar) = &**ROSETTA_INIT_DONE_COND;
    let mut flag = lock.lock().unwrap();
    while !*flag {
        flag = cvar.wait(flag).unwrap();
    }
    ROSETTA_DATA.lock().unwrap()
}
