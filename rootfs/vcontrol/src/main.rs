use anyhow::anyhow;
use axum::{
    routing::{get, post},
    response::{IntoResponse, Response},
    Json, Router, http::{Request, StatusCode}, middleware::{Next, self}, Extension,
};
use error::AppResult;
use nix::{fcntl::{fallocate, OFlag, self, FallocateFlags}, sys::{stat::Mode, statvfs}, unistd::ftruncate};
use serde::{Deserialize, Serialize};
use tokio::{process::Command, sync::Mutex};
use tower::ServiceBuilder;
use tracing::{info, error, debug};
use std::{net::SocketAddr, sync::{Arc}, path::Path};

mod error;

#[derive(Serialize, Deserialize, Clone, Debug)]
struct NetStartPortForward {
    port: u32,
}

#[derive(Serialize, Deserialize, Clone, Debug)]
#[serde(rename_all = "camelCase")]
struct UsbDeviceInfo {
    bus_id: String,
    // for lxd
    vid: String,
    pid: String,
}

#[derive(Serialize, Deserialize, Clone, Debug)]
#[serde(rename_all = "camelCase")]
struct UsbAttachDevice {
    device: UsbDeviceInfo,
    vsock_port: u32,
}

#[derive(Serialize, Deserialize, Clone, Debug)]
#[serde(rename_all = "camelCase")]
struct UsbDetachDevice {
    device: UsbDeviceInfo,
    vsock_port: u32,
}

#[derive(Serialize, Deserialize, Clone, Debug)]
#[serde(rename_all = "camelCase")]
struct SysRunCommand {
    command: String,
    args: Vec<String>,
}

#[derive(Serialize, Deserialize, Clone, Debug)]
#[serde(rename_all = "camelCase")]
struct SysRunCommandResponse {
    exit_code: i32,
    stdout: String,
    stderr: String,
}

#[derive(Serialize, Deserialize, Clone, Debug)]
#[serde(rename_all = "camelCase")]
struct DiskReportStats {
    host_fs_size: u64,
    host_fs_free: u64,
    data_img_size: u64,
}

#[derive(Clone, Debug)]
struct State {
    token: String,
}

#[derive(Clone, Debug)]
struct DiskManager {
    balloon_fd: i32,
    balloon_size: u64,
}

impl DiskManager {
    fn new() -> AppResult<Self> {
        Ok(Self {
            balloon_fd: -1,
            balloon_size: 0,
        })
    }

    fn update_balloon_size(&mut self, new_size: u64) -> AppResult<()> {
        if self.balloon_fd == -1 {
            if !Path::new("/tmp/flags/data_resized").exists() {
                return Err(anyhow!("data not ready").into());
            }

            self.balloon_fd = fcntl::open(
                "/data/balloon",
                OFlag::O_WRONLY | OFlag::O_CREAT | OFlag::O_TRUNC,
                Mode::S_IRUSR | Mode::S_IWUSR
            )?;
        }

        if new_size == self.balloon_size {
            // do nothing
        } else if new_size > self.balloon_size {
            fallocate(self.balloon_fd, FallocateFlags::empty(), self.balloon_size as i64, (new_size - self.balloon_size) as i64)?;
        } else {
            ftruncate(self.balloon_fd, new_size as i64)?;
        }

        self.balloon_size = new_size;
        Ok(())
    }

