//! Codex content-cost rules, with adapters for Pot's four wire protocols.
//! Baseline: Codex 822e58cc3d, utils/string/src/truncate.rs and
//! core/src/context_manager/history.rs. Keep fixed-value parity tests with changes.
use anyhow::Result;
use pot_model_protocol::{
    codecs::Protocol,
    model::{self, Message},
};
use serde_json::Value;

const IMAGE_BYTES: i64 = 7373;
const DEFAULT_NAMESPACE: &str = "functions";

fn text(value: &str) -> i64 {
    i64::try_from(value.len()).unwrap_or(i64::MAX)
}
fn field(value: &Value, name: &str) -> i64 {
    text(value[name].as_str().unwrap_or_default())
}
fn json(value: &impl serde::Serialize) -> i64 {
    serde_json::to_vec(value)
        .map(|v| i64::try_from(v.len()).unwrap_or(i64::MAX))
        .unwrap_or_default()
}
fn sum(values: impl Iterator<Item = i64>) -> i64 {
    values.fold(0, i64::saturating_add)
}
fn tokens(bytes: i64) -> u64 {
    (bytes.max(0) as u64).saturating_add(3) / 4
}
fn reasoning_bytes(encoded: &str) -> i64 {
    i64::try_from((encoded.len().saturating_mul(3) / 4).saturating_sub(650)).unwrap_or(i64::MAX)
}
fn encrypted_output_bytes(encoded: &str) -> i64 {
    i64::try_from(encoded.len().saturating_mul(9).div_ceil(16)).unwrap_or(i64::MAX)
}

/// Content in a message or function output is combined before rounding once.
fn content_bytes(value: &Value) -> i64 {
    match value {
        Value::String(s) => text(s),
        Value::Array(parts) => sum(parts.iter().map(content_bytes)),
        Value::Object(_) => match value["type"].as_str() {
            Some("text" | "input_text" | "output_text") => field(value, "text"),
            Some("image" | "image_url" | "input_image") => IMAGE_BYTES,
            Some("encrypted_content") => {
                encrypted_output_bytes(value["encrypted_content"].as_str().unwrap_or_default())
            }
            None if value.get("inlineData").is_some() => IMAGE_BYTES,
            None if value.get("text").is_some() && value["thought"] != true => field(value, "text"),
            _ => 0,
        },
        _ => 0,
    }
}
fn call_tokens(name: &str, namespace: Option<&str>, arguments: &str) -> u64 {
    tokens(
        text(name)
            .saturating_add(text(namespace.unwrap_or(DEFAULT_NAMESPACE)))
            .saturating_add(text(arguments)),
    )
}
fn output_tokens(value: &Value, content: &Value) -> u64 {
    tokens(
        content_bytes(content)
            .saturating_add(field(value, "call_id"))
            .saturating_add(field(value, "name"))
            .saturating_add(field(value, "namespace")),
    )
}
/// These are the Codex ResponseItem variants representable by Pot's saved replay.
fn response_item_tokens(item: &Value) -> u64 {
    match item["type"].as_str() {
        Some("message") | None if item.get("content").is_some() => {
            tokens(content_bytes(&item["content"]))
        }
        Some("function_call" | "custom_tool_call") => call_tokens(
            item["name"].as_str().unwrap_or_default(),
            item["namespace"].as_str(),
            item["arguments"]
                .as_str()
                .or_else(|| item["input"].as_str())
                .unwrap_or_default(),
        ),
        Some("function_call_output") => output_tokens(item, &item["output"]),
        Some("custom_tool_call_output") => tokens(
            content_bytes(&item["output"])
                .saturating_add(field(item, "call_id"))
                .saturating_add(field(item, "name")),
        ),
        Some("agent_message") => tokens(
            field(item, "author")
                .saturating_add(field(item, "recipient"))
                .saturating_add(content_bytes(&item["content"])),
        ),
        Some("reasoning" | "compaction" | "context_compaction") => tokens(reasoning_bytes(
            item["encrypted_content"].as_str().unwrap_or_default(),
        )),
        Some("additional_tools" | "tool_search_output") => tokens(json(&item["tools"])),
        Some("tool_search_call") => tokens(json(&item["arguments"])),
        Some("local_shell_call") => tokens(json(&item["action"])),
        Some("web_search_call") => {
            if item["action"].is_null() {
                0
            } else {
                tokens(json(&item["action"]))
            }
        }
        Some("image_generation_call") => tokens(field(item, "revised_prompt").saturating_add(
            if item["result"].as_str().is_some_and(|s| !s.is_empty()) {
                IMAGE_BYTES
            } else {
                0
            },
        )),
        _ => 0,
    }
}

