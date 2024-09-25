use std::sync::{Arc, Condvar, Mutex, OnceLock};

use anyhow::anyhow;
use once_cell::sync::Lazy;

static ROSETTA_DATA: OnceLock<Vec<u8>> = OnceLock::new();

static ROSETTA_INIT_DONE_COND: Lazy<Arc<(Mutex<bool>, Condvar)>> = Lazy::new(|| {
    let (lock, cvar) = (Mutex::new(false), Condvar::new());
    Arc::new((lock, cvar))
});

pub fn set_rosetta_data(data: &[u8]) -> anyhow::Result<()> {
    ROSETTA_DATA
        .set(data.to_vec())
        .map_err(|_| anyhow!("data already set"))?;

    // notify waiters
    let (lock, cvar) = &**ROSETTA_INIT_DONE_COND;
    let mut flag = lock.lock().unwrap();
    *flag = true;
    cvar.notify_all();

    Ok(())
}

pub fn get_rosetta_data() -> &'static [u8] {
    if let Some(data) = ROSETTA_DATA.get() {
        return data;
    }

    let (lock, cvar) = &**ROSETTA_INIT_DONE_COND;
    let mut flag = lock.lock().unwrap();
    while !*flag {
        flag = cvar.wait(flag).unwrap();
    }

    ROSETTA_DATA.get().unwrap()
}
