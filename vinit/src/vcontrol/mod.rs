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

pub async fn server_main(disk_manager: Arc<Mutex<DiskManager>>, action_tx: Sender<SystemAction>) {
    tracing_subscriber::fmt::init();

    let state = State {};

    let app = Router::new()
        .route("/ping", get(ping))
        .route("/sys/shutdown", post(sys_shutdown))
        .route("/sys/wake", post(sys_wake))
        .route("/disk/report_stats", post(disk_report_stats))
        .layer(
            ServiceBuilder::new()
                .layer(Extension(state))
                .layer(Extension(disk_manager))
                .layer(Extension(action_tx)),
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

// report disk stats
async fn disk_report_stats(
    Extension(disk_manager): Extension<Arc<Mutex<DiskManager>>>,
    Json(payload): Json<HostDiskStats>,
) -> AppResult<impl IntoResponse> {
    debug!("disk_report_stats: {:?}", payload);
    disk_manager.lock().unwrap().update_with_stats(&payload)?;
    Ok(())
}

// host wake from sleep
async fn sys_wake() -> AppResult<impl IntoResponse> {
    debug!("sys_wake");

    // fix the time immediately by stepping
    // chrony doesn't like massive time difference
    startup::sync_clock(false).map_err(|e| anyhow!("failed to step clock: {}", e))?;

    // then ask chrony to do a more precise fix
    // #chronyc -m 'burst 4/4' 'makestep 3.0 -1'
    // chronyc 'burst 4/4'
    chrony::send_burst_request(4, 4)
        .await
        .map_err(|e| anyhow!("failed to send command: {}", e))?;

    Ok(())
}
