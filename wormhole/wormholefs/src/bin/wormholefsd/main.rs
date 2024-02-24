use libc::{pthread_setschedparam, pthread_self, SCHED_FIFO, sched_param};
use wormholefs::client::client::WormholeFs;
use tracing::Level;
use tracing_subscriber::fmt::format::FmtSpan;

fn main() -> Result<(), Box<dyn std::error::Error>> {
    tracing_subscriber::fmt()
        .with_span_events(FmtSpan::CLOSE)
        .with_max_level(Level::TRACE)
        .init();

    let args = std::env::args().collect::<Vec<_>>();
    let client = WormholeFs::new(args[1].as_str(), args[2].as_str(), args[3].as_str())?;

    let param = sched_param {
        sched_priority: 1,
    };
    let ret = unsafe { pthread_setschedparam(pthread_self(), SCHED_FIFO, &param) };
    if ret != 0 {
        panic!("failed to set priority");
    }

    client.read_fuse_events()
}
