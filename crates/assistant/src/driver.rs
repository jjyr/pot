//! Chat turn sequencing. Product integrations supply task/model and lifecycle ports.
use anyhow::{Result, anyhow};
use futures::future::BoxFuture;
use pot_protocol::SendMessage;
use std::sync::Arc;
use tokio::sync::mpsc;

pub trait ChatServices: Send + Sync {
    fn stopped(&self) -> BoxFuture<'_, ()>;
    fn configured(&self) -> bool;
    fn execute(&self, input: SendMessage) -> BoxFuture<'_, Result<()>>;
    fn failed(&self, message: String);
    fn finish_turn(&self) -> Result<()>;
}
pub async fn serve(
    services: Arc<dyn ChatServices>,
    mut input: mpsc::UnboundedReceiver<SendMessage>,
) {
    while let Some(request) =
        tokio::select! { _=services.stopped()=>None, input=input.recv()=>input }
    {
        let configured = services.configured();
        let result = if configured {
            services.execute(request).await
        } else {
            Err(anyhow!("Configure an assistant before sending messages"))
        };
        if let Err(error) = result {
            services.failed(error.to_string());
        }
        if let Err(error) = services.finish_turn() {
            services.failed(error.to_string());
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::Mutex;

    #[derive(Default)]
    struct Services(Mutex<Vec<SendMessage>>);
    impl ChatServices for Services {
        fn stopped(&self) -> BoxFuture<'_, ()> {
            Box::pin(std::future::pending())
        }
        fn configured(&self) -> bool {
            true
        }
        fn execute(&self, input: SendMessage) -> BoxFuture<'_, Result<()>> {
            Box::pin(async move {
                self.0.lock().unwrap().push(input);
                Ok(())
            })
        }
        fn failed(&self, error: String) {
            panic!("{error}");
        }
        fn finish_turn(&self) -> Result<()> {
            Ok(())
        }
    }

    #[tokio::test]
    async fn accepted_user_text_reaches_execution_verbatim() {
        let services = Arc::new(Services::default());
        let (tx, rx) = mpsc::unbounded_channel();
        tx.send(SendMessage {
            request_id: "request".into(),
            conversation_id: String::new(),
            text: "  用户原文\n/skill:research\n".into(),
        })
        .unwrap();
        drop(tx);
        serve(services.clone(), rx).await;
        let accepted = services.0.lock().unwrap();
        assert_eq!(accepted.len(), 1);
        assert_eq!(accepted[0].request_id, "request");
        assert_eq!(accepted[0].text, "  用户原文\n/skill:research\n");
    }
}