    async fn update_quota(&mut self, new_size: u64) -> AppResult<()> {
        // wait for data ready and clear balloon
        // we've never had balloon on here
        //self.update_balloon_size(0)?;

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

async fn auth<B>(req: Request<B>, next: Next<B>) -> Result<Response, StatusCode> {
    let state: &State = req.extensions().get().unwrap();

    let auth_header = req.headers()
        .get("Authorization")
        .and_then(|header| header.to_str().ok());

    match auth_header {
        Some(auth_header) if auth_header == state.token => {
            Ok(next.run(req).await)
        }
        _ => Err(StatusCode::UNAUTHORIZED),
    }
}

#[tokio::main]
async fn main() {
    tracing_subscriber::fmt::init();

    let args: Vec<String> = std::env::args().collect();
    let state = State {
        token: args[1].clone(),
    };
    let disk_manager = DiskManager::new().unwrap();

    let app = Router::new()
        .route("/ping", get(ping))
        .route("/flag/data_resized", get(flag_data_resized))
        .route("/usb/attach_device", post(usb_attach_device))
        .route("/usb/detach_device", post(usb_detach_device))
        .route("/sys/sync", post(sys_sync))
        .route("/sys/shutdown", post(sys_shutdown))
        .route("/sys/emergency_shutdown", post(sys_emergency_shutdown))
        .route("/disk/report_stats", post(disk_report_stats))
        .route("/time/sync", post(time_sync))
        .layer(
            ServiceBuilder::new()
                .layer(Extension(state))
                .layer(Extension(Arc::new(Mutex::new(disk_manager))))
                .layer(middleware::from_fn(auth))
        );

    let addr = SocketAddr::from(([172, 30, 30, 2], 103));
    info!("listening on {}", addr);
    axum::Server::bind(&addr)
        .serve(app.into_make_service())
        .await
        .unwrap();
}

async fn ping() -> impl IntoResponse {
    "pong"
}

// attach usb device to usbip
async fn usb_attach_device(
    Json(payload): Json<UsbAttachDevice>,
) -> AppResult<impl IntoResponse> {
    info!("usb_attach_device: {:?}", payload);
    let UsbDeviceInfo { bus_id, .. } = payload.device;
    let vsock_port = payload.vsock_port;

    // usbip
    Command::new("/opt/vc/usbip/bin/usbip")
        .arg("attach")
        .arg("-r")
        .arg(vsock_port.to_string()) // TODO unix socket
        .arg("-b")
        .arg(&bus_id)
        .output()
        .await?;

    Ok(())
}

// detach usb device from usbip
async fn usb_detach_device(
    Json(payload): Json<UsbDetachDevice>,
) -> AppResult<impl IntoResponse> {
    info!("usb_detach_device: {:?}", payload);
    let UsbDeviceInfo { bus_id, .. } = payload.device;
    let vsock_port = payload.vsock_port;

    // usbip
    Command::new("/opt/vc/usbip/bin/usbip")
        .arg("detach")
        .arg("-r")
        .arg(vsock_port.to_string())
        .arg("-b")
        .arg(&bus_id)
        .output()
        .await?;

    Ok(())
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
const USE_QUOTA: bool = true;

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

    if USE_QUOTA {
        // For quota, just use that size.

        // Don't limit it more than currently used.
        let guest_used = guest_fs_size - guest_free;
        let max_data_size = max_data_size.max(guest_used);

        //info!("guest_fs_size={} guest_free={} total_host_free={} max_fs_size={} max_data_size={} guest_used={}", guest_fs_size, guest_free, total_host_free, max_fs_size, max_data_size, guest_used);
        disk_manager.update_quota(max_data_size).await?;
    } else {
        // That's the size we want to be available for data.
        // Subtract from guest FS size to get balloon size.
        let balloon_size = guest_fs_size - max_data_size;

        // Make sure we don't exceed fs size
        let old_balloon_size = disk_manager.balloon_size;
        let max_balloon_size = old_balloon_size + guest_free;
        let balloon_size = balloon_size.min(max_balloon_size);

        //info!("guest_fs_size={} guest_free={} total_host_free={} max_fs_size={} max_data_size={} balloon_size={} old_balloon_size={} max_balloon_size={}", guest_fs_size, guest_free, total_host_free, max_fs_size, max_data_size, balloon_size, old_balloon_size, max_balloon_size);
        disk_manager.update_balloon_size(balloon_size)?;
    }

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

// flag_data_resized
async fn flag_data_resized() -> AppResult<impl IntoResponse> {
    info!("flag_data_resized");

    if !Path::new("/tmp/flags/data_resized").exists() {
        return Err(anyhow!("data not ready").into());
    }

    Ok(())
}
