//! Transport-independent App relay. Endpoints retain all durable message state.
use anyhow::{Result, ensure};
use futures::future::BoxFuture;
use pot_protocol::message::*;

pub trait Endpoint: std::marker::Send + Sync {
    fn control_id(&self) -> &str;
    fn directory(&self) -> BoxFuture<'_, Result<Directory>>;
    fn register(&self, directory: Directory) -> BoxFuture<'_, Result<()>>;
    fn pending(&self, target: String) -> BoxFuture<'_, Result<Vec<Envelope>>>;
    fn deliver(&self, message: Envelope) -> BoxFuture<'_, Result<Receipt>>;
    fn acknowledge(&self, receipt: Receipt) -> BoxFuture<'_, Result<()>>;
}

/// One bounded pass. Retry on a later pass using the source's original identities.
/// Both directions are attempted independently; one failed message does not block others.
pub async fn exchange(a: &dyn Endpoint, b: &dyn Endpoint) -> Result<()> {
    ensure!(
        a.control_id() != b.control_id(),
        "Relay endpoints must differ"
    );
    let (ad, bd) = futures::join!(a.directory(), b.directory());
    let (ad, bd) = (ad?, bd?);
    ensure!(
        ad.control_id == a.control_id() && bd.control_id == b.control_id(),
        "Control identity changed"
    );
    let (ar, br) = futures::join!(a.register(bd), b.register(ad));
    ar?;
    br?;
    let (forward, backward) = futures::join!(transfer(a, b), transfer(b, a));
    forward?;
    backward?;
    Ok(())
}
async fn transfer(source: &dyn Endpoint, target: &dyn Endpoint) -> Result<()> {
    let messages = source.pending(target.control_id().into()).await?;
    ensure!(messages.len() <= PAGE_SIZE, "Relay batch too large");
    let mut failure = None;
    for message in messages {
        let result = async {
            ensure!(
                message.valid()
                    && message.from.control_id == source.control_id()
                    && message.to.control_id == target.control_id(),
                "Message route mismatch"
            );
            let receipt = target.deliver(message.clone()).await?;
            ensure!(receipt.message == message, "Delivery receipt mismatch");
            source.acknowledge(receipt).await
        }
        .await;
        if let Err(error) = result {
            failure = Some(error);
        }
    }
    match failure {
        Some(error) => Err(error),
        None => Ok(()),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use pot_assistant::message::Mailbox;
    use std::sync::atomic::{AtomicBool, Ordering};
    struct Node {
        id: String,
        mailbox: Mailbox,
        lose_ack: AtomicBool,
        offline: AtomicBool,
        wrong_identity: AtomicBool,
    }
    impl Node {
        fn new(id: &str) -> Self {
            let path = std::env::temp_dir().join(format!("relay-{}.sqlite", pot_storage::id()));
            Self {
                id: id.into(),
                mailbox: Mailbox::open(&path, id).unwrap(),
                lose_ack: AtomicBool::new(false),
                offline: AtomicBool::new(false),
                wrong_identity: AtomicBool::new(false),
            }
        }
        fn address(&self) -> AssistantRef {
            AssistantRef {
                control_id: self.id.clone(),
                assistant_id: "main".into(),
            }
        }
        fn send(&self, id: &str, target: &Node) {
            self.mailbox
                .send(
                    id,
                    self.address(),
                    Send {
                        to: target.address(),
                        text: "work".into(),
                        reply_to: None,
                    },
                    None,
                )
                .unwrap();
        }
    }
    impl Endpoint for Node {
        fn control_id(&self) -> &str {
            &self.id
        }
        fn directory(&self) -> BoxFuture<'_, Result<Directory>> {
            Box::pin(async move {
                ensure!(!self.offline.load(Ordering::SeqCst), "offline");
                Ok(Directory {
                    control_id: if self.wrong_identity.load(Ordering::SeqCst) {
                        "changed".into()
                    } else {
                        self.id.clone()
                    },
                    assistants: vec![Assistant {
                        address: self.address(),
                        name: "Main".into(),
                    }],
                })
            })
        }
        fn register(&self, directory: Directory) -> BoxFuture<'_, Result<()>> {
            Box::pin(async move { self.mailbox.register(directory) })
        }
        fn pending(&self, target: String) -> BoxFuture<'_, Result<Vec<Envelope>>> {
            Box::pin(async move { self.mailbox.pending(&target) })
        }
        fn deliver(&self, message: Envelope) -> BoxFuture<'_, Result<Receipt>> {
            Box::pin(async move { self.mailbox.deliver(&message) })
        }
        fn acknowledge(&self, receipt: Receipt) -> BoxFuture<'_, Result<()>> {
            Box::pin(async move {
                ensure!(
                    !self.lose_ack.swap(false, Ordering::SeqCst),
                    "lost acknowledgement"
                );
                self.mailbox.acknowledge(&receipt)
            })
        }
    }
    #[tokio::test]
    async fn lost_ack_retry_and_bidirectional_delivery() {
        let a = Node::new("a");
        let b = Node::new("b");
        a.send("ab", &b);
        b.send("ba", &a);
        a.lose_ack.store(true, Ordering::SeqCst);
        assert!(exchange(&a, &b).await.is_err());
        assert!(a.mailbox.next("main").unwrap().is_some());
        assert!(b.mailbox.next("main").unwrap().is_some());
        exchange(&a, &b).await.unwrap();
        assert!(a.mailbox.pending("b").unwrap().is_empty());
        assert!(b.mailbox.pending("a").unwrap().is_empty());
        assert_eq!(b.mailbox.history("main", 0).unwrap().len(), 2);
    }
    #[tokio::test]
    async fn offline_and_changed_identity_never_redirect_messages() {
        let a = Node::new("a");
        let b = Node::new("b");
        a.send("ab", &b);
        b.offline.store(true, Ordering::SeqCst);
        assert!(exchange(&a, &b).await.is_err());
        b.offline.store(false, Ordering::SeqCst);
        b.wrong_identity.store(true, Ordering::SeqCst);
        assert!(exchange(&a, &b).await.is_err());
        assert!(b.mailbox.next("main").unwrap().is_none());
        assert_eq!(a.mailbox.pending("b").unwrap().len(), 1);
        b.wrong_identity.store(false, Ordering::SeqCst);
        exchange(&a, &b).await.unwrap();
    }
}
