//! App-owned lifecycle adapter; routing never follows the selected UI Space.
use anyhow::{Result, ensure};
use futures::future::BoxFuture;
use pot_protocol::message::*;
use pot_relay::Endpoint;
use pot_server::ClientTransport;
use serde::de::DeserializeOwned;

pub(crate) struct Connection {
    pub id: String,
    pub client: ClientTransport,
}
impl Connection {
    async fn call<T: DeserializeOwned>(&self, request: RelayRequest) -> Result<T> {
        let response = tokio::time::timeout(
            std::time::Duration::from_secs(15),
            self.client
                .request(API, Some(serde_json::to_value(request)?)),
        )
        .await??;
        ensure!(
            response.status.is_success(),
            "Message endpoint rejected request ({})",
            response.status
        );
        Ok(serde_json::from_slice(&response.body)?)
    }
}
impl Endpoint for Connection {
    fn control_id(&self) -> &str {
        &self.id
    }
    fn directory(&self) -> BoxFuture<'_, Result<Directory>> {
        Box::pin(self.call(RelayRequest::Directory))
    }
    fn register(&self, directory: Directory) -> BoxFuture<'_, Result<()>> {
        Box::pin(async move {
            self.call::<serde_json::Value>(RelayRequest::Register { directory })
                .await?;
            Ok(())
        })
    }
    fn pending(&self, target_control_id: String) -> BoxFuture<'_, Result<Vec<Envelope>>> {
        Box::pin(self.call(RelayRequest::Pending { target_control_id }))
    }
    fn deliver(&self, message: Envelope) -> BoxFuture<'_, Result<Receipt>> {
        Box::pin(self.call(RelayRequest::Deliver { message }))
    }
    fn acknowledge(&self, receipt: Receipt) -> BoxFuture<'_, Result<()>> {
        Box::pin(async move {
            self.call::<serde_json::Value>(RelayRequest::Acknowledge { receipt })
                .await?;
            Ok(())
        })
    }
}
pub(crate) async fn run(local: ClientTransport) {
    let mut last_error = String::new();
    let mut remote = None;
    loop {
        if remote.is_none() {
            remote = crate::backend::relay_remote();
        }
        if let Some(remote) = &remote {
            let result = async {
                let response = local
                    .request(API, Some(serde_json::to_value(RelayRequest::Directory)?))
                    .await?;
                ensure!(response.status.is_success(), "Local assistants unavailable");
                let directory: Directory = serde_json::from_slice(&response.body)?;
                let local = Connection {
                    id: directory.control_id,
                    client: local.clone(),
                };
                pot_relay::exchange(&local, remote).await
            }
            .await;
            match result {
                Ok(()) => last_error.clear(),
                Err(error) => {
                    let error = error.to_string();
                    if error != last_error {
                        eprintln!("Assistant relay: {error}");
                        last_error = error;
                    }
                }
            }
        }
        tokio::time::sleep(std::time::Duration::from_secs(2)).await;
    }
}
