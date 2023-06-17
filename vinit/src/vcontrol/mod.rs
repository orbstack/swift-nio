use anyhow::anyhow;
use axum::{
    routing::{get, post},
    response::IntoResponse,
    Json, Router, Extension,
};
use error::AppResult;
use nix::sys::statvfs;
use serde::{Deserialize, Serialize};
use tokio::{process::Command, sync::Mutex};
use tower::ServiceBuilder;
use tracing::{info, debug};
use std::{net::SocketAddr, sync::Arc};

mod error;

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
        let output = Command::new("btrfs")
            .arg("qgroup").arg("limit")
            .arg(format!("{}", new_size))
            .arg("/data")
            .output().await?;
        if !output.status.success() {
            return Err(anyhow!("failed to update quota: {}", String::from_utf8_lossy(&output.stderr)).into());
        }

        Ok(())
    }
}

pub async fn server_main() {
    tracing_subscriber::fmt::init();

    let state = State {};
    let disk_manager = DiskManager::new().unwrap();

    let app = Router::new()
        .route("/ping", get(ping))
        .route("/sys/sync", post(sys_sync))
        .route("/sys/shutdown", post(sys_shutdown))
        .route("/sys/emergency_shutdown", post(sys_emergency_shutdown))
        .route("/disk/report_stats", post(disk_report_stats))
        .route("/time/sync", post(time_sync))
        .layer(
            ServiceBuilder::new()
                .layer(Extension(state))
                .layer(Extension(Arc::new(Mutex::new(disk_manager))))
        );

    // 100.115.92.2:103
    let addr = SocketAddr::from(([198, 19, 248, 2], 103));
    info!("starting");
    axum::Server::bind(&addr)
        .serve(app.into_make_service())
        .await
        .unwrap();
}

async fn ping() -> impl IntoResponse {
    "pong"
}

// sync system
async fn sys_sync() -> AppResult<impl IntoResponse> {
    info!("sys_sync");
    Command::new("sync").arg("-f").arg("/data").output().await?;
    Ok(())
}

// shutdown system
async fn sys_shutdown() -> AppResult<impl IntoResponse> {
    info!("sys_shutdown");

    tokio::spawn(async {
        // don't cut off connection
        tokio::time::sleep(std::time::Duration::from_millis(100)).await;
        // shutdown
        let _ = Command::new("poweroff").spawn();
    });

    Ok(())
}

// emergency shutdown system
async fn sys_emergency_shutdown() -> AppResult<impl IntoResponse> {
    info!("sys_emergency_shutdown");

    // sync
    Command::new("sync").output().await?;

    // shutdown, bypass init (connection may be cut off)
    Command::new("poweroff").arg("-f").spawn()?;

    Ok(())
}

// btrfs doesn't really have this much overhead
const BASE_FS_OVERHEAD: u64 = 100 * 1024 * 1024; // 100MiB

// report disk stats
async fn disk_report_stats(
    Extension(disk_manager): Extension<Arc<Mutex<DiskManager>>>,
    Json(payload): Json<DiskReportStats>,
) -> AppResult<impl IntoResponse> {
    debug!("disk_report_stats: {:?}", payload);
    let DiskReportStats { host_fs_free, data_img_size, .. } = payload;
    let mut disk_manager = disk_manager.lock().await;

    let guest_statfs = statvfs::statvfs("/data")?;
    let guest_fs_size = guest_statfs.blocks() * guest_statfs.fragment_size();
    let guest_free = guest_statfs.blocks_free() * guest_statfs.fragment_size();

    // Total free space for data img on host
    let total_host_free = host_fs_free + data_img_size;
    let max_fs_size = (total_host_free as f64) * 0.97;
    // Subtract FS overhead
    let max_data_size = max_fs_size * 0.99 - (BASE_FS_OVERHEAD as f64);
    let max_data_size = max_data_size.round() as u64;

    // For quota, just use that size.

    // Don't limit it more than currently used.
    let guest_used = guest_fs_size - guest_free;
    let max_data_size = max_data_size.max(guest_used);

    //info!("guest_fs_size={} guest_free={} total_host_free={} max_fs_size={} max_data_size={} guest_used={}", guest_fs_size, guest_free, total_host_free, max_fs_size, max_data_size, guest_used);
    disk_manager.update_quota(max_data_size).await?;

    Ok(())
}

// sync time
async fn time_sync() -> AppResult<impl IntoResponse> {
    debug!("time_sync");

    Command::new("chronyc").arg("offline")
        .output()
        .await?;

    Command::new("chronyc").arg("online")
        .output()
        .await?;

    Ok(())
}