fn chat_tokens(message: &Value) -> u64 {
    let mut count = if message["role"] == "tool" {
        tokens(content_bytes(&message["content"]).saturating_add(field(message, "tool_call_id")))
    } else {
        tokens(content_bytes(&message["content"]))
    };
    for call in message["tool_calls"].as_array().into_iter().flatten() {
        count = count.saturating_add(call_tokens(
            call["function"]["name"].as_str().unwrap_or_default(),
            None,
            call["function"]["arguments"].as_str().unwrap_or_default(),
        ));
    }
    // Plaintext reasoning is excluded, just as Codex excludes unencrypted Reasoning.
    count
}
fn anthropic_tokens(blocks: &Value) -> u64 {
    let mut count = tokens(content_bytes(blocks));
    for block in blocks.as_array().into_iter().flatten() {
        match block["type"].as_str() {
            Some("tool_use") => {
                count = count.saturating_add(call_tokens(
                    block["name"].as_str().unwrap_or_default(),
                    None,
                    &block["input"].to_string(),
                ))
            }
            Some("tool_result") => {
                count = count.saturating_add(tokens(
                    content_bytes(&block["content"]).saturating_add(field(block, "tool_use_id")),
                ))
            }
            _ => {}
        }
    }
    count
}
fn gemini_tokens(parts: &Value, image_bytes: i64) -> u64 {
    let mut count = tokens(content_bytes(parts).saturating_add(image_bytes));
    for part in parts.as_array().into_iter().flatten() {
        if let Some(call) = part.get("functionCall") {
            count = count.saturating_add(call_tokens(
                call["name"].as_str().unwrap_or_default(),
                None,
                &call["args"].to_string(),
            ));
        }
        if let Some(output) = part.get("functionResponse") {
            // Pot encodes its text result inside response.output; unwrap that wire envelope.
            count = count.saturating_add(tokens(
                content_bytes(&output["response"]["output"]).saturating_add(field(output, "id")),
            ));
        }
    }
    count
}

