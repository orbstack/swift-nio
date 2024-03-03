use anyhow::anyhow;
use axum::{
    routing::{get, post},
    response::IntoResponse,
    Json, Router, Extension,
};
use error::AppResult;
use nix::sys::statvfs;
use serde::{Deserialize, Serialize};
use tokio::sync::{Mutex, mpsc::Sender};
use tower::ServiceBuilder;
use tracing::debug;
use std::{net::SocketAddr, sync::Arc, fs::File, os::fd::AsRawFd};

use crate::{action::SystemAction, startup};

mod error;
mod btrfs;
mod chrony;

#[derive(Serialize, Deserialize, Clone, Debug)]
#[serde(rename_all = "camelCase")]
struct DiskReportStats {
    host_fs_size: u64,
    host_fs_free: u64,
    data_img_size: u64,
}

#[derive(Clone, Debug)]
struct State {
}

#[derive(Clone, Debug)]
struct DiskManager {
}

impl DiskManager {
    fn new() -> AppResult<Self> {
        Ok(Self {})
    }

    async fn update_quota(&mut self, new_size: u64) -> AppResult<()> {
        let dir_file = File::open("/data")?;

        // sets top-level dir quota
        let mut args = btrfs::btrfs_ioctl_qgroup_limit_args {
            qgroupid: 0,
            lim: btrfs::btrfs_qgroup_limit {
                flags: btrfs::BTRFS_QGROUP_LIMIT_MAX_RFER as u64,
                max_rfer: new_size,
                max_excl: 0,
                rsv_rfer: 0,
                rsv_excl: 0,
            },
        };
        unsafe {
            btrfs::ioctl::qgroup_limit(dir_file.as_raw_fd(), &mut args)?;
        };

        Ok(())
    }
}

pub async fn server_main(action_tx: Sender<SystemAction>) {
    tracing_subscriber::fmt::init();

    let state = State {};
    let disk_manager = DiskManager::new().unwrap();

    let app = Router::new()
        .route("/ping", get(ping))
        .route("/sys/shutdown", post(sys_shutdown))
        .route("/sys/wake", post(sys_wake))
        .route("/disk/report_stats", post(disk_report_stats))
        .layer(
            ServiceBuilder::new()
                .layer(Extension(state))
                .layer(Extension(Arc::new(Mutex::new(disk_manager))))
                .layer(Extension(action_tx))
        );

    // 100.115.92.2:103
    let addr = SocketAddr::from(([198, 19, 248, 2], 103));
    axum::Server::bind(&addr)
        .serve(app.into_make_service())
        .await
        .unwrap();
}

async fn ping() -> impl IntoResponse {
    ""
}

// shutdown system
async fn sys_shutdown(
    Extension(action_tx): Extension<Sender<SystemAction>>,
) -> AppResult<impl IntoResponse> {
    debug!("sys_shutdown");
    action_tx.send(SystemAction::Shutdown).await?;
    Ok(())
}

// btrfs doesn't really have this much overhead
const BASE_FS_OVERHEAD: u64 = 100 * 1024 * 1024; // 100MiB
// can't use more than 95% of the host's free space
const MAX_HOST_FS_PERCENT: u64 = 95;
// can't boot without free space for scon db. leave some - I/O error + R/O remount is better than no boot
const MIN_FREE_SPACE: u64 = 2 * 1024 * 1024; // 2 MiB

// report disk stats
async fn disk_report_stats(
    Extension(disk_manager): Extension<Arc<Mutex<DiskManager>>>,
    Json(payload): Json<DiskReportStats>,
) -> AppResult<impl IntoResponse> {
    debug!("disk_report_stats: {:?}", payload);
    let DiskReportStats { host_fs_free, data_img_size, .. } = payload;
    let mut disk_manager = disk_manager.lock().await;

    let guest_statfs = statvfs::statvfs("/data")?;
    // (blocks - free) = df
    // (blocks - avail) = matches qgroup rfer, when we have quota statfs
    let guest_fs_size = guest_statfs.blocks() * guest_statfs.block_size();
    let guest_free = guest_statfs.blocks_available() * guest_statfs.block_size();

    // Total free space for data img on host
    // = 95% of free space, plus existing data img size
    // can't take 95% of the sum, because it's possible that data img size > free space
    let max_fs_size = (host_fs_free * MAX_HOST_FS_PERCENT / 100) + data_img_size;
    // Subtract FS overhead - we're setting FS quota, not disk img size limit
    // so FS limit should be a bit lower than the disk img
    let max_data_size = max_fs_size - BASE_FS_OVERHEAD;

    // For quota, just use that size.

    // Don't limit it more than currently used (according to qgroup)
    let guest_used = guest_fs_size - guest_free;
    // prevent ENOSPC boot failure by always leaving a bit of free space
    let max_data_size = max_data_size.max(guest_used) + MIN_FREE_SPACE;

    //info!("guest_fs_size={} guest_free={} total_host_free={} max_fs_size={} max_data_size={} guest_used={}", guest_fs_size, guest_free, total_host_free, max_fs_size, max_data_size, guest_used);
    disk_manager.update_quota(max_data_size).await?;

    Ok(())
}

// host wake from sleep
async fn sys_wake() -> AppResult<impl IntoResponse> {
    debug!("sys_wake");

    // fix the time immediately by stepping
    // chrony doesn't like massive time difference
    startup::sync_clock(false)
        .map_err(|e| anyhow!("failed to step clock: {}", e))?;

    // then ask chrony to do a more precise fix
    // #chronyc -m 'burst 4/4' 'makestep 3.0 -1'
    // chronyc 'burst 4/4'
    chrony::send_burst_request(4, 4).await
        .map_err(|e| anyhow!("failed to send command: {}", e))?;

    Ok(())
}
