use std::time::Duration;
use std::{fs::File, io::Write};

use nix::{fcntl::OFlag, sys::stat::Mode};
use tracing::{debug, error};

const MGLRU_AGING_INTERVAL: Duration = Duration::from_secs(45);

// a memccg id that always exists, to pass lookup
const MEMCG_ROOT: u64 = 1;

fn age_and_reclaim() -> anyhow::Result<()> {
    // be as efficient as possible with syscalls here
    let mut file = File::from(nix::fcntl::open(
        "/sys/kernel/debug/lru_gen",
        OFlag::O_RDWR,
        Mode::empty(),
    )?);

    // reclaim first, then age, so that we keep all 4 gens full
    // u64::MAX = custom hack to ignore seq
    // swappiness 0 = file only
    // can_swap=0 to skip anon
    // force_scan=0 to use bloom filter to skip
    // kernel is also hacked to iterate over all cgroups, which fixes the memcg id=0 issue (for unreferenced CSS page cache)
    // write it atomically in one call
    file.write_all(
        format!(
            "- {} 0 {} 0\n+ {} 0 {} 0 0",
            MEMCG_ROOT,
            u64::MAX,
            MEMCG_ROOT,
            u64::MAX
        )
        .as_bytes(),
    )?;

    Ok(())
}

pub async fn reclaim_worker() -> anyhow::Result<()> {
    loop {
        tokio::time::sleep(MGLRU_AGING_INTERVAL).await;
        debug!("Reclaiming memory");
        if let Err(e) = age_and_reclaim() {
            error!("Failed to reclaim memory: {}", e);
        }
    }
}
