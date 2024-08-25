use std::{collections::HashMap, process::Command, sync::Arc};

use anyhow::anyhow;
use axum::{
    body::Body,
    extract::Path,
    http::{HeaderValue, Method, StatusCode},
    response::IntoResponse,
    routing::get,
    Extension, Router,
};
use once_cell::sync::Lazy;
use tokio::net::TcpListener;
use tokio_util::io::ReaderStream;
use tower_http::cors::CorsLayer;
use tracing::{error, info};
use utils::qos::QosClass;

use crate::profiler::THREAD_NAME_TAG;

pub struct FirefoxApiServer {
    profile_paths: std::sync::RwLock<HashMap<String, String>>,
    listener_addr: std::sync::RwLock<Option<String>>,
}

impl FirefoxApiServer {
    fn new() -> Arc<Self> {
        Arc::new(FirefoxApiServer {
            profile_paths: std::sync::RwLock::new(HashMap::new()),
            listener_addr: std::sync::RwLock::new(None),
        })
    }

    async fn profile_handler(
        Extension(server): Extension<Arc<FirefoxApiServer>>,
        Path(profile_id): Path<String>,
    ) -> impl IntoResponse {
        let Some(path) = server
            .profile_paths
            .read()
            .unwrap()
            .get(&profile_id)
            .cloned()
        else {
            return Err((StatusCode::NOT_FOUND, "Profile not found".to_string()));
        };

        let file = match tokio::fs::File::open(path).await {
            Ok(file) => file,
            Err(_) => {
                return Err((
                    StatusCode::INTERNAL_SERVER_ERROR,
                    "Failed to open profile".to_string(),
                ))
            }
        };

        let stream = ReaderStream::new(file);
        let body = Body::from_stream(stream);
        Ok((StatusCode::OK, body))
    }

    async fn start(self: &Arc<Self>) -> anyhow::Result<()> {
        let app = Router::new()
            .route("/profile/:profile_id", get(Self::profile_handler))
            .layer(Extension(self.clone()))
            .layer(
                CorsLayer::new()
                    .allow_methods([Method::GET])
                    .allow_origin("https://profiler.firefox.com".parse::<HeaderValue>()?),
            );

        // use a random ephemeral port
        let listener = TcpListener::bind("localhost:0").await?;
        *self.listener_addr.write().unwrap() = Some(listener.local_addr()?.to_string());
        tokio::spawn(async move {
            if let Err(e) = axum::serve(listener, app).await {
                error!("Error running Firefox API server: {:?}", e);
            }
        });
        Ok(())
    }

    pub fn add_profile_path(&self, path: String) -> anyhow::Result<String> {
        // generate a random ID
        let profile_id = uuid::Uuid::new_v4().to_string() + ".json";
        self.profile_paths
            .write()
            .unwrap()
            .insert(profile_id.clone(), path);

        // return URL
        let addr = self.listener_addr.read().unwrap();
        let port = if let Some(addr) = addr.as_deref() {
            addr.split(':').last().unwrap()
        } else {
            return Err(anyhow!("Server not started"));
        };
        Ok(format!("http://localhost:{}/profile/{}", port, profile_id))
    }

    pub fn add_and_open_profile(&self, path: String) -> anyhow::Result<()> {
        let profile_url = self.add_profile_path(path)?;
        let url = format!(
            "https://profiler.firefox.com/from-url/{}/",
            urlencoding::encode(&profile_url)
        );

        info!("Open this profile URL: {}", url);

        // run the 'open' command
        // TODO: posix_spawn
        let _ = Command::new("open").arg(url).output()?;
        Ok(())
    }

    fn start_threaded(self: &Arc<Self>) {
        let server = self.clone();
        let rt = tokio::runtime::Builder::new_current_thread()
            .enable_all()
            .build()
            .unwrap();

        rt.block_on(async {
            if let Err(e) = server.start().await {
                error!("Error starting server: {:?}", e);
            } else {
                let addr = self.listener_addr.read().unwrap();
                info!(
                    "Firefox Profiler API server listening on {}",
                    addr.as_deref().unwrap_or("<unknown>")
                );
            }
        });

        // move it all to a background thread now that it's started
        std::thread::Builder::new()
            .name(format!("{}: API server", THREAD_NAME_TAG))
            .spawn(move || {
                utils::qos::set_thread_qos(QosClass::UserInitiated, None).unwrap();
                rt.block_on(futures::future::pending::<()>());
            })
            .unwrap();
    }

    pub fn shared() -> Arc<Self> {
        static INSTANCE: Lazy<Arc<FirefoxApiServer>> = Lazy::new(FirefoxApiServer::new);
        INSTANCE.start_threaded();
        INSTANCE.clone()
    }
}
