use std::{
    any::Any,
    fmt::{self, Write as _},
    io::Write as _,
    sync::{
        atomic::{AtomicU64, Ordering},
        Arc,
    },
    thread,
    time::{Duration, Instant},
};

use aho_corasick::AhoCorasick;
use once_cell::sync::OnceCell;

// === Counter Traits === //

pub struct InitializedCounter {
    pub counter: &'static dyn DynCounter,
    pub userdata: Box<dyn Any + Send>,
}

impl fmt::Debug for InitializedCounter {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("InitializedCounter").finish_non_exhaustive()
    }
}

impl fmt::Display for InitializedCounter {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        self.counter.display_raw(f, &*self.userdata)
    }
}

impl InitializedCounter {
    pub fn new(filter: &AhoCorasick, counter: &'static dyn DynCounter) -> Option<Self> {
        counter
            .init_raw(filter)
            .map(|userdata| Self { counter, userdata })
    }

    pub fn tick(&mut self, info: IntervalInfo) {
        self.counter.tick_raw(&mut *self.userdata, info);
    }
}

pub trait DynCounter: Send + Sync {
    fn init_raw(&self, filter: &AhoCorasick) -> Option<Box<dyn Any + Send>>;

    fn tick_raw(&self, userdata: &mut (dyn Any + Send), info: IntervalInfo);

    fn display_raw(&self, fmt: &mut fmt::Formatter<'_>, userdata: &(dyn Any + Send))
        -> fmt::Result;
}

impl<T: Counter> DynCounter for T {
    fn init_raw(&self, filter: &AhoCorasick) -> Option<Box<dyn Any + Send>> {
        self.init(filter)
            .map(|userdata| Box::<T::Userdata>::new(userdata) as Box<dyn Any + Send>)
    }

    fn tick_raw(&self, userdata: &mut (dyn Any + Send), info: IntervalInfo) {
        self.tick(userdata.downcast_mut::<T::Userdata>().unwrap(), info);
    }

    fn display_raw(&self, f: &mut fmt::Formatter<'_>, userdata: &(dyn Any + Send)) -> fmt::Result {
        self.display(f, userdata.downcast_ref::<T::Userdata>().unwrap())
    }
}

pub trait Counter: Sized + Send + Sync {
    type Userdata: 'static + Send;

    fn init(&self, filter: &AhoCorasick) -> Option<Self::Userdata>;

    fn tick(&self, userdata: &mut Self::Userdata, info: IntervalInfo);

    fn display(&self, f: &mut fmt::Formatter, userdata: &Self::Userdata) -> fmt::Result;
}

pub trait DisableableCounter: Counter {
    type Dummy;
}

// === Basic Counters === //

// DummyCounter
#[derive(Debug, Default)]
#[non_exhaustive]
pub struct DummyCounter;

impl DummyCounter {
    pub const fn new() -> Self {
        Self
    }

    pub fn count(&self) {}
}

impl Counter for DummyCounter {
    type Userdata = ();

    fn init(&self, _filter: &AhoCorasick) -> Option<Self::Userdata> {
        None
    }

    fn tick(&self, _userdata: &mut Self::Userdata, _info: IntervalInfo) {
        // (no-op)
    }

