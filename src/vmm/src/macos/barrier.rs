use std::sync::{Condvar, Mutex};

#[derive(Debug, Copy, Clone)]
pub enum BarrierWaitResult {
    Thunder(bool),
    Broken,
}

impl BarrierWaitResult {
    pub fn is_broken(self) -> bool {
        matches!(self, Self::Broken)
    }
}

pub struct BreakableBarrier {
    lock: Mutex<BarrierState>,
    cvar: Condvar,
    num_threads: usize,
}

struct BarrierState {
    count: usize,
    generation_id: usize,
    is_broken: bool,
}

impl BreakableBarrier {
    #[must_use]
    pub fn new(n: usize) -> Self {
        Self {
            lock: Mutex::new(BarrierState {
                count: 0,
                generation_id: 0,
                is_broken: false,
            }),
            cvar: Condvar::new(),
            num_threads: n,
        }
    }

    #[must_use]
    pub fn wait(&self) -> BarrierWaitResult {
        let mut lock = self.lock.lock().unwrap();

        if lock.is_broken {
            return BarrierWaitResult::Broken;
        }

        let local_gen = lock.generation_id;
        lock.count += 1;

        let (is_leader, lock) = if lock.count < self.num_threads {
            let guard = self
                .cvar
                .wait_while(lock, |state| {
                    !state.is_broken && local_gen == state.generation_id
                })
                .unwrap();

            (false, guard)
        } else {
            lock.count = 0;
            lock.generation_id = lock.generation_id.wrapping_add(1);
            self.cvar.notify_all();

            (true, lock)
        };

        if lock.is_broken {
            BarrierWaitResult::Broken
        } else {
            BarrierWaitResult::Thunder(is_leader)
        }
    }

    pub fn destroy(&self) {
        let mut lock = self.lock.lock().unwrap();
        lock.is_broken = true;
        self.cvar.notify_all();
    }
}
