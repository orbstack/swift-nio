use anyhow::anyhow;
use axum::{
    response::IntoResponse,
    routing::{get, post},
    Extension, Json, Router,
};
use error::AppResult;
use std::{
    net::SocketAddr,
    sync::{Arc, Mutex},
};
use tokio::sync::mpsc::Sender;
use tower::ServiceBuilder;
use tracing::debug;

use crate::{
    action::SystemAction,
    filesystem::{DiskManager, HostDiskStats},
    startup,
};

mod chrony;
mod error;

#[derive(Clone, Debug)]
struct State {}

pub async fn spawn_server(disk_manager: Arc<Mutex<DiskManager>>, action_tx: Sender<SystemAction>) {
    tracing_subscriber::fmt::init();

    let state = State {};

    let app = Router::new()
        .route("/ping", get(ping))
        .route("/sys/shutdown", post(sys_shutdown))
        .route("/sys/sleep", post(sys_sleep))
        .route("/sys/wake", post(sys_wake))
        .route("/disk/report_stats", post(disk_report_stats))
        // for scon
        .route("/internal/sync_time", post(sync_time))
        .layer(
            ServiceBuilder::new()
                .layer(Extension(state))
                .layer(Extension(disk_manager))
                .layer(Extension(action_tx)),
        );

    // TCP
    // 0.250.250.2:103
    let addr = SocketAddr::from(([0, 250, 250, 2], 103));
    let tcp_listener = tokio::net::TcpListener::bind(addr).await.unwrap();
    let app_clone = app.clone();
    tokio::spawn(async move {
        axum::serve(tcp_listener, app_clone).await.unwrap();
    });

    // Unix socket
    let unix_listener = tokio::net::UnixListener::bind("/run/vinit.sock").unwrap();
    tokio::spawn(async move {
        axum::serve(unix_listener, app).await.unwrap();
    });
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

// report disk stats
async fn disk_report_stats(
    Extension(disk_manager): Extension<Arc<Mutex<DiskManager>>>,
    Json(payload): Json<HostDiskStats>,
) -> AppResult<impl IntoResponse> {
    debug!("disk_report_stats: {:?}", payload);
    disk_manager.lock().unwrap().update_with_stats(&payload)?;
    Ok(())
}

// host is about to sleep
async fn sys_sleep() -> AppResult<impl IntoResponse> {
    debug!("sys_sleep");

    // freeze all machines
    // only freezing the cgroup keeps scon (NFS upcalls), FUSE fpll servers, and the kernel nfsd alive, so that macOS doesn't complain about NFS timeouts during sleep
    std::fs::write("/sys/fs/cgroup/scon/container/cgroup.freeze", "1")
        .map_err(|e| anyhow!("failed to freeze machines: {}", e))?;

    // NOTE: if you add anything here, make sure to update vmgr:vclient/client.go
    // currently, it only calls this API if pause-on-sleep is enabled

    Ok(())
}

// host wake from sleep
async fn sys_wake() -> AppResult<impl IntoResponse> {
    debug!("sys_wake");

    // fix the time immediately by stepping
    // chrony doesn't like massive time difference
    startup::sync_clock(false).map_err(|e| anyhow!("failed to step clock: {}", e))?;

    // always unfreeze all machines, in case they were frozen and then the setting was disabled
    std::fs::write("/sys/fs/cgroup/scon/container/cgroup.freeze", "0")
        .map_err(|e| anyhow!("failed to unfreeze machines: {}", e))?;

    // then ask chrony to do a more precise fix
    // #chronyc -m 'burst 4/4' 'makestep 3.0 -1'
    // chronyc 'burst 4/4'
    chrony::send_burst_request(4, 4)
        .await
        .map_err(|e| anyhow!("failed to send command: {}", e))?;

    Ok(())
}

async fn sync_time() -> AppResult<impl IntoResponse> {
    debug!("sync_time");
    startup::sync_clock(false).map_err(|e| anyhow!("failed to step clock: {}", e))?;
    Ok(())
}
