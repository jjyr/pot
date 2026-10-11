//! Chat turn sequencing. Product integrations supply task/model and lifecycle ports.
use anyhow::{Result, anyhow};
use futures::future::BoxFuture;
use pot_protocol::SendMessage;
use std::sync::Arc;
use tokio::sync::mpsc;

pub trait ChatServices: Send + Sync {
    fn stopped(&self) -> BoxFuture<'_, ()>;
    fn configured(&self) -> bool;
    fn paused(&self) -> bool;
    fn task_context(&self) -> String;
    fn resource_context(&self) -> Result<String>;
    fn execute(&self, input: SendMessage) -> BoxFuture<'_, Result<()>>;
    fn failed(&self, message: String);
    fn finish_turn(&self) -> Result<()>;
}
pub async fn serve(
    services: Arc<dyn ChatServices>,
    mut input: mpsc::UnboundedReceiver<SendMessage>,
) {
    while let Some(mut request) =
        tokio::select! { _=services.stopped()=>None, input=input.recv()=>input }
    {
        let configured = services.configured();
        let prepared = (|| -> Result<()> {
            if configured && !services.paused() {
                request.text.push_str(&services.task_context());
            }
            if configured
                && !request
                    .request_id
                    .starts_with(crate::orchestration::RESULT_PREFIX)
            {
                request.text.push_str(&services.resource_context()?);
            }
            Ok(())
        })();
        let result = match prepared {
            Err(error) => Err(error),
            Ok(()) if configured => services.execute(request).await,
            Ok(()) => Err(anyhow!("Configure an assistant before sending messages")),
        };
        if let Err(error) = result {
            services.failed(error.to_string());
        }
        if let Err(error) = services.finish_turn() {
            services.failed(error.to_string());
        }
    }
}
