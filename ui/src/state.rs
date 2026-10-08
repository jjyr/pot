//! Transport-independent chat commands, receipts and draft ownership.
use pot_protocol::{AgentPhase, SendMessage, Snapshot};
use serde::{Deserialize, Serialize};
use std::collections::HashMap;

#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
pub struct Receipt {
    pub id: String,
    pub conversation: String,
    pub text: String,
}
impl Receipt {
    pub fn from_request(request: &SendMessage) -> Self {
        Self {
            id: request.request_id.clone(),
            conversation: request.conversation_id.clone(),
            text: request.text.clone(),
        }
    }
    pub fn clears(&self, conversation: &str, draft: &str) -> bool {
        self.conversation == conversation && self.text == draft
    }
}
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct SendState {
    pending: Option<SendMessage>,
}
impl SendState {
    pub fn begin(
        &mut self,
        id: String,
        conversation: String,
        text: String,
    ) -> Result<SendMessage, &'static str> {
        if self.pending.is_some() {
            return Err("pending_send");
        }
        if text.trim().is_empty() || text.len() > 65536 {
            return Err("invalid_message");
        }
        let request = SendMessage {
            request_id: id,
            conversation_id: conversation,
            text,
        };
        self.pending = Some(request.clone());
        Ok(request)
    }
    /// Retrying reads this exact request, never generates a replacement UUID.
    pub fn pending(&self) -> Option<&SendMessage> {
        self.pending.as_ref()
    }
    pub fn accept(&mut self, id: &str) -> Option<Receipt> {
        if self.pending.as_ref()?.request_id != id {
            return None;
        }
        self.pending.take().map(|r| Receipt::from_request(&r))
    }
    pub fn reject(&mut self, id: &str) {
        if self.pending.as_ref().is_some_and(|r| r.request_id == id) {
            self.pending = None;
        }
    }
    pub fn reconcile(&mut self, snapshot: &Snapshot) -> Option<Receipt> {
        let request = self.pending.as_ref()?;
        if snapshot.conversation_id != request.conversation_id
            || !snapshot
                .messages
                .iter()
                .any(|m| m.request_id.as_deref() == Some(&request.request_id))
        {
            return None;
        }
        let id = request.request_id.clone();
        self.accept(&id)
    }
}
/// Platform-specific editor entities can be stored without sharing their I/O.
pub struct Drafts<T> {
    saved: HashMap<String, T>,
}
impl<T> Default for Drafts<T> {
    fn default() -> Self {
        Self {
            saved: HashMap::new(),
        }
    }
}
impl<T: Clone> Drafts<T> {
    pub fn save(&mut self, conversation: &str, editor: T) {
        self.saved.insert(conversation.into(), editor);
    }
    pub fn get(&self, conversation: &str) -> Option<T> {
        self.saved.get(conversation).cloned()
    }
}
pub fn can_send(snapshot: Option<&Snapshot>, task: bool, pending: bool, connected: bool) -> bool {
    !pending
        && connected
        && snapshot.is_some_and(|s| {
            (s.turn.is_none() || task)
                && (task || s.agent.detail.as_deref() != Some("等待 Pot 额度恢复"))
                && s.agent.phase == AgentPhase::Ready
                && s.budget.as_ref().is_none_or(|b| b.remaining() > 0)
        })
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn uncertain_send_reuses_id_and_late_receipt_cannot_clear_another_request() {
        let mut state = SendState::default();
        let request = state
            .begin("one".into(), "main".into(), "first".into())
            .unwrap();
        assert_eq!(state.pending(), Some(&request));
        assert!(
            state
                .begin("two".into(), "task".into(), "second".into())
                .is_err()
        );
        state.reject("other");
        assert_eq!(state.pending(), Some(&request));
        assert!(state.accept("other").is_none());
        let receipt = state.accept("one").unwrap();
        assert!(receipt.clears("main", "first"));
        assert!(!receipt.clears("task", "first"));
        assert!(!receipt.clears("main", "edited"));
        let second = state
            .begin("two".into(), "task".into(), "second".into())
            .unwrap();
        assert!(state.accept("one").is_none());
        assert_eq!(state.pending(), Some(&second));
    }
    #[test]
    fn invalid_input_never_occupies_pending_slot_and_drafts_are_conversation_scoped() {
        let mut state = SendState::default();
        assert!(
            state
                .begin("one".into(), "main".into(), "   ".into())
                .is_err()
        );
        assert!(
            state
                .begin("one".into(), "main".into(), "中".repeat(22000))
                .is_err()
        );
        assert!(state.pending().is_none());
        let mut drafts = Drafts::default();
        drafts.save("main", "main draft".to_owned());
        drafts.save("task", "task draft".to_owned());
        assert_eq!(drafts.get("main").as_deref(), Some("main draft"));
        assert_eq!(drafts.get("task").as_deref(), Some("task draft"));
        assert!(drafts.get("new").is_none());
    }
}

#[cfg(test)]
mod integration_tests {
    use super::*;
    use pot_protocol::{AgentStatus, Message, MessageStatus, RequestBudget, Role, TurnState};
    fn snapshot() -> Snapshot {
        Snapshot {
            api_version: pot_protocol::API_VERSION,
            boot_id: "fixture".into(),
            cursor: 0,
            agent: AgentStatus {
                model: "pot-agent".into(),
                phase: AgentPhase::Ready,
                detail: None,
            },
            conversation_id: "main".into(),
            messages: vec![],
            turn: None,
            budget: None,
        }
    }
    #[test]
    fn authoritative_receipts_are_conversation_scoped_and_send_guards_match_both_clients() {
        let mut snapshot = snapshot();
        let mut state = SendState::default();
        state
            .begin("request".into(), "main".into(), "message".into())
            .unwrap();
        snapshot.messages.push(Message {
            id: "message".into(),
            conversation_id: "main".into(),
            role: Role::User,
            text: "message".into(),
            status: MessageStatus::Accepted,
            created_at_ms: 0,
            request_id: Some("request".into()),
        });
        snapshot.conversation_id = "other".into();
        assert!(state.reconcile(&snapshot).is_none());
        snapshot.conversation_id = "main".into();
        assert!(
            state
                .reconcile(&snapshot)
                .unwrap()
                .clears("main", "message")
        );
        assert!(state.pending().is_none());
        assert!(can_send(Some(&snapshot), false, false, true));
        assert!(!can_send(Some(&snapshot), false, true, true));
        assert!(!can_send(Some(&snapshot), false, false, false));
        snapshot.turn = Some(TurnState {
            user_message_id: "message".into(),
            cancelling: false,
        });
        assert!(!can_send(Some(&snapshot), false, false, true));
        assert!(can_send(Some(&snapshot), true, false, true));
        snapshot.agent.phase = AgentPhase::NeedsConfiguration;
        assert!(!can_send(Some(&snapshot), true, false, true));
        snapshot.agent.phase = AgentPhase::Ready;
        snapshot.budget = Some(RequestBudget {
            used: 1,
            limit: 1,
            max_output_tokens: 1,
        });
        assert!(!can_send(Some(&snapshot), true, false, true));
    }
}
