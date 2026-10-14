//! Coalescing invalidation subscriptions. No timers, transport or UI dependencies.
use futures::{Stream, task::AtomicWaker};
use std::{
    collections::BTreeMap,
    pin::Pin,
    sync::{Arc, Mutex, Weak},
    task::{Context, Poll},
};
pub type Versions = BTreeMap<String, u64>;
#[derive(Default)]
struct State {
    sequence: u64,
    versions: Versions,
    listeners: Vec<Weak<AtomicWaker>>,
}
#[derive(Clone, Default)]
pub struct Changes(Arc<Mutex<State>>);
impl Changes {
    pub fn notify(&self, topic: impl Into<String>) {
        let listeners = {
            let mut state = self.0.lock().unwrap();
            state.sequence += 1;
            let sequence = state.sequence;
            state.versions.insert(topic.into(), sequence);
            state.listeners.retain(|w| w.strong_count() > 0);
            state
                .listeners
                .iter()
                .filter_map(Weak::upgrade)
                .collect::<Vec<_>>()
        };
        for listener in listeners {
            listener.wake();
        }
    }
    pub fn subscribe(&self) -> Subscription {
        let waker = Arc::new(AtomicWaker::new());
        {
            let mut state = self.0.lock().unwrap();
            state
                .listeners
                .retain(|listener| listener.strong_count() > 0);
            state.listeners.push(Arc::downgrade(&waker));
        }
        Subscription {
            changes: self.clone(),
            waker,
            seen: None,
        }
    }
}
pub struct Subscription {
    changes: Changes,
    waker: Arc<AtomicWaker>,
    seen: Option<u64>,
}
impl Stream for Subscription {
    type Item = Versions;
    fn poll_next(mut self: Pin<&mut Self>, cx: &mut Context<'_>) -> Poll<Option<Versions>> {
        self.waker.register(cx.waker());
        let next = {
            let state = self.changes.0.lock().unwrap();
            (self.seen != Some(state.sequence)).then(|| (state.sequence, state.versions.clone()))
        };
        if let Some((sequence, versions)) = next {
            self.seen = Some(sequence);
            Poll::Ready(Some(versions))
        } else {
            Poll::Pending
        }
    }
}
/// Attachable publication point for independently constructed backend components.
#[derive(Default)]
pub struct Signal(Mutex<Option<(Changes, String)>>);
impl Signal {
    pub fn bind(&self, changes: Changes, topic: impl Into<String>) {
        *self.0.lock().unwrap() = Some((changes, topic.into()));
    }
    pub fn notify(&self) {
        if let Some((changes, topic)) = self.0.lock().unwrap().as_ref() {
            changes.notify(topic.clone());
        }
    }
}
#[cfg(test)]
mod tests {
    use super::*;
    use futures::{FutureExt, StreamExt, executor::block_on};
    #[test]
    fn slow_subscriber_keeps_every_dirty_topic_without_an_unbounded_queue() {
        let changes = Changes::default();
        let mut a = changes.subscribe();
        let mut b = changes.subscribe();
        assert!(block_on(a.next()).unwrap().is_empty());
        for _ in 0..10000 {
            changes.notify("tasks");
        }
        changes.notify("machines");
        let latest = block_on(a.next()).unwrap();
        assert_eq!(latest["tasks"], 10000);
        assert_eq!(latest["machines"], 10001);
        assert_eq!(block_on(b.next()).unwrap(), latest);
        assert!(a.next().now_or_never().is_none());
    }
    #[test]
    fn subscribe_before_read_cannot_lose_a_change_during_initial_fetch() {
        let changes = Changes::default();
        let mut stream = changes.subscribe();
        let before = block_on(stream.next()).unwrap();
        changes.notify("settings");
        assert_ne!(block_on(stream.next()).unwrap(), before);
    }
}
