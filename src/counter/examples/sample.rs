use std::{thread, time::Duration};

use counter::{counter, default_env_filter, display_every, TotalCounter};

counter! {
    pub TIMES_CALLED: TotalCounter = TotalCounter::new("foo");
}

fn main() {
    thread::spawn(|| loop {
        TIMES_CALLED.count();
    });

    let _guard = display_every(default_env_filter(), Duration::from_secs_f32(0.5));

    thread::sleep(Duration::from_secs_f32(10.));
}