pub(crate) fn estimate(
    protocol: &Protocol,
    model: &str,
    system: &str,
    messages: &[Message],
    tools: &[Value],
) -> Result<u64> {
    // Keep protocol validation, but count logical history items before wire encoders
    // merge adjacent messages or move tool images into synthetic user messages.
    let _ = model::context_payload(protocol, model, system, messages, tools)?;
    let mut count = tokens(text(system));
    for message in messages {
        let image_bytes = sum(message.images.iter().map(|_| IMAGE_BYTES));
        let replay = &message.replay;
        let native = if message.role == "assistant" {
            match protocol {
                Protocol::OpenaiChat if replay.is_object() => {
                    Some(chat_tokens(replay).saturating_add(tokens(image_bytes)))
                }
                Protocol::OpenaiResponses if replay.is_array() => Some(
                    replay
                        .as_array()
                        .unwrap()
                        .iter()
                        .map(response_item_tokens)
                        .fold(tokens(image_bytes), u64::saturating_add),
                ),
                // The Anthropic encoder replaces the entire assistant content array.
                Protocol::AnthropicMessages if replay.is_array() => Some(anthropic_tokens(replay)),
                Protocol::Gemini if replay.is_array() => Some(gemini_tokens(replay, image_bytes)),
                _ => None,
            }
        } else {
            None
        };
        if let Some(native) = native {
            count = count.saturating_add(native);
            continue;
        }
        let mut bytes = text(&message.text).saturating_add(image_bytes);
        if message.role == "tool" {
            bytes = bytes.saturating_add(text(&message.call_id));
        }
        count = count.saturating_add(tokens(bytes));
        for call in &message.calls {
            count =
                count.saturating_add(call_tokens(&call.name, None, &call.arguments.to_string()));
        }
    }
    // Map the supplied logical catalog to one Codex AdditionalTools item. Its
    // schema JSON is content; per-provider request wrappers are not content.
    if !tools.is_empty() {
        count = count.saturating_add(tokens(json(&tools)));
    }
    Ok(count)
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    const PROTOCOLS: [Protocol; 4] = [
        Protocol::OpenaiChat,
        Protocol::OpenaiResponses,
        Protocol::AnthropicMessages,
        Protocol::Gemini,
    ];

    #[test]
    fn codex_rounding_uses_utf8_bytes_and_rounds_each_item_separately() {
        // Fixed expected values, not a second copy of the implementation formula.
        for (bytes, expected) in [
            (-1, 0),
            (0, 0),
            (1, 1),
            (3, 1),
            (4, 1),
            (5, 2),
            (8, 2),
            (9, 3),
            (i64::MAX, 2_305_843_009_213_693_952),
        ] {
            assert_eq!(tokens(bytes), expected);
        }
        for protocol in PROTOCOLS {
            for (value, expected) in [
                ("", 0),
                ("a", 1),
                ("abcde", 2),
                ("你好🌱", 3),
                ("\n\\\"", 1),
                ("Hello, \"world\"!\nこんにちは", 8),
            ] {
                assert_eq!(
                    estimate(
                        &protocol,
                        "model",
                        "",
                        &[Message::text("user", value.into())],
                        &[]
                    )
                    .unwrap(),
                    expected,
                    "{protocol:?}: {value:?}"
                );
            }
            // Base instructions and two one-byte messages cost 1 + 1 + 1, not 1.
            let messages = [
                Message::text("user", "a".into()),
                Message::text("assistant", "b".into()),
            ];
            assert_eq!(
                estimate(&protocol, "model", "c", &messages, &[]).unwrap(),
                3
            );
            // Anthropic/Gemini merge these on the wire; Codex still rounds each
            // logical history item separately. This also keeps usage deltas stable.
            let adjacent = [
                Message::text("user", "a".into()),
                Message::text("user", "b".into()),
            ];
            assert_eq!(
                estimate(&protocol, "model", "c", &adjacent, &[]).unwrap(),
                3
            );
        }
    }

    #[test]
    fn codex_response_item_costs_are_fixed_at_the_source_baseline() {
        let cases = [
            (
                json!({"type":"message","content":[{"type":"output_text","text":"你好🌱"}]}),
                3,
            ),
            (
                json!({"type":"function_call","name":"exec_command","arguments":"{\"cmd\":\"ls\"}"}),
                9,
            ),
            (
                json!({"type":"custom_tool_call","namespace":"n","name":"x","input":"a"}),
                1,
            ),
            (
                json!({"type":"function_call_output","call_id":"call-abc","output":"Screenshot captured"}),
                7,
            ),
            (
                json!({"type":"function_call_output","call_id":"c","name":"x","namespace":"n","output":"a"}),
                1,
            ),
            (
                json!({"type":"reasoning","summary":[{"text":"ignored plaintext reasoning"}]}),
                0,
            ),
            (
                json!({"type":"reasoning","encrypted_content":"A".repeat(1868)}),
                188,
            ),
            (
                json!({"type":"compaction","encrypted_content":"A".repeat(1868)}),
                188,
            ),
            (
                json!({"type":"context_compaction","encrypted_content":null}),
                0,
            ),
            (
                json!({"type":"function_call_output","call_id":"call-encrypted","output":[{"type":"encrypted_content","encrypted_content":"A".repeat(1868)}]}),
                267,
            ),
            (
                json!({"type":"message","content":[{"type":"input_image","image_url":"https://example.com/image.png","detail":"high"}]}),
                1844,
            ),
            (
                json!({"type":"message","content":[{"type":"input_text","text":"images"},{"type":"input_image"},{"type":"input_image"}]}),
                3688,
            ),
            (
                json!({"type":"message","content":[{"type":"input_text","text":"a"},{"type":"input_text","text":"b"},{"type":"output_text","text":"c"}]}),
                1,
            ),
            (
                json!({"type":"function_call_output","call_id":"call-abc","output":[{"type":"input_text","text":"Screenshot captured"},{"type":"input_image"}]}),
                1850,
            ),
            (
                json!({"type":"custom_tool_call_output","call_id":"c","name":"x","namespace":"ignored".repeat(100),"output":"a"}),
                1,
            ),
            (
                json!({"type":"agent_message","author":"/root","recipient":"/root/worker","content":[{"type":"input_text","text":"hi"}]}),
                5,
            ),
            (
                json!({"type":"agent_message","author":"/root","recipient":"/root/worker","content":[{"type":"encrypted_content","encrypted_content":"A".repeat(1868)}]}),
                267,
            ),
            (
                json!({"type":"additional_tools","tools":[{"name":"x","inputSchema":{"type":"object"}}]}),
                12,
            ),
            (
                json!({"type":"tool_search_output","tools":[{"name":"x","inputSchema":{"type":"object"}}]}),
                12,
            ),
            (json!({"type":"tool_search_call","arguments":{"a":1}}), 2),
            (json!({"type":"local_shell_call","action":{"cmd":"ls"}}), 3),
            (json!({"type":"web_search_call","action":null}), 0),
            (json!({"type":"web_search_call","action":{"query":"x"}}), 4),
            (
                json!({"type":"image_generation_call","revised_prompt":"draw","result":"AQID"}),
                1845,
            ),
            (
                json!({"type":"image_generation_call","revised_prompt":"draw","result":""}),
                1,
            ),
            (
                json!({"type":"configuration_update","content":"ignored".repeat(100)}),
                0,
            ),
            (json!({"type":"other","text":"ignored".repeat(100)}), 0),
        ];
        for (item, expected) in cases {
            assert_eq!(response_item_tokens(&item), expected, "{item}");
        }
        // Lock the source's 650-byte subtraction and encrypted-output ceiling.
        assert_eq!(reasoning_bytes(&"A".repeat(867)), 0);
        assert_eq!(reasoning_bytes(&"A".repeat(868)), 1);
        assert_eq!(encrypted_output_bytes("A"), 1);
        assert_eq!(encrypted_output_bytes(&"A".repeat(16)), 9);
    }

    #[test]
    fn transport_metadata_is_ignored_but_inner_argument_json_is_content() {
        let mut message = json!({"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello"}]});
        assert_eq!(response_item_tokens(&message), 2);
        message["id"] = json!("id".repeat(1000));
        message["status"] = json!("complete");
        message["internal_chat_message_metadata_passthrough"] =
            json!({"debug":"metadata".repeat(1000)});
        assert_eq!(response_item_tokens(&message), 2);
        let mut call = json!({"type":"function_call","call_id":"c","name":"exec_command","arguments":"{\"cmd\":\"ls\"}"});
        assert_eq!(response_item_tokens(&call), 9);
        call["call_id"] = json!("transport".repeat(1000));
        assert_eq!(response_item_tokens(&call), 9);
        call["arguments"] = json!("{\"cmd\":\"ls\",\"id\":\"abcdefghijklmnop\"}");
        assert_eq!(response_item_tokens(&call), 15);
    }

    #[test]
    fn native_replay_and_other_protocols_map_to_the_same_content_rules() {
        for protocol in PROTOCOLS {
            let mut reply = Message::text("assistant", "DISPLAY ONLY".repeat(1000));
            reply.calls.push(model::ToolCall {
                id: "call-abc".into(),
                name: "exec_command".into(),
                arguments: json!({"cmd":"ls"}),
            });
            reply.replay = match protocol {
                Protocol::OpenaiChat => {
                    json!({"role":"assistant","content":"ok","reasoning_content":"ignored plaintext","tool_calls":[{"id":"call-abc","type":"function","function":{"name":"exec_command","arguments":"{\"cmd\":\"ls\"}"}}]})
                }
                Protocol::OpenaiResponses => {
                    json!([{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]},{"type":"function_call","call_id":"call-abc","name":"exec_command","arguments":"{\"cmd\":\"ls\"}"}])
                }
                Protocol::AnthropicMessages => {
                    json!([{"type":"text","text":"ok"},{"type":"thinking","thinking":"ignored plaintext","signature":"signature"},{"type":"tool_use","id":"call-abc","name":"exec_command","input":{"cmd":"ls"}}])
                }
                Protocol::Gemini => {
                    json!([{"text":"ignored plaintext","thought":true},{"text":"ok"},{"functionCall":{"id":"call-abc","name":"exec_command","args":{"cmd":"ls"}},"thoughtSignature":"signature"}])
                }
                _ => unreachable!(),
            };
            reply.usage = Some(model::TokenUsage {
                input: 999999,
                output: 88888,
            });
            let mut result = Message::text("tool", "done".into());
            result.call_id = "call-abc".into();
            let messages = [Message::text("user", "你好🌱".into()), reply, result];
            // Rules=2, user=3, reply=1, call=9, output=3. Never count display/usage twice.
            assert_eq!(
                estimate(&protocol, "model", "Rules", &messages, &[]).unwrap(),
                18,
                "{protocol:?}"
            );
            let tools = [json!({"name":"x","inputSchema":{"type":"object"}})];
            // AdditionalTools uses its 46-byte JSON content, not the transport wrapper.
            assert_eq!(estimate(&protocol, "model", "", &[], &tools).unwrap(), 12);
        }
    }

    #[test]
    fn normal_images_use_codex_fixed_cost_independent_of_base64_size_or_dimensions() {
        for protocol in PROTOCOLS {
            for (width, height, data) in
                [(1, 1, "AQID".into()), (4096, 4096, "AQID".repeat(200000))]
            {
                let mut message = Message::text("user", String::new());
                message.images.push(model::ModelImage {
                    mime_type: "image/jpeg".into(),
                    data,
                    width,
                    height,
                });
                assert_eq!(
                    estimate(&protocol, "model", "", &[message], &[]).unwrap(),
                    1844,
                    "{protocol:?}"
                );
            }
            let mut invalid = Message::text("user", String::new());
            invalid.images.push(model::ModelImage {
                mime_type: "image/jpeg".into(),
                data: "AQID".into(),
                width: 0,
                height: 1,
            });
            assert!(estimate(&protocol, "model", "", &[invalid], &[]).is_err());
        }
    }
}
