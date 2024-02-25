use wormholefs::client::client::WormholeFs;
use tracing::Level;
use tracing_subscriber::fmt::format::FmtSpan;

fn main() -> anyhow::Result<()> {
    tracing_subscriber::fmt()
        .with_span_events(FmtSpan::CLOSE)
        .with_max_level(Level::TRACE)
        .init();

    let args = std::env::args().collect::<Vec<_>>();
    let fuse_root = if args.len() > 3 { Some(args[3].as_str()) } else { None };
    let client = WormholeFs::new(args[1].as_str(), args[2].as_str(), fuse_root)?;

    client.read_fuse_events()
}
