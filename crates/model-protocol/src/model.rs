//! Model protocol boundary. Persist native assistant items for lossless same-provider replay.
use crate::codecs::Protocol;
use anyhow::{Result, anyhow, bail, ensure};
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};

#[derive(Clone, Debug, Serialize, Deserialize)]
pub struct ToolCall {
    pub id: String,
    pub name: String,
    pub arguments: Value,
}
#[derive(Clone, Debug, Serialize, Deserialize)]
pub struct Message {
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub images: Vec<ModelImage>,
    pub role: String,
    pub text: String,
    #[serde(default)]
    pub calls: Vec<ToolCall>,
    #[serde(default)]
    pub call_id: String,
    #[serde(default)]
    pub replay: Value,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub replay_route: Option<String>,
    /// Complete provider-reported usage for context calibration, never sent upstream.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub usage: Option<TokenUsage>,
}
#[derive(Clone, Debug, Serialize, Deserialize)]
pub struct ModelImage {
    pub mime_type: String,
    pub data: String,
    pub width: u32,
    pub height: u32,
}
impl ModelImage {
    pub fn data_url(&self) -> String {
        format!("data:{};base64,{}", self.mime_type, self.data)
    }
    pub fn validate(&self) -> Result<()> {
        ensure!(
            matches!(
                self.mime_type.as_str(),
                "image/png" | "image/jpeg" | "image/webp"
            ) && self.width > 0
                && self.width <= 4096
                && self.height > 0
                && self.height <= 4096
                && !self.data.is_empty()
                && self.data.len() <= 1400000
                && self
                    .data
                    .bytes()
                    .all(|b| b.is_ascii_alphanumeric() || matches!(b, b'+' | b'/' | b'=')),
            "Invalid model image"
        );
        Ok(())
    }
}
#[derive(Clone, Debug, Serialize, Deserialize)]
pub struct TokenUsage {
    /// Includes cache reads/writes where the provider reports them separately.
    pub input: u64,
    /// Includes reasoning where it is a subset of, or separately reported from, output.
    pub output: u64,
}
impl Message {
    pub fn text(role: &str, text: String) -> Self {
        Self {
            images: vec![],
            role: role.into(),
            text,
            calls: vec![],
            call_id: String::new(),
            replay: Value::Null,
            replay_route: None,
            usage: None,
        }
    }
}

