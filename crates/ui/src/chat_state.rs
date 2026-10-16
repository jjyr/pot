//! Chat presentation derived from authoritative messages and the active turn.
use pot_protocol::{Message, MessageStatus, Role, Snapshot};

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Activity {
    Sending,
    Thinking,
    Working,
    Stopping,
}

pub fn activity(snapshot: Option<&Snapshot>, pending: bool) -> Option<Activity> {
    let Some((snapshot, turn)) = snapshot.and_then(|s| s.turn.as_ref().map(|turn| (s, turn)))
    else {
        return pending.then_some(Activity::Sending);
    };
    if turn.cancelling {
        return Some(Activity::Stopping);
    }
    let messages = &snapshot.messages;
    let start = messages
        .iter()
        .position(|m| m.id == turn.user_message_id)
        .map(|i| i + 1)
        .unwrap_or(messages.len());
    match messages[start..]
        .iter()
        .rev()
        .find(|m| m.role != Role::User && !m.text.is_empty())
    {
        Some(m) if m.role == Role::Assistant && m.status == MessageStatus::Streaming => None,
        Some(m) if m.role == Role::Tool => Some(Activity::Working),
        _ => Some(Activity::Thinking),
    }
}

pub fn failure_detail(message: &Message) -> Option<&str> {
    if message.role != Role::System || message.status != MessageStatus::Failed {
        return None;
    }
    message
        .text
        .strip_prefix("任务未完成：")
        .or_else(|| message.text.strip_prefix("Task incomplete: "))
}

/// A turn failure is stored both in its unfinished assistant text and a system
/// notice. Present the notice once, retaining any assistant text before it.
pub fn messages_for_display(messages: &[Message]) -> Vec<Message> {
    let mut result: Vec<Message> = Vec::with_capacity(messages.len());
    for message in messages {
        if let Some(detail) = failure_detail(message) {
            for previous in result.iter_mut().rev().take_while(|m| m.role != Role::User) {
                if previous.role == Role::Assistant && previous.status == MessageStatus::Failed {
                    let text = previous.text.trim_end();
                    if text == detail.trim() {
                        previous.text.clear();
                    } else if let Some(text) = text.strip_suffix(&format!("\n{}", detail.trim())) {
                        previous.text = text.trim_end().into();
                    }
                }
            }
        }
        result.push(message.clone());
    }
    result.retain(|m| m.role != Role::Assistant || !m.text.trim().is_empty());
    result
}

#[cfg(test)]
mod tests {
    use super::*;
    use pot_protocol::{AgentPhase, AgentStatus, TurnState};
    fn message(id: &str, role: Role, text: &str, status: MessageStatus) -> Message {
        Message {
            id: id.into(),
            conversation_id: "main".into(),
            role,
            text: text.into(),
            status,
            created_at_ms: 1,
            request_id: None,
        }
    }
    fn snapshot() -> Snapshot {
        Snapshot {
            api_version: pot_protocol::API_VERSION,
            conversation_id: "main".into(),
            boot_id: "test".into(),
            cursor: 0,
            agent: AgentStatus {
                model: "test".into(),
                phase: AgentPhase::Ready,
                detail: None,
            },
            budget: None,
            turn: Some(TurnState {
                user_message_id: "user".into(),
                cancelling: false,
            }),
            messages: vec![
                message(
                    "old",
                    Role::Assistant,
                    "Previous reply",
                    MessageStatus::Complete,
                ),
                message("user", Role::User, "Hi", MessageStatus::Streaming),
            ],
        }
    }
    #[test]
    fn activity_tracks_the_current_turn_and_streamed_output() {
        let mut s = snapshot();
        assert_eq!(activity(None, true), Some(Activity::Sending));
        assert_eq!(activity(Some(&s), false), Some(Activity::Thinking));
        s.messages.push(message(
            "reply",
            Role::Assistant,
            "",
            MessageStatus::Streaming,
        ));
        assert_eq!(activity(Some(&s), false), Some(Activity::Thinking));
        s.messages.last_mut().unwrap().text = "Hello".into();
        assert_eq!(activity(Some(&s), false), None);
        s.messages.push(message(
            "tool",
            Role::Tool,
            "read",
            MessageStatus::Streaming,
        ));
        assert_eq!(activity(Some(&s), false), Some(Activity::Working));
        s.turn.as_mut().unwrap().cancelling = true;
        assert_eq!(activity(Some(&s), false), Some(Activity::Stopping));
        s.turn = None;
        assert_eq!(activity(Some(&s), false), None);
    }
    #[test]
    fn failure_is_shown_once_without_discarding_partial_text_or_previous_turns() {
        let mut messages = vec![
            message("old", Role::Assistant, "502", MessageStatus::Failed),
            message("user", Role::User, "Hi", MessageStatus::Failed),
            message(
                "reply",
                Role::Assistant,
                "Partial reply\n502",
                MessageStatus::Failed,
            ),
            message(
                "error",
                Role::System,
                "任务未完成：502",
                MessageStatus::Failed,
            ),
        ];
        let shown = messages_for_display(&messages);
        assert_eq!(shown[0].text, "502");
        assert_eq!(shown[2].text, "Partial reply");
        assert_eq!(failure_detail(&shown[3]), Some("502"));
        messages[2].text = "502".into();
        let shown = messages_for_display(&messages);
        assert_eq!(shown.len(), 3);
        assert_eq!(shown[2].id, "error");
        assert_eq!(messages[2].text, "502"); // Projection never changes the transcript.
    }
}
