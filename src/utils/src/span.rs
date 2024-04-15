use std::{
    collections::HashMap,
    sync::Mutex,
    time::{Duration, Instant},
};

use log::info;
use once_cell::sync::Lazy;

// TODO: EWMA / simple moving average?
struct TagMetrics {
    count: usize,
    total_elapsed: Duration,
}

static TAG_METRICS: Lazy<Mutex<HashMap<String, TagMetrics>>> =
    Lazy::new(|| Mutex::new(HashMap::new()));

// simple span for profiling, in lieu of tracing::span
pub struct Span {
    started_at: Instant,
    tag: String,
}

impl Span {
    pub fn new(tag: &str) -> Span {
        Span {
            started_at: Instant::now(),
            tag: tag.to_string(),
        }
    }
}

impl Drop for Span {
    fn drop(&mut self) {
        let elapsed = self.started_at.elapsed();

        // record metrics
        let mut metrics = TAG_METRICS.lock().unwrap();
        let metrics = metrics.entry(self.tag.clone()).or_insert(TagMetrics {
            count: 0,
            total_elapsed: Duration::new(0, 0),
        });

        metrics.count += 1;
        metrics.total_elapsed += elapsed;

        let avg_elapsed = metrics.total_elapsed / metrics.count as u32;

        info!("{}= {:?}      (avg= {:?})", self.tag, elapsed, avg_elapsed);
    }
}
