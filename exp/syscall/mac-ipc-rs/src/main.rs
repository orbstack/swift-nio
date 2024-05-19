use std::{error::Error, sync::{atomic::{AtomicU64, Ordering}, Arc}, time::{Duration, Instant}};

use mio::{Events, Poll, Token, Waker};
use tokio::{io::{AsyncReadExt, AsyncWriteExt}, net::unix::pipe::pipe, sync::Notify};

const NS: u64 = 1;
const US: u64 = 1000 * NS;
const MS: u64 = 1000 * US;
const SEC: u64 = 1000 * MS;

const DURATION: u64 = 10 * SEC;
const BUCKET_SIZE: usize = 3;

const WAKE_TOKEN: Token = Token(12);

fn now(ref_instant: Instant) -> u64 {
    // get current monotonic time in nanoseconds
    let elapsed = ref_instant.elapsed();
    elapsed.as_nanos() as u64
}

fn main() -> Result<(), Box<dyn Error>> {
    let start = Instant::now();
    // 0 = abort
    let last_ts = Arc::new(AtomicU64::new(0));
    let last_ts_clone = last_ts.clone();

    let mut poll = Poll::new()?;
    let waker = Arc::new(Waker::new(poll.registry(), WAKE_TOKEN)?);

    // spawn reader
    let handle = std::thread::spawn(move || {
        let last_ts = last_ts_clone;

        let mut buckets = vec![0u64; 65536];
        let mut total_lat: u64 = 0;
        let mut iters: u64 = 0;

        loop {
            // let mut events = Events::with_capacity(2);
            // poll.poll(&mut events, None).unwrap();
            // let waker_event = events.iter().next().unwrap();
            std::thread::park();
            let send_ts = last_ts.load(Ordering::Relaxed);
            if send_ts == 0 {
                break;
            }
            let recv_ts = now(start);
            let latency = (recv_ts - send_ts) / 1000;
            total_lat += latency;
            iters += 1;
            let bucket: usize = latency as usize / BUCKET_SIZE;
            if bucket < 65536 {
                buckets[bucket] += 1;
            } else {
                buckets[65535] += 1;
            }
        }

        // print avg
        println!("avg latency: {}", total_lat / iters);

        // print median
        let mut sum = 0;
        for i in 0..65536 {
            sum += buckets[i];
            if sum > iters / 2 {
                println!("median latency: {}", i * BUCKET_SIZE);
                break;
            }
        }

        println!();

        // print buckets
        for i in 0..65536 {
            if buckets[i] > 1 {
                println!("{}-{}: {}", i * BUCKET_SIZE, (i + 1) * BUCKET_SIZE, buckets[i]);
            }
        }
    });

    let mut total_wake_time = 0;
    let mut total_wake_iters = 0;
    let thread = handle.thread();
    loop {
        let send_ts = now(start);
        if send_ts > DURATION {
            break;
        }
        last_ts.store(send_ts, Ordering::Relaxed);
        let before_wake = now(start);
        thread.unpark();
        let after_wake = now(start);
        total_wake_time += after_wake - before_wake;
        total_wake_iters += 1;
        std::thread::sleep(Duration::from_millis(1));
    }

    // stop
    last_ts.store(0, Ordering::Relaxed);
    thread.unpark();

    handle.join().unwrap();

    println!("avg wake time: {} ns", total_wake_time / total_wake_iters);

    Ok(())
}
