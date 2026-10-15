//! New-reply detection is independent of unread/read state and window selection.
use std::collections::HashMap;
#[derive(Default)]
pub struct Replies(HashMap<String, (String, u64, bool)>);
impl Replies {
    pub fn observe(&mut self, owner: &str, reply: &str, count: u64, connected: bool) -> bool {
        let previous = self
            .0
            .insert(owner.into(), (reply.into(), count, connected));
        previous.is_some_and(|(old, old_count, was_connected)| {
            was_connected && connected && !reply.is_empty() && reply != old && count > old_count
        })
    }
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn initial_history_reconnects_and_duplicate_refreshes_never_replay_arrivals() {
        let mut replies = Replies::default();
        assert!(!replies.observe("control/a", "old", 5, true));
        assert!(!replies.observe("control/a", "old", 5, true));
        assert!(replies.observe("control/a", "new", 6, true));
        assert!(!replies.observe("control/a", "new", 6, true));
        assert!(!replies.observe("control/b", "new", 6, true));
        assert!(!replies.observe("control/a", "new", 6, false));
        assert!(!replies.observe("control/a", "catch-up", 9, true));
        assert!(replies.observe("control/a", "live", 10, true));
        assert!(!replies.observe("control/a", "reset", 1, true));
    }
}
