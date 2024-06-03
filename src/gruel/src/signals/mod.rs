#[macro_use]
mod waker_set;
pub use waker_set::*;

mod channel;
pub use channel::*;

mod multiplex;
pub use multiplex::*;

mod std_wakers;
pub use std_wakers::*;

mod mio;
pub use mio::*;

#[cfg(all(test, not(loom)))]
mod tests {
    use std::{sync::Barrier, thread, time::Duration};

    use crate::{
        DynamicallyBoundWaker, ParkSignalChannelExt, ParkWaker, QueueRecvSignalChannelExt,
        RawSignalChannel,
    };

    define_waker_set! {
        #[derive(Default)]
        struct MyWakerSet {
            parker: ParkWaker,
            dynamic: DynamicallyBoundWaker,
        }
    }

    #[test]
    fn simple_wake_up() {
        let start_barrier = Barrier::new(2);
        let channel = RawSignalChannel::new(MyWakerSet::default());

        std::thread::scope(|s| {
            s.spawn(|| {
                start_barrier.wait();
                channel.wait_on_park(u64::MAX);
                assert_eq!(channel.take(1), 1);
            });

            s.spawn(|| {
                start_barrier.wait();
                channel.assert(1);
            });
        });
    }

    #[test]
    fn early_exit_works() {
        let signal = RawSignalChannel::new(MyWakerSet::default());

        signal.assert(1);

        for _ in 0..1000 {
            signal.wait_on_park(u64::MAX);
        }
    }

    #[test]
    fn respects_timeouts() {
        let signal = RawSignalChannel::new(MyWakerSet::default());
        signal.wait_on_park_timeout(u64::MAX, Duration::from_millis(100));
    }

    #[test]
    fn queues_can_be_cancelled() {
        let (_send, recv) = crossbeam_channel::unbounded::<u32>();
        let signal = RawSignalChannel::new(MyWakerSet::default());

        thread::scope(|s| {
            s.spawn(|| {
                assert_eq!(
                    signal.recv_with_cancel(u64::MAX, &recv),
                    Err(crate::QueueRecvError::Cancelled)
                );
            });

            s.spawn(|| {
                // We'd like to exercise the cancellation behavior, ideally.
                thread::sleep(Duration::from_millis(100));
                signal.assert(1);
            });
        });
    }

    #[test]
    fn queues_can_still_receive() {
        let (send, recv) = crossbeam_channel::unbounded::<u32>();
        let signal = RawSignalChannel::new(MyWakerSet::default());

        thread::scope(|s| {
            s.spawn(|| {
                assert_eq!(signal.recv_with_cancel(u64::MAX, &recv), Ok(42));
            });

            s.spawn(|| {
                thread::sleep(Duration::from_millis(100));
                send.send(42).unwrap();
            });
        });
    }
}

#[cfg(all(loom, test))]
mod loom_tests {
    use super::*;

    use std::sync::{Arc, Barrier};

    define_waker_set! {
        #[derive(Default)]
        struct MyWakerSet {
            parker: ParkWaker,
        }
    }

    #[test]
    fn single_wake_up_loom() {
        loom::model(|| {
            let channel = Arc::new(RawSignalChannel::new(MyWakerSet::default()));

            loom::thread::spawn({
                let channel = channel.clone();

                move || {
                    channel.wait_on_park(0b1);
                    assert_eq!(channel.take(u64::MAX), 0b1);
                }
            });

            loom::thread::spawn({
                let channel = channel.clone();

                move || {
                    channel.assert(0b1);
                }
            });
        });
    }

    #[test]
    fn double_wake_up_loom() {
        loom::model(|| {
            let channel = Arc::new(RawSignalChannel::new(MyWakerSet::default()));

            loom::thread::spawn({
                let channel = channel.clone();

                move || {
                    channel.wait_on_park(0b1);
                    assert_eq!(channel.take(0b1), 0b1);

                    channel.wait_on_park(0b10);
                    assert_eq!(channel.take(0b10), 0b10);

                    assert_eq!(channel.take(u64::MAX), 0);
                }
            });

            loom::thread::spawn({
                let channel = channel.clone();

                move || {
                    channel.assert(0b1);
                    channel.assert(0b10);
                }
            });
        });
    }

    #[test]
    fn multi_source_wake_up_loom() {
        loom::model(|| {
            let channel = Arc::new(RawSignalChannel::new(MyWakerSet::default()));

            loom::thread::spawn({
                let channel = channel.clone();

                move || {
                    channel.wait_on_park(0b1);
                    assert_eq!(channel.take(0b1), 0b1);

                    channel.wait_on_park(0b10);
                    assert_eq!(channel.take(0b10), 0b10);

                    assert_eq!(channel.take(u64::MAX), 0);
                }
            });

            loom::thread::spawn({
                let channel = channel.clone();

                move || {
                    channel.assert(0b1);
                }
            });

            loom::thread::spawn({
                let channel = channel.clone();

                move || {
                    channel.assert(0b10);
                }
            });
        });
    }
}
