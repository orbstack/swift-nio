use anyhow::{anyhow, Result};
use tokio::io::{self, AsyncReadExt, AsyncWriteExt};
use tokio::net::UnixStream;
use tokio::sync::{broadcast, mpsc};
use tokio::task;

#[tokio::main]
async fn main() -> Result<()> {
    let socket = UnixStream::connect("/rpc.sock")
        .await
        .map_err(|e| anyhow!("Could not connect to RPC socket: {}", e))?;

    let (mut socket_reader, mut socket_writer) = socket.into_split();
    let (shutdown_tx, _) = broadcast::channel(1);

    let to_socket = task::spawn({
        let mut socket_writer = socket_writer;
        let shutdown_tx = shutdown_tx.clone();
        let mut shutdown_rx = shutdown_tx.subscribe();
        async move {
            let mut stdin = io::stdin();
            let mut buf = [0u8; 1024];

            loop {
                tokio::select! {
                    result = stdin.read(&mut buf) => {
                        match result {
                            Ok(0) => {
                                socket_writer.shutdown().await?;
                                let _= shutdown_tx.send(());
                                break;
                            }
                            Ok(n) => {
                                socket_writer.write_all(&buf[..n]).await?;
                            }
                            Err(e) => {
                                return Err(anyhow!("Error reading from stdin: {}", e));
                            }
                        }
                    }
                    _ = shutdown_rx.recv() => {
                        break;
                    }
                }
            }

            Ok::<(), anyhow::Error>(())
        }
    });

    let from_socket = task::spawn({
        let mut socket_reader = socket_reader;
        let shutdown_tx = shutdown_tx.clone();
        let mut shutdown_rx = shutdown_tx.subscribe();
        async move {
            let mut stdout = io::stdout();
            let mut buf = [0u8; 1024];

            loop {
                tokio::select! {
                    result = socket_reader.read(&mut buf) => {
                        match result {
                            Ok(0) => {
                                let _ = shutdown_tx.send(());
                                break;
                            }
                            Ok(n) => {
                                stdout.write_all(&buf[..n]).await?;
                                stdout.flush().await?;
                            }
                            Err(e) => {
                                return Err(anyhow!("Error reading from socket: {}", e));
                            }
                        }
                    }
                    _ = shutdown_rx.recv() => {
                        break;
                    }
                }
            }

            Ok::<(), anyhow::Error>(())
        }
    });

    let (to_result, from_result) = tokio::join!(to_socket, from_socket);

    to_result??;
    from_result??;

    Ok(())
}
