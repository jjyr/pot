use anyhow::Result;
use pot_server::{Server, ServerConfig};
#[tokio::main]
async fn main() -> Result<()> {
    let server = Server::bind(ServerConfig::from_env()).await?;
    eprintln!("pot server listening on {}", server.local_addr()?);
    server
        .serve(async {
            let mut terminate =
                tokio::signal::unix::signal(tokio::signal::unix::SignalKind::terminate())
                    .expect("signal handler");
            tokio::select! { _ = tokio::signal::ctrl_c() => {}, _ = terminate.recv() => {} }
        })
        .await
}