/// The encoded context only, excluding transport/generation options and internal replay metadata.
pub fn context_payload(
    protocol: &Protocol,
    model: &str,
    system: &str,
    messages: &[Message],
    tools: &[Value],
) -> Result<Value> {
    let body = request(protocol, model, system, messages, tools, 1, None)?;
    let keys: &[&str] = match protocol {
        Protocol::OpenaiChat => &["messages", "tools"],
        Protocol::OpenaiResponses => &["instructions", "input", "tools"],
        Protocol::AnthropicMessages => &["system", "messages", "tools"],
        Protocol::Gemini => &["systemInstruction", "contents", "tools"],
        Protocol::Unknown => bail!("Unsupported model protocol"),
    };
    Ok(Value::Object(
        keys.iter()
            .filter_map(|key| body.get(*key).map(|v| ((*key).to_owned(), v.clone())))
            .collect(),
    ))
}
pub fn request(
    protocol: &Protocol,
    model: &str,
    system: &str,
    messages: &[Message],
    tools: &[Value],
    cap: u64,
    effort: Option<&str>,
) -> Result<Value> {
    let mut names = std::collections::HashSet::new();
    for message in messages {
        for image in &message.images {
            image.validate()?;
        }
    }
    for tool in tools {
        ensure!(
            tool["name"]
                .as_str()
                .is_some_and(|name| !name.is_empty() && names.insert(name)),
            "Duplicate or invalid tool name"
        );
        let schema = &tool["inputSchema"];
        ensure!(
            schema.is_object() && schema["type"] == "object",
            "Tool inputSchema must be an object schema"
        );
        ensure!(
            schema.get("properties").is_none_or(Value::is_object),
            "Tool properties must be an object"
        );
        ensure!(
            schema
                .get("required")
                .is_none_or(|v| v.as_array().is_some_and(|a| a.iter().all(Value::is_string))),
            "Tool required must be a string array"
        );
    }
    let functions: Vec<Value> = tools.iter().map(|t| json!({"name":t["name"],"description":t["description"],"parameters":t["inputSchema"]})).collect();
    Ok(match protocol {
        Protocol::OpenaiChat => {
            let mut input = vec![json!({"role":"system","content":system})];
            let mut images = vec![];
            for m in messages {
                if m.role != "tool" && !images.is_empty() {
                    input.push(json!({"role":"user","content":images}));
                    images = vec![];
                }
                if m.role == "assistant" && m.replay.is_object() {
                    input.push(m.replay.clone());
                } else if m.role == "tool" {
                    input.push(json!({"role":"tool","tool_call_id":m.call_id,"content":m.text}));
                } else {
                    let mut message = json!({"role":m.role,"content":m.text});
                    if !m.calls.is_empty() {
                        message["tool_calls"] = json!(m.calls.iter().map(|c| json!({"id":c.id,"type":"function","function":{"name":c.name,"arguments":c.arguments.to_string()}})).collect::<Vec<_>>());
                    }
                    input.push(message);
                }
                images.extend(m.images.iter().map(|image| json!({"type":"image_url","image_url":{"url":image.data_url(),"detail":"high"}})));
            }
            if !images.is_empty() {
                input.push(json!({"role":"user","content":images}));
            }
            let mut b = json!({"model":model,"messages":input,"stream":true,"stream_options":{"include_usage":true},"max_tokens":cap});
            if !functions.is_empty() {
                b["tools"] = json!(
                    functions
                        .iter()
                        .map(|f| json!({"type":"function","function":f}))
                        .collect::<Vec<_>>()
                );
            }
            if model.starts_with("gpt-5")
                || model.starts_with("o1")
                || model.starts_with("o3")
                || model.starts_with("o4")
            {
                b.as_object_mut().unwrap().remove("max_tokens");
                b["max_completion_tokens"] = json!(cap);
            }
            if let Some(e) = effort {
                b["reasoning_effort"] = json!(e);
            }
            b
        }
        Protocol::OpenaiResponses => {
            let mut input = vec![];
            let mut images = vec![];
            for m in messages {
                if m.role != "tool" && !images.is_empty() {
                    input.push(json!({"role":"user","content":images}));
                    images = vec![];
                }
                if m.role == "assistant" && m.replay.is_array() {
                    input.extend(m.replay.as_array().unwrap().clone());
                } else if m.role == "tool" {
                    input.push(
                        json!({"type":"function_call_output","call_id":m.call_id,"output":m.text}),
                    );
                } else {
                    if !m.text.is_empty() || m.calls.is_empty() {
                        input.push(json!({"role":m.role,"content":m.text}));
                    }
                    input.extend(m.calls.iter().map(|c| json!({"type":"function_call","call_id":c.id,"name":c.name,"arguments":c.arguments.to_string()})));
                }
                images.extend(m.images.iter().map(|image| json!({"type":"input_image","image_url":image.data_url(),"detail":"high"})));
            }
            if !images.is_empty() {
                input.push(json!({"role":"user","content":images}));
            }
            let mut b = json!({"model":model,"instructions":system,"input":input,"stream":true,"store":false,"include":["reasoning.encrypted_content"],"max_output_tokens":cap});
            if !functions.is_empty() {
                b["tools"] = json!(
                    functions
                        .iter()
                        .map(|f| {
                            let mut f = f.clone();
                            f["type"] = json!("function");
                            f
                        })
                        .collect::<Vec<_>>()
                );
            }
            if let Some(e) = effort {
                b["reasoning"] = json!({"effort":e});
            }
            b
        }
        Protocol::AnthropicMessages => {
            let mut input: Vec<Value> = vec![];
            for m in messages {
                let role = if m.role == "tool" { "user" } else { &m.role };
                let blocks = if m.role == "assistant" && m.replay.is_array() {
                    m.replay.as_array().unwrap().clone()
                } else if m.role == "tool" {
                    let mut content = vec![json!({"type":"text","text":m.text})];
                    content.extend(m.images.iter().map(|image| json!({"type":"image","source":{"type":"base64","media_type":image.mime_type,"data":image.data}})));
                    vec![json!({"type":"tool_result","tool_use_id":m.call_id,"content":content})]
                } else {
                    let mut content = vec![];
                    if !m.text.is_empty() || m.calls.is_empty() {
                        content.push(json!({"type":"text","text":m.text}));
                    }
                    content.extend(m.calls.iter().map(
                        |c| json!({"type":"tool_use","id":c.id,"name":c.name,"input":c.arguments}),
                    ));
                    content.extend(m.images.iter().map(|image| json!({"type":"image","source":{"type":"base64","media_type":image.mime_type,"data":image.data}})));
                    content
                };
                if input.last().is_some_and(|p| p["role"] == role) {
                    input.last_mut().unwrap()["content"]
                        .as_array_mut()
                        .unwrap()
                        .extend(blocks);
                } else {
                    input.push(json!({"role":role,"content":blocks}));
                }
            }
            let mut b = json!({"model":model,"system":system,"messages":input,"stream":true,"max_tokens":cap});
            if !functions.is_empty() {
                b["tools"]=json!(functions.iter().map(|f|json!({"name":f["name"],"description":f["description"],"input_schema":f["parameters"]})).collect::<Vec<_>>());
            }
            if let Some(e) = effort {
                b["thinking"] = json!({"type":"adaptive"});
                b["output_config"] = json!({"effort":e});
            }
            b
        }
        Protocol::Gemini => {
            let mut input: Vec<Value> = vec![];
            for m in messages {
                let role = if m.role == "assistant" {
                    "model"
                } else {
                    "user"
                };
                let mut parts = if m.role == "assistant" && m.replay.is_array() {
                    m.replay.as_array().unwrap().clone()
                } else if m.role == "tool" {
                    let name = messages
                        .iter()
                        .flat_map(|m| &m.calls)
                        .find(|c| c.id == m.call_id)
                        .map(|c| c.name.as_str())
                        .ok_or_else(|| anyhow!("Orphan Gemini tool result"))?;
                    vec![
                        json!({"functionResponse":{"id":m.call_id,"name":name,"response":{"output":m.text}}}),
                    ]
                } else {
                    let mut parts = vec![];
                    if !m.text.is_empty() || m.calls.is_empty() {
                        parts.push(json!({"text":m.text}));
                    }
                    parts.extend(m.calls.iter().map(
                        |c| json!({"functionCall":{"id":c.id,"name":c.name,"args":c.arguments}}),
                    ));
                    parts
                };
                parts.extend(m.images.iter().map(
                    |image| json!({"inlineData":{"mimeType":image.mime_type,"data":image.data}}),
                ));
                if input.last().is_some_and(|p| p["role"] == role) {
                    input.last_mut().unwrap()["parts"]
                        .as_array_mut()
                        .unwrap()
                        .extend(parts);
                } else {
                    input.push(json!({"role":role,"parts":parts}));
                }
            }
            let mut b = json!({"systemInstruction":{"parts":[{"text":system}]},"contents":input,"generationConfig":{"maxOutputTokens":cap}});
            if !functions.is_empty() {
                b["tools"] = json!([{"functionDeclarations":functions}]);
            }
            if let Some(e) = effort {
                b["generationConfig"]["thinkingConfig"] = json!({"thinkingLevel":e.to_uppercase()});
            }
            b
        }
        Protocol::Unknown => bail!("Unsupported model protocol"),
    })
}

