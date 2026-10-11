//! Context estimates are separate from Runtime's actual-usage ledger and reservations.
use crate::AgentExtensions;
use anyhow::Result;
use pot_model_protocol::{
    codecs::Protocol,
    model::{self, Message},
};
use serde::{Deserialize, Serialize};
use serde_json::Value;
use sha2::{Digest, Sha256};

#[derive(Serialize, Deserialize)]
pub(crate) struct UsageAnchor {
    prefix_len: usize,
    fingerprint: String,
    tokens: u64,
}
fn fingerprint(
    protocol: &Protocol,
    model: &str,
    system: &str,
    messages: &[Message],
    tools: &[Value],
) -> Result<String> {
    let payload = model::context_payload(protocol, model, system, messages, tools)?;
    let routes: Vec<_> = messages.iter().map(|m| &m.replay_route).collect();
    Ok(
        Sha256::digest(serde_json::to_vec(&(protocol, model, payload, routes))?)
            .iter()
            .map(|byte| format!("{byte:02x}"))
            .collect(),
    )
}
impl UsageAnchor {
    pub(crate) fn capture(
        protocol: &Protocol,
        model: &str,
        system: &str,
        messages: &[Message],
        tools: &[Value],
    ) -> Result<Option<Self>> {
        let Some(usage) = messages.last().and_then(|m| m.usage.as_ref()) else {
            return Ok(None);
        };
        let Some(tokens) = usage.input.checked_add(usage.output).filter(|n| *n > 0) else {
            return Ok(None);
        };
        Ok(Some(Self {
            prefix_len: messages.len(),
            fingerprint: fingerprint(protocol, model, system, messages, tools)?,
            tokens,
        }))
    }
}
pub(crate) fn estimate(
    extensions: &dyn AgentExtensions,
    protocol: &Protocol,
    model: &str,
    system: &str,
    messages: &[Message],
    tools: &[Value],
    anchor: Option<&UsageAnchor>,
) -> Result<u64> {
    let full = extensions.estimate_tokens(protocol, model, system, messages, tools)?;
    if let Some(anchor) = anchor.filter(|a| a.prefix_len <= messages.len()) {
        let prefix = &messages[..anchor.prefix_len];
        if fingerprint(protocol, model, system, prefix, tools)? == anchor.fingerprint {
            let base = extensions.estimate_tokens(protocol, model, system, prefix, tools)?;
            return Ok(anchor.tokens.saturating_add(full.saturating_sub(base)));
        }
    }
    Ok(full)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::extensions::Defaults;
    use pot_model_protocol::model::TokenUsage;
    use serde_json::json;

    fn history() -> Vec<Message> {
        let mut reply = Message::text("assistant", "duplicate display text".repeat(100));
        reply.replay = json!({"role":"assistant","content":"short native response"});
        reply.usage = Some(TokenUsage {
            input: 100,
            output: 20,
        });
        vec![Message::text("user", "history".repeat(1000)), reply]
    }
    #[test]
    fn provider_route_change_invalidates_calibration_with_identical_message_payload() {
        let protocol = Protocol::OpenaiChat;
        let mut messages = history();
        messages.last_mut().unwrap().replay_route = Some("provider-a:model:openai_chat".into());
        let anchor = UsageAnchor::capture(&protocol, "model", "system", &messages, &[])
            .unwrap()
            .unwrap();
        assert_eq!(
            estimate(
                &Defaults,
                &protocol,
                "model",
                "system",
                &messages,
                &[],
                Some(&anchor)
            )
            .unwrap(),
            120
        );
        messages.last_mut().unwrap().replay_route = None;
        let local = estimate(
            &Defaults,
            &protocol,
            "model",
            "system",
            &messages,
            &[],
            None,
        )
        .unwrap();
        assert_eq!(
            estimate(
                &Defaults,
                &protocol,
                "model",
                "system",
                &messages,
                &[],
                Some(&anchor)
            )
            .unwrap(),
            local
        );
    }
    #[test]
    fn measured_prefix_survives_reopen_and_only_new_input_is_estimated() {
        let protocol = Protocol::OpenaiChat;
        let mut messages = history();
        let anchor = UsageAnchor::capture(&protocol, "model", "system", &messages, &[])
            .unwrap()
            .unwrap();
        let anchor: UsageAnchor =
            serde_json::from_slice(&serde_json::to_vec(&anchor).unwrap()).unwrap();
        assert_eq!(
            estimate(
                &Defaults,
                &protocol,
                "model",
                "system",
                &messages,
                &[],
                Some(&anchor)
            )
            .unwrap(),
            120
        );
        messages.push(Message::text("user", "new guidance".into()));
        let result = estimate(
            &Defaults,
            &protocol,
            "model",
            "system",
            &messages,
            &[],
            Some(&anchor),
        )
        .unwrap();
        assert!(result > 120 && result < 300);
        assert!(
            estimate(
                &Defaults,
                &protocol,
                "model",
                "system",
                &messages,
                &[],
                None
            )
            .unwrap()
                > 7000
        );
    }
    #[test]
    fn edits_tools_models_and_compaction_invalidate_measured_prefix() {
        let protocol = Protocol::OpenaiChat;
        let messages = history();
        let anchor = UsageAnchor::capture(&protocol, "model", "system", &messages, &[])
            .unwrap()
            .unwrap();
        let mut edited = messages.clone();
        edited[0].text.push_str("edited");
        let tools = vec![
            json!({"name":"new_tool","description":"changed","inputSchema":{"type":"object"}}),
        ];
        for (model, system, input, tools) in [
            ("model", "system", edited.as_slice(), &[][..]),
            ("model", "changed system", messages.as_slice(), &[][..]),
            ("different model", "system", messages.as_slice(), &[][..]),
            ("model", "system", messages.as_slice(), tools.as_slice()),
            ("model", "system", &messages[..1], &[][..]),
        ] {
            assert_eq!(
                estimate(
                    &Defaults,
                    &protocol,
                    model,
                    system,
                    input,
                    tools,
                    Some(&anchor)
                )
                .unwrap(),
                estimate(&Defaults, &protocol, model, system, input, tools, None).unwrap()
            );
        }
        // Internal display metadata is not part of the request and must not invalidate usage.
        let mut display_only = messages;
        display_only[1].text = "different display text".repeat(1000);
        assert_eq!(
            estimate(
                &Defaults,
                &protocol,
                "model",
                "system",
                &display_only,
                &[],
                Some(&anchor)
            )
            .unwrap(),
            120
        );
    }
    #[test]
    fn unknown_or_zero_usage_is_not_a_measured_baseline() {
        let protocol = Protocol::OpenaiChat;
        let mut messages = history();
        messages[1].usage = None;
        assert!(
            UsageAnchor::capture(&protocol, "model", "system", &messages, &[])
                .unwrap()
                .is_none()
        );
        messages[1].usage = Some(TokenUsage {
            input: 0,
            output: 0,
        });
        assert!(
            UsageAnchor::capture(&protocol, "model", "system", &messages, &[])
                .unwrap()
                .is_none()
        );
    }
    #[test]
    fn overflow_usage_cannot_become_an_anchor() {
        let mut messages = history();
        messages[1].usage = Some(TokenUsage {
            input: u64::MAX,
            output: 1,
        });
        assert!(
            UsageAnchor::capture(&Protocol::OpenaiChat, "model", "system", &messages, &[])
                .unwrap()
                .is_none()
        );
    }
    #[test]
    fn four_protocol_estimates_include_utf8_system_tools_and_tool_results() {
        let tools = crate::tools::schemas(&pot_core::ShellSettings::default());
        for protocol in [
            Protocol::OpenaiChat,
            Protocol::OpenaiResponses,
            Protocol::AnthropicMessages,
            Protocol::Gemini,
        ] {
            let base = vec![Message::text("user", "你好 🌱".into())];
            let plain =
                estimate(&Defaults, &protocol, "model", "system", &base, &[], None).unwrap();
            let with_tools =
                estimate(&Defaults, &protocol, "model", "system", &base, &tools, None).unwrap();
            assert!(with_tools > plain);
            let extended_system = estimate(
                &Defaults,
                &protocol,
                "model",
                &"规则".repeat(50),
                &base,
                &[],
                None,
            )
            .unwrap();
            assert!(extended_system > plain);
            let mut messages = base;
            let mut assistant = Message::text("assistant", String::new());
            assistant.calls = vec![model::ToolCall {
                id: "call".into(),
                name: "exec_command".into(),
                arguments: json!({"cmd":"true"}),
            }];
            messages.push(assistant);
            let mut result = Message::text("tool", "工具结果".repeat(50));
            result.call_id = "call".into();
            messages.push(result);
            assert!(
                estimate(
                    &Defaults, &protocol, "model", "system", &messages, &tools, None
                )
                .unwrap()
                    > with_tools
            );
        }
    }
    #[test]
    fn protocol_changes_invalidate_anchor_and_calibration_saturates_without_wrap() {
        let messages = history();
        let anchor = UsageAnchor::capture(&Protocol::OpenaiChat, "model", "system", &messages, &[])
            .unwrap()
            .unwrap();
        assert_eq!(
            estimate(
                &Defaults,
                &Protocol::AnthropicMessages,
                "model",
                "system",
                &messages,
                &[],
                Some(&anchor)
            )
            .unwrap(),
            estimate(
                &Defaults,
                &Protocol::AnthropicMessages,
                "model",
                "system",
                &messages,
                &[],
                None
            )
            .unwrap()
        );
        let mut maximum = history();
        maximum[1].usage = Some(TokenUsage {
            input: u64::MAX,
            output: 0,
        });
        let anchor = UsageAnchor::capture(&Protocol::OpenaiChat, "model", "system", &maximum, &[])
            .unwrap()
            .unwrap();
        maximum.push(Message::text("user", "trailing".into()));
        assert_eq!(
            estimate(
                &Defaults,
                &Protocol::OpenaiChat,
                "model",
                "system",
                &maximum,
                &[],
                Some(&anchor)
            )
            .unwrap(),
            u64::MAX
        );
    }
}
