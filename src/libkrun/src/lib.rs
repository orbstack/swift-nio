// exported to Go and Swift
#![allow(clippy::missing_safety_doc)]

use tracing::level_filters::LevelFilter;
use tracing_subscriber::{fmt::format::FmtSpan, EnvFilter};

mod fs;
pub mod machine;
mod network;
mod result;

fn init_logger_once() {
    use std::sync::Once;

    static INIT: Once = Once::new();

    INIT.call_once(|| {
        tracing_subscriber::fmt::fmt()
            .with_env_filter(
                EnvFilter::builder()
                    .with_default_directive(LevelFilter::INFO.into())
                    .from_env_lossy(),
            )
            .with_span_events(FmtSpan::CLOSE)
            .init();
    });
}