    fn display(&self, f: &mut fmt::Formatter<'_>, _userdata: &Self::Userdata) -> fmt::Result {
        f.write_str("[disabled]")
    }
}

// TotalCounter
#[derive(Debug, Default)]
pub struct TotalCounter(&'static str, AtomicU64);

impl TotalCounter {
    pub const fn new(name: &'static str) -> Self {
        Self(name, AtomicU64::new(0))
    }

    pub fn count(&self) {
        self.1.fetch_add(1, Ordering::Relaxed);
    }
}

impl Counter for TotalCounter {
    type Userdata = ();

    fn init(&self, filter: &AhoCorasick) -> Option<Self::Userdata> {
        filter.find(self.0).map(|_| ())
    }

    fn tick(&self, _userdata: &mut Self::Userdata, _info: IntervalInfo) {
        // (no-op)
    }

    fn display(&self, f: &mut fmt::Formatter, _userdata: &Self::Userdata) -> fmt::Result {
        write!(f, "{} = {}", self.0, self.1.load(Ordering::Relaxed))
    }
}

impl DisableableCounter for TotalCounter {
    type Dummy = DummyCounter;
}

// RateCounter
#[derive(Debug, Default)]
pub struct RateCounter(&'static str, AtomicU64);

impl RateCounter {
    pub const fn new(name: &'static str) -> Self {
        Self(name, AtomicU64::new(0))
    }

    pub fn count(&self) {
        self.1.fetch_add(1, Ordering::Relaxed);
    }
}

impl Counter for RateCounter {
    type Userdata = (u64, f64);

    fn init(&self, filter: &AhoCorasick) -> Option<Self::Userdata> {
        filter
            .find(self.0)
            .map(|_| (self.1.load(Ordering::Relaxed), f64::NAN))
    }

    fn tick(&self, (start_count, snap): &mut Self::Userdata, info: IntervalInfo) {
        let count = self.1.load(Ordering::Relaxed) - *start_count;
        *snap = count as f64 / info.since_start.as_secs_f64();
    }

    fn display(&self, f: &mut fmt::Formatter, (_, snap): &Self::Userdata) -> fmt::Result {
        let total = self.1.load(Ordering::Relaxed);

        if snap.is_nan() {
            write!(f, "{} = [unknown] (total: {total})", self.0)
        } else {
            write!(f, "{} = {snap}/s (total: {total})", self.0)
        }
    }
}

impl DisableableCounter for RateCounter {
    type Dummy = DummyCounter;
}

// === Registry === //

#[doc(hidden)]
pub mod global_counter_inner {
    pub use crate::{DisableableCounter, DynCounter};

    pub use linkme::distributed_slice;

    #[linkme::distributed_slice]
    pub static COUNTERS: [&'static dyn crate::DynCounter];
}

#[macro_export]
macro_rules! counter {
    ($(
        $(#[$attr:meta])*
        $vis:vis $name:ident $(where($($cfg:tt)*))? : $ty:ty = $init:expr;
    )*) => {$(
        $(#[$attr])*
        $(#[cfg($($cfg)*)])?
        $vis static $name: $ty = {
            #[$crate::global_counter_inner::distributed_slice($crate::global_counter_inner::COUNTERS)]
            static MY_COUNTER: &'static dyn $crate::global_counter_inner::DynCounter = &$name;

            $init
        };


        $(
            #[cfg(not($($cfg)*))]
            $vis static $name: <$ty as $crate::global_counter_inner::DisableableCounter>::Dummy =
                <<$ty as $crate::global_counter_inner::DisableableCounter>::Dummy>::new();
        )?
    )*};
}

pub fn counters() -> &'static [&'static dyn DynCounter] {
    &global_counter_inner::COUNTERS
}

pub fn counters_init(filter: &AhoCorasick) -> impl Iterator<Item = InitializedCounter> + '_ {
    counters()
        .iter()
        .filter_map(|&counter| InitializedCounter::new(filter, counter))
}

pub fn default_env_filter() -> &'static AhoCorasick {
    static FILTER: OnceCell<AhoCorasick> = OnceCell::new();

    FILTER.get_or_init(|| {
        AhoCorasick::new(
            std::env::var("RUST_COUNTERS")
                .unwrap_or_default()
                .split(','),
        )
        .unwrap()
    })
}

// === Displays === //

#[derive(Debug, Clone)]
#[allow(dead_code)]
#[must_use]
pub struct RunAtInterval(Arc<()>);

impl RunAtInterval {
    pub fn new(interval: Duration, mut f: impl 'static + Send + FnMut(IntervalInfo)) -> Self {
        let canceller = Arc::new(());
        let weak_canceller = Arc::downgrade(&canceller);

        thread::spawn(move || {
            let start = Instant::now();
            let mut prev = Instant::now();
            while weak_canceller.strong_count() > 0 {
                let now = Instant::now();
                let elapsed = now.duration_since(prev);
                prev = now;

                f(IntervalInfo {
                    since_start: now.duration_since(start),
                    since_last: elapsed,
                });

                // TODO: We might need to use an adaptive sleep mechanism for this.
                thread::sleep(interval);
            }
        });

        Self(canceller)
    }
}

#[derive(Debug, Copy, Clone)]
#[non_exhaustive]
pub struct IntervalInfo {
    pub since_start: Duration,
    pub since_last: Duration,
}

pub fn display_now(filter: &AhoCorasick) {
    for counter in counters_init(filter) {
        eprintln!("{counter}");
    }
}

pub fn display_every(filter: &AhoCorasick, interval: Duration) -> RunAtInterval {
    let mut counters = counters_init(filter).collect::<Vec<_>>();

    RunAtInterval::new(interval, move |duration| {
        let mut builder = String::new();
        builder.push_str("\n====== COUNTERS ======\n");
        if counters.is_empty() {
            builder.push_str("no counters to display\n");
        }
        for counter in &mut counters {
            counter.tick(duration);
            writeln!(&mut builder, "{counter}").unwrap();
        }
        builder.push_str("======================\n");

        let mut stderr = std::io::stderr().lock();
        stderr.write_all(builder.as_bytes()).unwrap();
        stderr.flush().unwrap();
    })
}
