use super::{
    driver::{Outcome, Update},
    *,
};
use std::collections::HashMap;

#[derive(Default)]
pub struct Transcript {
    pub assistant: Option<Message>,
    pub tools: HashMap<String, Message>,
    pub failure: Option<String>,
}
fn limited(text: String) -> String {
    if text.len() <= 24000 {
        text
    } else {
        format!(
            "{}\n…（输出已截断，完整结果在引擎会话中）",
            text.chars().take(6000).collect::<String>()
        )
    }
}
impl Transcript {
    pub fn record(&mut self, s: &Service, update: &Update) -> Result<()> {
        match update {
            Update::MessageStarted => {
                let message = Message {
                    id: id(),
                    conversation_id: "main".into(),
                    role: Role::Assistant,
                    text: String::new(),
                    status: MessageStatus::Streaming,
                    created_at_ms: now(),
                    request_id: None,
                };
                s.save(message.clone())?;
                self.assistant = Some(message);
            }
            Update::TextDelta(delta) => {
                if let Some(message) = &mut self.assistant {
                    message.text.push_str(delta);
                    s.save(message.clone())?;
                }
            }
            Update::MessageFinished { text, outcome } => {
                if let Some(message) = &mut self.assistant {
                    message.text = text.clone();
                    message.status = if cancelled(s) {
                        MessageStatus::Cancelled
                    } else {
                        match outcome {
                            Outcome::Success => MessageStatus::Complete,
                            Outcome::Cancelled => MessageStatus::Cancelled,
                            _ => MessageStatus::Failed,
                        }
                    };
                    if let Outcome::Failed(reason) = outcome {
                        message.text.push_str(&format!("\n{reason}"));
                        self.failure = Some(reason.clone());
                    }
                    if message.status == MessageStatus::Complete {
                        self.failure = None;
                    }
                    if message.text.is_empty() {
                        message.text = if message.status == MessageStatus::Cancelled {
                            "已停止"
                        } else {
                            "调用工具"
                        }
                        .into();
                    }
                    s.save(message.clone())?;
                }
            }
            Update::ToolStarted {
                id: call_id,
                name,
                args,
            } => {
                self.tool_usage(s, json!({"type":"tool_execution_start","toolCallId":call_id,"toolName":name,"args":args}))?;
                let message = Message {
                    id: id(),
                    conversation_id: "main".into(),
                    role: Role::Tool,
                    text: limited(format!("{name}\n{args}")),
                    status: MessageStatus::Streaming,
                    created_at_ms: now(),
                    request_id: None,
                };
                s.save(message.clone())?;
                self.tools.insert(call_id.clone(), message);
            }
            Update::ToolFinished {
                id: call_id,
                name,
                result,
                failed,
            } => {
                self.tool_usage(s, json!({"type":"tool_execution_end","toolCallId":call_id,"toolName":name,"result":result,"isError":failed}))?;
                if let Some(message) = self.tools.get_mut(call_id) {
                    message.text = limited(format!(
                        "{}\n{}",
                        message.text,
                        result["content"]
                            .as_array()
                            .map(|blocks| blocks
                                .iter()
                                .filter_map(|v| v["text"].as_str())
                                .collect::<Vec<_>>()
                                .join("\n"))
                            .unwrap_or_default()
                    ));
                    message.status = if cancelled(s) {
                        MessageStatus::Cancelled
                    } else if *failed {
                        MessageStatus::Failed
                    } else {
                        MessageStatus::Complete
                    };
                    s.save(message.clone())?;
                }
            }
            Update::CompactionObserved { recovered: true } => self.failure = None,
            _ => {}
        }
        Ok(())
    }
    fn tool_usage(&self, s: &Service, value: Value) -> Result<()> {
        let turn = s
            .inner
            .lock()
            .unwrap()
            .turn
            .as_ref()
            .map(|t| t.user_message_id.clone())
            .unwrap_or_default();
        s.usage
            .lock()
            .unwrap()
            .tool(&s.usage_scope, &turn, &value, now())
    }
    pub fn finish(&mut self, s: &Service, status: MessageStatus, detail: &str) -> Result<()> {
        for message in self.assistant.iter_mut().chain(self.tools.values_mut()) {
            if message.status == MessageStatus::Streaming {
                message.status = status;
                message.text.push_str(detail);
                s.save(message.clone())?;
            }
        }
        Ok(())
    }
}
