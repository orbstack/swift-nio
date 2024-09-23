use tokio::sync::mpsc;
use tokio_stream::wrappers::ReceiverStream;
use tonic::{transport::Server, Request, Response, Status};

use wormhole::{
    wormhole_server::{Wormhole, WormholeServer},
    InputMessage, OutputMessage,
};

pub mod wormhole {
    tonic::include_proto!("wormhole");
}

#[derive(Debug, Default)]
pub struct WormholeService {}

#[tonic::async_trait]
impl Wormhole for WormholeService {
    type SendCommandStream = ReceiverStream<Result<OutputMessage, Status>>;

    async fn send_command(
        &self,
        request: Request<tonic::Streaming<InputMessage>>,
    ) -> Result<Response<Self::SendCommandStream>, Status> {
        let mut stream = request.into_inner();
        let (mut tx, rx) = mpsc::channel(4);

        tokio::spawn(async move {
            tx.send(Ok(OutputMessage {
                output: "hello from output streaming",
            }))
            .await
            .unwrap();
        });

        // Ok(Response::new(reply))
        // Ok(Response::new())
        unimplemented!()
    }
}

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    let addr = "[::1]:50051".parse()?;
    let wormhole = WormholeService::default();
    let svc = WormholeServer::new(wormhole);

    Server::builder().add_service(svc).serve(addr).await?;

    Ok(())
}
