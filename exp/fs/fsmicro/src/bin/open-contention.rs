use std::{ffi::CString, sync::{atomic::{AtomicBool, AtomicIsize, Ordering}, Arc}};

use hdrhistogram::{Histogram, SyncHistogram};
use nix::{fcntl::OFlag, sys::{stat::Mode, time::TimeValLike}, time::{clock_gettime, ClockId}};

fn now_ns() -> u64 {
    clock_gettime(ClockId::CLOCK_MONOTONIC).unwrap().num_nanoseconds() as u64
}

static TID: AtomicIsize = AtomicIsize::new(0);

fn spawn_worker(stop: Arc<AtomicBool>, open_sync_hist: &SyncHistogram<u64>, close_sync_hist: &SyncHistogram<u64>) -> std::thread::JoinHandle<()> {
    let mut open_recorder = open_sync_hist.recorder();
    let mut close_recorder = close_sync_hist.recorder();
    std::thread::spawn(move || {
        let fpath = CString::new(format!("a{}", TID.fetch_add(1, Ordering::Relaxed))).unwrap();
        loop {
            if stop.load(Ordering::Relaxed) {
                break;
            }

            let before_open = now_ns();
            let fd = nix::fcntl::open(fpath.as_ref(), OFlag::O_CREAT, Mode::from_bits_truncate(0o644)).unwrap();
            let after_open = now_ns();
            unsafe { nix::libc::close(fd) };
            let after_close = now_ns();

            open_recorder.record(after_open - before_open).unwrap();
            close_recorder.record(after_close - after_open).unwrap();
        }
    })
}

fn dump_histogram(label: &str, hist: &Histogram<u64>) {
    println!("\n\n-----------");
    println!("{}:", label);
    println!("  min = {}", hist.min());
    println!("  max = {}", hist.max());
    println!("  mean = {}", hist.mean());
    println!("  stddev = {}", hist.stdev());
    println!();
    println!("  p50 = {}", hist.value_at_quantile(0.5));
    println!("  p90 = {}", hist.value_at_quantile(0.9));
    println!("  p99 = {}", hist.value_at_quantile(0.99));
    println!("  p99.9 = {}", hist.value_at_quantile(0.999));
    println!();

    // for v in hist.iter_recorded() {
    //     println!("  p{} = {}  ({} samples)", v.percentile(), v.value_iterated_to(), v.count_at_value());
    // }
}

fn main() {
    let open_counter = Histogram::<u64>::new(2).unwrap();
    let mut open_sync_hist = SyncHistogram::from(open_counter);

    let close_counter = Histogram::<u64>::new(2).unwrap();
    let mut close_sync_hist = SyncHistogram::from(close_counter);

    let stop = Arc::new(AtomicBool::new(false));

    let num_workers = std::env::args().nth(1).unwrap_or_else(|| "2".to_string()).parse::<usize>().unwrap();
    let workers = (0..num_workers).map(|_| {
        spawn_worker(stop.clone(), &open_sync_hist, &close_sync_hist)
    }).collect::<Vec<_>>();

    std::thread::sleep(std::time::Duration::from_secs(5));

    stop.store(true, Ordering::Relaxed);

    for worker in workers {
        worker.join().unwrap();
    }

    open_sync_hist.refresh();
    close_sync_hist.refresh();

    dump_histogram("open", &open_sync_hist);
    dump_histogram("close", &close_sync_hist);
}
