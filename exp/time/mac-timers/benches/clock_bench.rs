use criterion::{criterion_group, criterion_main, Criterion};
use hdrhistogram::Histogram;
use mach2::{clock_types::mach_timespec_t, mach_time::{mach_absolute_time, mach_timebase_info}};
use osx_timers::dispatch::Queue;

fn mabs_to_nsec(mabs: u64) -> u64 {
    let mut info = mach_timebase_info {
        numer: 0,
        denom: 0,
    };
    unsafe {
        mach_timebase_info(&mut info);
    }
    mabs * info.numer as u64 / info.denom as u64
}

fn bench(c: &mut Criterion) {
    // c.bench_function("clock", |c| {
    //     let clock = osx_timers::clock::Clock::new().unwrap();

    //     c.iter(|| {
    //         drop(
    //             clock
    //                 .trigger(mach_timespec_t {
    //                     tv_sec: 0,
    //                     tv_nsec: 1000 * 100, // 100 µs
    //                 })
    //                 .unwrap(),
    //         );
    //     });
    // });

    // c.bench_function("clock long", |c| {
    //     let clock = osx_timers::clock::Clock::new().unwrap();

    //     c.iter(|| {
    //         drop(
    //             clock
    //                 .trigger(mach_timespec_t {
    //                     tv_sec: 0,
    //                     tv_nsec: 1000 * 10000, // 10ms
    //                 })
    //                 .unwrap(),
    //         );
    //     });
    // });

    let target_time = unsafe { mach_absolute_time() } + 10000000000;
    // c.bench_function("timer deadline", |c| {
    //     let clock = osx_timers::timer::Clock::new().unwrap();

    //     c.iter(|| {
    //         clock.arm_until(target_time).unwrap(); // Who knows :shrug:
    //         clock.cancel().unwrap();
    //     });
    // });
    // // mach_absolute_time = 5ns
    // c.bench_function("timer timeout", |c| {
    //     let clock = osx_timers::timer::Clock::new().unwrap();

    //     c.iter(|| {
    //         clock.arm_for(10000000).unwrap(); // Who knows :shrug:
    //         clock.cancel().unwrap();
    //     });
    // });
    let mut mk_arm_hist = Histogram::<u64>::new(3).unwrap();
    let mut mk_cancel_hist = Histogram::<u64>::new(3).unwrap();
    c.bench_function("timer: separate arm/cancel", |c| {
        let clock = osx_timers::timer::Clock::new().unwrap();

        c.iter(|| {
            let before_arm = unsafe { mach_absolute_time() };
            clock.arm_until(target_time).unwrap(); // Who knows :shrug:
            let after_arm = unsafe { mach_absolute_time() };
            clock.cancel().unwrap();
            let after_cancel = unsafe { mach_absolute_time() };

            mk_arm_hist.record(mabs_to_nsec(after_arm - before_arm)).unwrap();
            mk_cancel_hist.record(mabs_to_nsec(after_cancel - after_arm)).unwrap();
        });
    });

    let mut kq_arm_hist = Histogram::<u64>::new(3).unwrap();
    let mut kq_cancel_hist = Histogram::<u64>::new(3).unwrap();
    c.bench_function("kevent", |c| {
        let clock = osx_timers::kevent::Clock::new().unwrap();

        c.iter(|| {
            let before_arm = unsafe { mach_absolute_time() };
            let r = clock.trigger(42, target_time as i64).unwrap();
            let after_arm = unsafe { mach_absolute_time() };
            r.cancel().unwrap(); // 100 µs
            let after_cancel = unsafe { mach_absolute_time() };

            kq_arm_hist.record(mabs_to_nsec(after_arm - before_arm)).unwrap();
            kq_cancel_hist.record(mabs_to_nsec(after_cancel - after_arm)).unwrap();
        });
    });

    println!("mk_arm avg: {}", mk_arm_hist.mean());
    println!("mk_cancel avg: {}", mk_cancel_hist.mean());
    println!("kq_arm avg: {}", kq_arm_hist.mean());
    println!("kq_cancel avg: {}", kq_cancel_hist.mean());

    // let queue = Queue::new();
    // c.bench_function("dispatch", |c| {
    //     c.iter(|| {
    //         let timer = osx_timers::dispatch::Timer::new(&queue);
    //         timer.arm(10000000); // 10ms
    //         timer.cancel();
    //     });
    // });
}

criterion_group!(benches, bench);
criterion_main!(benches);
