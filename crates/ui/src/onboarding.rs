//! Contextual guidance derived from settled task reports, never from user messages.
use pot_protocol::{Message, MessageStatus, Role};

/// Reports have a stable product wrapper; their body remains the Agent's diagnostic.
/// Deliberately exclude transport, permission and unqualified-capability errors:
/// creating a desktop would not resolve those failures.
pub fn missing_desktop(message: &Message) -> bool {
    if message.role != Role::Assistant
        || message.status != MessageStatus::Complete
        || !message.id.starts_with("task-report-")
    {
        return false;
    }
    let Some((heading, body)) = message.text.split_once('\n') else {
        return false;
    };
    if !(heading.starts_with("任务「") && heading.ends_with("未完成")) {
        return false;
    }
    let body = body.to_lowercase();
    [
        "desktop capability unavailable",
        "no desktop environment",
        "no available desktop",
        "no usable desktop",
        "desktop environment is unavailable",
        "desktop environment not found",
        "找不到桌面环境",
        "找不到可用的桌面",
        "无法找到桌面环境",
        "没有桌面环境",
        "没有可用的桌面",
        "未找到桌面环境",
        "未找到可用的桌面",
        "缺少桌面环境",
        "桌面环境不可用",
    ]
    .iter()
    .any(|reason| body.contains(reason))
}

#[cfg(test)]
mod tests {
    use super::*;

    fn report(body: &str) -> Message {
        Message {
            id: "task-report-run-1".into(),
            conversation_id: "main".into(),
            role: Role::Assistant,
            text: format!("任务「Browser task」未完成\n{body}\n任务编号：task-1"),
            status: MessageStatus::Complete,
            created_at_ms: 0,
            request_id: None,
        }
    }

    #[test]
    fn settled_failed_reports_offer_desktop_guidance() {
        for reason in [
            "Desktop capability unavailable",
            "No desktop environment",
            "找不到桌面环境",
            "没有可用的桌面环境",
        ] {
            assert!(missing_desktop(&report(reason)));
        }
    }

    #[test]
    fn unrelated_failures_and_success_do_not_offer_creation() {
        for reason in [
            "API authentication failed",
            "Desktop transport unavailable",
            "Desktop task capability has not passed acceptance",
            "Permission denied",
            "Guest desktop unavailable; verify its configured RFB service",
        ] {
            assert!(!missing_desktop(&report(reason)));
        }
        let mut message = report("No desktop environment");
        message.text = message.text.replace("未完成", "已完成");
        assert!(!missing_desktop(&message));
    }

    #[test]
    fn user_text_and_unsettled_reports_are_not_failure_signals() {
        let mut message = report("No desktop environment");
        message.role = Role::User;
        assert!(!missing_desktop(&message));
        message.role = Role::Assistant;
        message.id = "model-reply".into();
        assert!(!missing_desktop(&message));
        message.id = "task-report-run-1".into();
        message.status = MessageStatus::Streaming;
        assert!(!missing_desktop(&message));
    }
}
