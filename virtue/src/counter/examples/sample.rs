use std::{thread, time::Duration};

use counter::{counter, default_env_filter, display_every, RateCounter};

counter! {
    pub TIMES_CALLED in "foo": RateCounter = RateCounter::new(FILTER);
}

fn main() {
    thread::spawn(|| loop {
        TIMES_CALLED.count();
    });

    let _guard = display_every(default_env_filter().unwrap(), Duration::from_secs_f32(0.5));

    thread::sleep(Duration::from_secs_f32(10.));
}