pub struct Decoder {
    protocol: Protocol,
    text: String,
    blocks: std::collections::BTreeMap<usize, Value>,
    arguments: std::collections::BTreeMap<usize, String>,
    terminal: bool,
    failed: Option<String>,
    reason: String,
    chat_reasoning: String,
    observer: crate::codecs::UsageObserver,
    usage: Option<TokenUsage>,
}
impl Decoder {
    pub fn new(protocol: Protocol) -> Self {
        Self {
            protocol,
            text: String::new(),
            blocks: Default::default(),
            arguments: Default::default(),
            terminal: false,
            failed: None,
            reason: String::new(),
            chat_reasoning: String::new(),
            observer: Default::default(),
            usage: None,
        }
    }
    pub fn push(&mut self, e: &Value) -> Result<String> {
        if e.get("error").is_some() || e["type"] == "error" {
            bail!("Model stream reported an error");
        }
        if let Some(usage) = self.observer.observe(&self.protocol, e)?
            && let (Some(input), Some(output)) = (
                usage["prompt_tokens"].as_u64(),
                usage["completion_tokens"].as_u64(),
            )
            && input.checked_add(output).is_some_and(|total| total > 0)
        {
            // Usage frames are cumulative snapshots, not deltas to add together.
            self.usage = Some(TokenUsage { input, output });
        }
        let mut delta = String::new();
        match self.protocol {
            Protocol::OpenaiChat => {
                if let Some(c) = e["choices"].as_array().and_then(|c| c.first()) {
                    if let Some(s) = c["delta"]["content"].as_str() {
                        delta.push_str(s);
                    }
                    if let Some(s) = c["delta"]["reasoning_content"].as_str() {
                        self.chat_reasoning.push_str(s);
                    }
                    if let Some(calls) = c["delta"]["tool_calls"].as_array() {
                        for c in calls {
                            let i = c["index"].as_u64().unwrap_or(0) as usize;
                            let block=self.blocks.entry(i).or_insert_with(||json!({"id":"","type":"function","function":{"name":"","arguments":""}}));
                            if let Some(s) = c["id"].as_str() {
                                block["id"] =
                                    json!(format!("{}{}", block["id"].as_str().unwrap_or(""), s));
                            }
                            for key in ["name", "arguments"] {
                                if let Some(s) = c["function"][key].as_str() {
                                    block["function"][key] = json!(format!(
                                        "{}{}",
                                        block["function"][key].as_str().unwrap_or(""),
                                        s
                                    ));
                                }
                            }
                        }
                    }
                    if let Some(reason) = c["finish_reason"].as_str() {
                        self.reason = reason.into();
                        self.terminal = true;
                    }
                }
            }
            Protocol::OpenaiResponses => match e["type"].as_str().unwrap_or("") {
                "response.output_text.delta" => {
                    delta = e["delta"].as_str().unwrap_or("").into();
                }
                "response.output_item.done" => {
                    let i = e["output_index"].as_u64().unwrap_or(0) as usize;
                    self.blocks.insert(i, e["item"].clone());
                }
                "response.completed" => {
                    if let Some(output) = e["response"]["output"].as_array() {
                        self.blocks = output.iter().cloned().enumerate().collect();
                    }
                    self.terminal = true;
                    self.reason = "stop".into();
                }
                "response.incomplete" | "response.failed" => {
                    self.failed = Some("Model response incomplete or failed".into());
                    self.terminal = true;
                }
                _ => {}
            },
            Protocol::AnthropicMessages => match e["type"].as_str().unwrap_or("") {
                "content_block_start" => {
                    let i = e["index"].as_u64().unwrap_or(0) as usize;
                    if e["content_block"]["type"] == "text" {
                        delta = e["content_block"]["text"].as_str().unwrap_or("").into();
                    }
                    self.blocks.insert(i, e["content_block"].clone());
                }
                "content_block_delta" => {
                    let i = e["index"].as_u64().unwrap_or(0) as usize;
                    let block = self
                        .blocks
                        .get_mut(&i)
                        .ok_or_else(|| anyhow!("Unknown Anthropic block"))?;
                    match e["delta"]["type"].as_str().unwrap_or("") {
                        "text_delta" => {
                            let s = e["delta"]["text"].as_str().unwrap_or("");
                            delta = s.into();
                            block["text"] =
                                json!(format!("{}{}", block["text"].as_str().unwrap_or(""), s));
                        }
                        "input_json_delta" => {
                            self.arguments
                                .entry(i)
                                .or_default()
                                .push_str(e["delta"]["partial_json"].as_str().unwrap_or(""));
                        }
                        "thinking_delta" => {
                            block["thinking"] = json!(format!(
                                "{}{}",
                                block["thinking"].as_str().unwrap_or(""),
                                e["delta"]["thinking"].as_str().unwrap_or("")
                            ));
                        }
                        "signature_delta" => {
                            block["signature"] = json!(format!(
                                "{}{}",
                                block["signature"].as_str().unwrap_or(""),
                                e["delta"]["signature"].as_str().unwrap_or("")
                            ));
                        }
                        _ => {}
                    }
                }
                "message_delta" => {
                    if let Some(r) = e["delta"]["stop_reason"].as_str() {
                        self.reason = r.into();
                    }
                }
                "message_stop" => {
                    self.terminal = true;
                }
                _ => {}
            },
            Protocol::Gemini => {
                if let Some(c) = e["candidates"].as_array().and_then(|c| c.first()) {
                    if let Some(parts) = c["content"]["parts"].as_array() {
                        for p in parts {
                            if p["thought"] != true
                                && let Some(s) = p["text"].as_str()
                            {
                                delta.push_str(s);
                            }
                            self.blocks.insert(self.blocks.len(), p.clone());
                        }
                    }
                    if let Some(reason) = c["finishReason"].as_str() {
                        self.reason = reason.into();
                        self.terminal = true;
                    }
                }
                if e["promptFeedback"]["blockReason"].is_string() {
                    bail!("Gemini blocked the prompt");
                }
            }
            Protocol::Unknown => bail!("Unsupported protocol"),
        }
        self.text.push_str(&delta);
        ensure!(self.text.len() <= 16 * 1024 * 1024, "Model text too large");
        Ok(delta)
    }
    pub fn finish(mut self) -> Result<Message> {
        ensure!(
            self.terminal,
            "Model stream ended without a terminal response"
        );
        if let Some(e) = self.failed {
            bail!("{e}");
        }
        let accepted = matches!(
            self.reason.as_str(),
            "stop" | "tool_calls" | "end_turn" | "tool_use" | "STOP"
        );
        ensure!(accepted, "Model response ended with {}", self.reason);
        let mut calls = vec![];
        for (i, b) in &mut self.blocks {
            let call = match self.protocol {
                Protocol::OpenaiChat => Some((
                    b["id"].as_str().unwrap_or("").to_owned(),
                    b["function"]["name"].as_str().unwrap_or("").to_owned(),
                    serde_json::from_str::<Value>(
                        b["function"]["arguments"].as_str().unwrap_or(""),
                    )?,
                )),
                Protocol::OpenaiResponses if b["type"] == "function_call" => Some((
                    b["call_id"].as_str().unwrap_or("").to_owned(),
                    b["name"].as_str().unwrap_or("").to_owned(),
                    serde_json::from_str::<Value>(b["arguments"].as_str().unwrap_or(""))?,
                )),
                Protocol::AnthropicMessages if b["type"] == "tool_use" => {
                    if let Some(args) = self.arguments.get(i) {
                        b["input"] = serde_json::from_str(args)?;
                    }
                    Some((
                        b["id"].as_str().unwrap_or("").to_owned(),
                        b["name"].as_str().unwrap_or("").to_owned(),
                        b["input"].clone(),
                    ))
                }
                Protocol::Gemini if b["functionCall"].is_object() => {
                    let c = &b["functionCall"];
                    Some((
                        c["id"]
                            .as_str()
                            .map(str::to_owned)
                            .unwrap_or_else(|| format!("gemini-{i}")),
                        c["name"].as_str().unwrap_or("").to_owned(),
                        c.get("args").cloned().unwrap_or(json!({})),
                    ))
                }
                _ => None,
            };
            if let Some((id, name, arguments)) = call {
                ensure!(
                    !id.is_empty() && !name.is_empty() && arguments.is_object(),
                    "Invalid tool call"
                );
                ensure!(
                    !calls.iter().any(|c: &ToolCall| c.id == id),
                    "Duplicate tool call ID"
                );
                calls.push(ToolCall {
                    id,
                    name,
                    arguments,
                });
            }
        }
        let replay = if self.protocol == Protocol::OpenaiChat {
            let mut r = json!({"role":"assistant","content":if self.text.is_empty(){Value::Null}else{json!(self.text)}});
            if !self.blocks.is_empty() {
                r["tool_calls"] = json!(self.blocks.values().collect::<Vec<_>>());
            }
            if !self.chat_reasoning.is_empty() {
                r["reasoning_content"] = json!(self.chat_reasoning);
            }
            r
        } else {
            json!(self.blocks.values().collect::<Vec<_>>())
        };
        // Some Responses endpoints only report text in final output items.
        if self.protocol == Protocol::OpenaiResponses {
            self.text = self
                .blocks
                .values()
                .filter(|b| b["type"] == "message")
                .flat_map(|b| b["content"].as_array().into_iter().flatten())
                .filter_map(|b| b["text"].as_str())
                .collect::<Vec<_>>()
                .join("");
        }
        ensure!(calls.len() <= 128, "Too many tool calls in one response");
        Ok(Message {
            images: vec![],
            role: "assistant".into(),
            text: self.text,
            calls,
            call_id: String::new(),
            replay,
            replay_route: None,
            usage: self.usage,
        })
    }
}

