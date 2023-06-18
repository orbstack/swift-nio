use std::collections::HashMap;

use once_cell::sync::Lazy;
use tokio::sync::Mutex;

pub static PROCESS_WAIT_LOCK: Lazy<Mutex<()>> = Lazy::new(|| Mutex::new(()));

struct ServiceTracker {
    pids: HashMap<String, Vec<u32>>,
}

impl ServiceTracker {
    fn new() -> Self {
        Self { pids: HashMap::new() }
    }
}