/// Bounded incremental SSE parser, including multiline data and CRLF.
#[derive(Default)]
pub struct Sse {
    pending: Vec<u8>,
    data: Vec<String>,
}
impl Sse {
    pub fn push(&mut self, bytes: &[u8]) -> Result<Vec<Value>> {
        self.pending.extend_from_slice(bytes);
        ensure!(
            self.pending.len() <= 16 * 1024 * 1024,
            "SSE frame too large"
        );
        let mut events = vec![];
        while let Some(i) = self.pending.iter().position(|c| *c == b'\n') {
            let line: Vec<u8> = self.pending.drain(..=i).collect();
            let line = std::str::from_utf8(&line)?.trim_end_matches(['\r', '\n']);
            if line.is_empty() {
                if !self.data.is_empty() {
                    let data = self.data.join("\n");
                    self.data.clear();
                    if data != "[DONE]" {
                        events.push(serde_json::from_str(&data)?);
                    }
                }
            } else if let Some(data) = line.strip_prefix("data:") {
                self.data
                    .push(data.strip_prefix(' ').unwrap_or(data).into());
            }
            ensure!(
                self.data.iter().map(String::len).sum::<usize>() <= 16 * 1024 * 1024,
                "SSE event too large"
            );
        }
        Ok(events)
    }
    pub fn finish(&mut self) -> Result<Vec<Value>> {
        self.push(b"\n\n")
    }
}

#[cfg(test)]
mod tests {
    #[test]
    fn typed_images_survive_serialization_and_use_provider_image_blocks() {
        use super::*;
        let mut tool = Message::text("tool", "Browser observation".into());
        tool.call_id = "call".into();
        tool.images.push(ModelImage {
            mime_type: "image/jpeg".into(),
            data: "AQID".into(),
            width: 1280,
            height: 800,
        });
        let restored: Message =
            serde_json::from_value(serde_json::to_value(&tool).unwrap()).unwrap();
        assert_eq!(restored.images[0].width, 1280);
        let mut assistant = Message::text("assistant", String::new());
        assistant.calls.push(ToolCall {
            id: "call".into(),
            name: "browser".into(),
            arguments: json!({}),
        });
        for protocol in [
            Protocol::OpenaiChat,
            Protocol::OpenaiResponses,
            Protocol::AnthropicMessages,
            Protocol::Gemini,
        ] {
            let body = request(
                &protocol,
                "model",
                "system",
                &[assistant.clone(), restored.clone()],
                &[],
                100,
                None,
            )
            .unwrap();
            let encoded = body.to_string();
            let key = match protocol {
                Protocol::OpenaiChat => "image_url",
                Protocol::OpenaiResponses => "input_image",
                Protocol::AnthropicMessages => "media_type",
                Protocol::Gemini => "inlineData",
                _ => unreachable!(),
            };
            assert!(encoded.contains(key), "{encoded}");
            assert!(
                !encoded.contains("\\\"data\\\""),
                "Image Base64 must not be serialized into tool text"
            );
        }
    }
    use super::*;
    #[test]
    fn free_text_effort_is_encoded_per_protocol_and_blank_defaults_are_omitted() {
        for (protocol, path, expected) in [
            (Protocol::OpenaiChat, "/reasoning_effort", "vendor-mode"),
            (
                Protocol::OpenaiResponses,
                "/reasoning/effort",
                "vendor-mode",
            ),
            (
                Protocol::AnthropicMessages,
                "/output_config/effort",
                "vendor-mode",
            ),
            (
                Protocol::Gemini,
                "/generationConfig/thinkingConfig/thinkingLevel",
                "VENDOR-MODE",
            ),
        ] {
            let body = request(
                &protocol,
                "model",
                "system",
                &[],
                &[],
                100,
                Some("vendor-mode"),
            )
            .unwrap();
            assert_eq!(body.pointer(path).unwrap(), expected);
            let default = request(&protocol, "model", "system", &[], &[], 100, None).unwrap();
            assert!(default.pointer(path).is_none());
        }
    }
    #[test]
    fn sse_handles_split_unicode_crlf_and_multiline_data() {
        let bytes = "data: {\r\ndata: \"text\":\"中文\"}\r\n\r\ndata: [DONE]\n\n".as_bytes();
        let mut sse = Sse::default();
        let mut events = vec![];
        for byte in bytes {
            events.extend(sse.push(&[*byte]).unwrap());
        }
        events.extend(sse.finish().unwrap());
        assert_eq!(events, vec![json!({"text":"中文"})]);
    }
    #[test]
    fn truncated_or_unterminated_response_never_yields_executable_calls() {
        for reason in [Value::Null, json!("length"), json!("content_filter")] {
            let mut decoder = Decoder::new(Protocol::OpenaiChat);
            decoder.push(&json!({"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call","function":{"name":"exec_command","arguments":"{\"cmd\":\"true\"}"}}]},"finish_reason":reason}]})).unwrap();
            assert!(decoder.finish().is_err());
        }
    }
    #[test]
    fn fragmented_tool_arguments_and_reasoning_content_roundtrip() {
        let mut decoder = Decoder::new(Protocol::OpenaiChat);
        decoder.push(&json!({"choices":[{"delta":{"reasoning_content":"analysis","tool_calls":[{"index":0,"id":"call","function":{"name":"exec_command","arguments":"{\"cmd\":"}}]}}]})).unwrap();
        decoder.push(&json!({"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"true\"}"}}]},"finish_reason":"tool_calls"}]})).unwrap();
        let message = decoder.finish().unwrap();
        assert_eq!(message.calls[0].arguments, json!({"cmd":"true"}));
        let body = request(
            &Protocol::OpenaiChat,
            "fixture",
            "system",
            &[message],
            &[],
            128,
            None,
        )
        .unwrap();
        assert_eq!(body["messages"][1]["reasoning_content"], "analysis");
    }
}

#[cfg(test)]
mod context_tests {
    use super::*;
    #[test]
    fn context_projection_excludes_duplicate_display_calls_and_saved_usage() {
        for protocol in [
            Protocol::OpenaiChat,
            Protocol::OpenaiResponses,
            Protocol::AnthropicMessages,
            Protocol::Gemini,
        ] {
            let replay = match protocol {
                Protocol::OpenaiChat => json!({"role":"assistant","content":"native"}),
                Protocol::OpenaiResponses => {
                    json!([{"type":"message","role":"assistant","content":[{"type":"output_text","text":"native"}]}])
                }
                Protocol::AnthropicMessages => json!([{"type":"text","text":"native"}]),
                Protocol::Gemini => json!([{"text":"native"}]),
                _ => unreachable!(),
            };
            let mut assistant = Message::text("assistant", "DISPLAY_ONLY".repeat(1000));
            assistant.replay = replay;
            assistant.calls.push(ToolCall {
                id: "internal".into(),
                name: "INTERNAL_ONLY".into(),
                arguments: json!({"duplicate":true}),
            });
            assistant.usage = Some(TokenUsage {
                input: 987654321,
                output: 100,
            });
            let payload = context_payload(&protocol, "fixture", "SYSTEM", &[assistant], &[])
                .unwrap()
                .to_string();
            assert!(payload.contains("native") && payload.contains("SYSTEM"));
            assert!(
                !payload.contains("DISPLAY_ONLY")
                    && !payload.contains("INTERNAL_ONLY")
                    && !payload.contains("987654321")
            );
            assert!(!payload.contains("max_tokens") && !payload.contains("maxOutputTokens"));
        }
    }
    #[test]
    fn stream_usage_is_normalized_once_and_partial_usage_stays_unknown() {
        let cases = [
            (
                Protocol::OpenaiChat,
                vec![
                    json!({"choices":[{"delta":{"content":"ok"},"finish_reason":"stop"}]}),
                    json!({"usage":{"prompt_tokens":33,"completion_tokens":10,"prompt_tokens_details":{"cached_tokens":10}}}),
                ],
            ),
            (
                Protocol::OpenaiResponses,
                vec![
                    json!({"type":"response.completed","response":{"status":"completed","output":[],"usage":{"input_tokens":33,"output_tokens":10,"input_tokens_details":{"cached_tokens":10}}}}),
                ],
            ),
            (
                Protocol::AnthropicMessages,
                vec![
                    json!({"type":"message_start","message":{"usage":{"input_tokens":20,"cache_read_input_tokens":10,"cache_creation_input_tokens":3,"output_tokens":1}}}),
                    json!({"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":10}}),
                    json!({"type":"message_stop"}),
                ],
            ),
            (
                Protocol::Gemini,
                vec![
                    json!({"candidates":[{"content":{"parts":[]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":33,"candidatesTokenCount":7,"thoughtsTokenCount":3,"cachedContentTokenCount":10}}),
                ],
            ),
        ];
        for (protocol, events) in cases {
            let mut decoder = Decoder::new(protocol);
            for event in &events {
                decoder.push(event).unwrap();
            }
            decoder.push(events.last().unwrap()).unwrap();
            let usage = decoder.finish().unwrap().usage.unwrap();
            assert_eq!((usage.input, usage.output), (33, 10));
        }
        let mut decoder = Decoder::new(Protocol::AnthropicMessages);
        decoder.push(&json!({"type":"message_start","message":{"usage":{"input_tokens":20,"output_tokens":1}}})).unwrap();
        decoder
            .push(&json!({"type":"message_delta","delta":{"stop_reason":"end_turn"}}))
            .unwrap();
        decoder.push(&json!({"type":"message_stop"})).unwrap();
        assert!(decoder.finish().unwrap().usage.is_none());
    }
}

#[cfg(test)]
mod contract_tests {
    use super::*;
    fn protocols() -> [Protocol; 4] {
        [
            Protocol::OpenaiChat,
            Protocol::OpenaiResponses,
            Protocol::AnthropicMessages,
            Protocol::Gemini,
        ]
    }
    #[test]
    fn tool_schema_required_parameters_survive_all_four_wire_formats() {
        let schema = json!({"type":"object","properties":{"partition":{"type":"string","enum":["left","right"]}},"required":["partition"],"additionalProperties":false});
        let tools = vec![
            json!({"name":"dispatch_partition","description":"Dispatch","inputSchema":schema}),
        ];
        for protocol in protocols() {
            let body = request(&protocol, "fixture", "system", &[], &tools, 128, None).unwrap();
            let actual = match protocol {
                Protocol::OpenaiChat => &body["tools"][0]["function"]["parameters"],
                Protocol::OpenaiResponses => &body["tools"][0]["parameters"],
                Protocol::AnthropicMessages => &body["tools"][0]["input_schema"],
                Protocol::Gemini => &body["tools"][0]["functionDeclarations"][0]["parameters"],
                _ => unreachable!(),
            };
            assert_eq!(actual, &schema, "{protocol:?}");
        }
    }
    #[test]
    fn invalid_or_misnamed_tool_schema_and_duplicate_names_fail_before_encoding() {
        let valid = json!({"name":"tool","description":"test","inputSchema":{"type":"object"}});
        for protocol in protocols() {
            for invalid in [
                json!({"name":"tool","parameters":{"type":"object"}}),
                json!({"name":"tool","inputSchema":null}),
                json!({"name":"tool","inputSchema":"object"}),
                json!({"name":"tool","inputSchema":{"type":"string"}}),
                json!({"name":"","inputSchema":{"type":"object"}}),
                json!({"name":"tool","inputSchema":{"type":"object","properties":[]}}),
                json!({"name":"tool","inputSchema":{"type":"object","required":[123]}}),
            ] {
                assert!(
                    request(&protocol, "fixture", "system", &[], &[invalid], 128, None).is_err(),
                    "{protocol:?}"
                );
            }
            assert!(
                request(
                    &protocol,
                    "fixture",
                    "system",
                    &[],
                    &[valid.clone(), valid.clone()],
                    128,
                    None
                )
                .is_err()
            );
        }
    }
    #[test]
    fn missing_partial_and_zero_usage_remain_distinct() {
        for usage in [
            json!({}),
            json!({"prompt_tokens":100}),
            json!({"completion_tokens":20}),
            json!({"prompt_tokens":0,"completion_tokens":0}),
        ] {
            let mut decoder = Decoder::new(Protocol::OpenaiChat);
            decoder.push(&json!({"choices":[{"delta":{"content":"done"},"finish_reason":"stop"}],"usage":usage})).unwrap();
            assert!(decoder.finish().unwrap().usage.is_none());
        }
    }
    #[test]
    fn terminal_failures_and_invalid_tool_arguments_cannot_become_executable_calls() {
        for protocol in protocols() {
            let mut decoder = Decoder::new(protocol.clone());
            let event = match protocol {
                Protocol::OpenaiChat => {
                    json!({"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call","function":{"name":"exec_command","arguments":"{bad"}}]},"finish_reason":"tool_calls"}]})
                }
                Protocol::OpenaiResponses => {
                    json!({"type":"response.completed","response":{"output":[{"type":"function_call","call_id":"call","name":"exec_command","arguments":"[]"}]}})
                }
                Protocol::AnthropicMessages => {
                    json!({"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"call","name":"exec_command","input":[]}})
                }
                Protocol::Gemini => {
                    json!({"candidates":[{"content":{"parts":[{"functionCall":{"id":"call","name":"exec_command","args":[]}}]},"finishReason":"STOP"}]})
                }
                _ => unreachable!(),
            };
            decoder.push(&event).unwrap();
            if protocol == Protocol::AnthropicMessages {
                decoder
                    .push(&json!({"type":"message_delta","delta":{"stop_reason":"tool_use"}}))
                    .unwrap();
                decoder.push(&json!({"type":"message_stop"})).unwrap();
            }
            assert!(decoder.finish().is_err());
        }
        for event in [
            json!({"type":"response.failed","response":{"usage":{"input_tokens":10,"output_tokens":1}}}),
            json!({"type":"response.incomplete","response":{"usage":{"input_tokens":10,"output_tokens":1}}}),
        ] {
            let mut decoder = Decoder::new(Protocol::OpenaiResponses);
            decoder.push(&event).unwrap();
            assert!(decoder.finish().is_err());
        }
    }
    #[test]
    fn excessive_usage_never_wraps_into_a_small_context_anchor() {
        let mut decoder = Decoder::new(Protocol::OpenaiChat);
        decoder.push(&json!({"choices":[{"delta":{"content":"done"},"finish_reason":"stop"}],"usage":{"prompt_tokens":u64::MAX,"completion_tokens":1}})).unwrap();
        assert!(decoder.finish().unwrap().usage.is_none());
        let mut observer = crate::codecs::UsageObserver::default();
        assert!(observer.observe(&Protocol::AnthropicMessages,&json!({"type":"message_start","message":{"usage":{"input_tokens":u64::MAX,"cache_read_input_tokens":1}}})).is_err());
        assert!(observer.observe(&Protocol::Gemini,&json!({"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":u64::MAX,"thoughtsTokenCount":1}})).is_err());
    }
    #[test]
    fn anthropic_initial_text_and_later_deltas_are_preserved_once() {
        for tail in ["", " world"] {
            let mut decoder = Decoder::new(Protocol::AnthropicMessages);
            assert_eq!(decoder.push(&json!({"type":"content_block_start","index":0,"content_block":{"type":"text","text":"hello"}})).unwrap(),"hello");
            if !tail.is_empty() {
                assert_eq!(decoder.push(&json!({"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":tail}})).unwrap(),tail);
            }
            decoder
                .push(&json!({"type":"message_delta","delta":{"stop_reason":"end_turn"}}))
                .unwrap();
            decoder.push(&json!({"type":"message_stop"})).unwrap();
            let message = decoder.finish().unwrap();
            assert_eq!(message.text, format!("hello{tail}"));
            assert_eq!(message.replay[0]["text"], format!("hello{tail}"));
        }
    }
}
