//! Wire formats stay native. Codecs only constrain output and observe usage.
use anyhow::{Result, anyhow};
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};

#[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum Protocol {
    OpenaiChat,
    OpenaiResponses,
    AnthropicMessages,
    Gemini,
    Unknown,
}
impl Protocol {
    pub fn name(&self) -> &'static str {
        match self {
            Self::OpenaiChat => "openai_chat",
            Self::OpenaiResponses => "openai_responses",
            Self::AnthropicMessages => "anthropic_messages",
            Self::Gemini => "gemini",
            Self::Unknown => "unknown",
        }
    }
    pub fn from_name(name: &str) -> Result<Self> {
        Ok(serde_json::from_value(json!(name))?)
    }
    pub fn path_matches(&self, path: &str, model: &str) -> bool {
        match self {
            Self::OpenaiChat | Self::Unknown => path == "v1/chat/completions",
            Self::OpenaiResponses => path == "v1/responses",
            Self::AnthropicMessages => path == "v1/messages",
            Self::Gemini => path == format!("v1/models/{model}:streamGenerateContent"),
        }
    }
    pub fn clamp(&self, body: &mut Value, cap: u64) -> Result<u64> {
        anyhow::ensure!(
            body.is_object() && cap > 0,
            "Invalid model request or output cap"
        );
        if *self == Self::Unknown {
            return Ok(cap);
        }
        let target = match self {
            Self::OpenaiChat => {
                if body.get("stream_options").is_none() {
                    body["stream_options"] = json!({});
                }
                anyhow::ensure!(body["stream_options"].is_object(), "Invalid stream options");
                body["stream_options"]["include_usage"] = json!(true);
                // Keep the native field selected by the client, including modern models.
                if body.get("max_completion_tokens").is_some() {
                    let old = body.as_object_mut().unwrap().remove("max_tokens");
                    if let Some(old) = old.and_then(|v| v.as_u64()) {
                        body["max_completion_tokens"] =
                            json!(old.min(body["max_completion_tokens"].as_u64().unwrap_or(cap)));
                    }
                    &mut body["max_completion_tokens"]
                } else {
                    &mut body["max_tokens"]
                }
            }
            Self::OpenaiResponses => &mut body["max_output_tokens"],
            Self::AnthropicMessages => &mut body["max_tokens"],
            Self::Gemini => {
                if body.get("generationConfig").is_none() {
                    body["generationConfig"] = json!({});
                }
                anyhow::ensure!(
                    body["generationConfig"].is_object(),
                    "Invalid generation config"
                );
                &mut body["generationConfig"]["maxOutputTokens"]
            }
            Self::Unknown => unreachable!(),
        };
        let requested = if target.is_null() {
            cap
        } else {
            target
                .as_u64()
                .ok_or_else(|| anyhow!("Invalid output cap"))?
        };
        let output = requested.max(1).min(cap);
        *target = json!(output);
        Ok(output)
    }
}

#[derive(Default)]
pub struct UsageObserver {
    anthropic_input: Option<u64>,
    anthropic_cache: u64,
}
impl UsageObserver {
    pub fn observe(&mut self, protocol: &Protocol, event: &Value) -> Result<Option<Value>> {
        let normalized = match protocol {
            Protocol::OpenaiChat => {
                if !event["usage"].is_object() {
                    return Ok(None);
                }
                event["usage"].clone()
            }
            Protocol::OpenaiResponses => {
                if !matches!(
                    event["type"].as_str(),
                    Some("response.completed" | "response.incomplete" | "response.failed")
                ) {
                    return Ok(None);
                }
                let usage = &event["response"]["usage"];
                if !usage.is_object() {
                    return Ok(None);
                }
                json!({"prompt_tokens":usage["input_tokens"],"completion_tokens":usage["output_tokens"],"prompt_cache_hit_tokens":usage["input_tokens_details"]["cached_tokens"]})
            }
            Protocol::AnthropicMessages => {
                if event["type"] == "message_start" {
                    let u = &event["message"]["usage"];
                    if let Some(input) = u["input_tokens"].as_u64() {
                        self.anthropic_cache = u["cache_read_input_tokens"].as_u64().unwrap_or(0);
                        self.anthropic_input = Some(
                            input
                                .checked_add(self.anthropic_cache)
                                .and_then(|v| {
                                    v.checked_add(
                                        u["cache_creation_input_tokens"].as_u64().unwrap_or(0),
                                    )
                                })
                                .ok_or_else(|| anyhow!("Anthropic usage overflow"))?,
                        );
                    }
                    // The initial output count is not a final report. Keep reservation.
                    json!({"prompt_tokens":self.anthropic_input,"prompt_cache_hit_tokens":self.anthropic_cache})
                } else if event["type"] == "message_delta" && event["usage"].is_object() {
                    json!({"prompt_tokens":self.anthropic_input,"completion_tokens":event["usage"]["output_tokens"],"prompt_cache_hit_tokens":self.anthropic_cache})
                } else {
                    return Ok(None);
                }
            }
            Protocol::Gemini => {
                let u = &event["usageMetadata"];
                if !u.is_object() {
                    return Ok(None);
                }
                let output = u["candidatesTokenCount"]
                    .as_u64()
                    .map(|v| {
                        v.checked_add(u["thoughtsTokenCount"].as_u64().unwrap_or(0))
                            .ok_or_else(|| anyhow!("Gemini usage overflow"))
                    })
                    .transpose()?;
                json!({"prompt_tokens":u["promptTokenCount"],"completion_tokens":output,"prompt_cache_hit_tokens":u["cachedContentTokenCount"]})
            }
            Protocol::Unknown => return Ok(None),
        };
        Ok(Some(normalized))
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use pot_storage::now;
    use pot_usage as usage;
    #[test]
    fn formats_keep_payloads_and_only_bound_output() {
        for (p, mut body, pointer) in [
            (
                Protocol::OpenaiResponses,
                json!({"input":[{"role":"user","content":"hello"}],"max_output_tokens":100}),
                "/max_output_tokens",
            ),
            (
                Protocol::AnthropicMessages,
                json!({"messages":[{"role":"user","content":"hello"}],"max_tokens":100}),
                "/max_tokens",
            ),
            (
                Protocol::Gemini,
                json!({"contents":[{"parts":[{"text":"hello"}]}],"generationConfig":{"maxOutputTokens":100,"temperature":0.2}}),
                "/generationConfig/maxOutputTokens",
            ),
        ] {
            let mut expected = body.clone();
            *expected.pointer_mut(pointer).unwrap() = json!(10);
            assert_eq!(p.clamp(&mut body, 10).unwrap(), 10);
            assert_eq!(body, expected);
        }
    }
    #[test]
    fn paths_cannot_select_another_model_or_protocol() {
        assert!(
            Protocol::Gemini
                .path_matches("v1/models/gemini-test:streamGenerateContent", "gemini-test")
        );
        assert!(
            !Protocol::Gemini.path_matches("v1/models/other:streamGenerateContent", "gemini-test")
        );
        assert!(!Protocol::AnthropicMessages.path_matches("v1/responses", "test"));
        assert!(!Protocol::OpenaiResponses.path_matches("https://elsewhere/responses", "test"));
    }
    #[test]
    fn partial_anthropic_usage_keeps_reservation_until_final_output() {
        let ledger = usage::Ledger::open(std::path::Path::new(":memory:")).unwrap();
        ledger
            .reserve("r", "main", "turn", now(), "mock", 100, 20)
            .unwrap();
        let mut observer = UsageObserver::default();
        let partial = observer.observe(&Protocol::AnthropicMessages,&json!({"type":"message_start","message":{"usage":{"input_tokens":20,"output_tokens":0}}})).unwrap().unwrap();
        ledger.settle("r", &partial).unwrap();
        let reserved: u64 = ledger
            .0
            .query_row("SELECT reserved FROM requests WHERE id='r'", [], |r| {
                r.get(0)
            })
            .unwrap();
        assert_eq!(reserved, 120);
        let end = observer
            .observe(
                &Protocol::AnthropicMessages,
                &json!({"type":"message_delta","usage":{"output_tokens":5}}),
            )
            .unwrap()
            .unwrap();
        ledger.settle("r", &end).unwrap();
        ledger.settle("r", &end).unwrap();
        let counts: (u64, u64, u64) = ledger
            .0
            .query_row(
                "SELECT input,output,reserved FROM requests WHERE id='r'",
                [],
                |r| Ok((r.get(0)?, r.get(1)?, r.get(2)?)),
            )
            .unwrap();
        assert_eq!(counts, (20, 5, 0));
    }
    #[test]
    fn usage_handles_partial_cumulative_cache_and_reasoning() {
        let mut observer = UsageObserver::default();
        let p = Protocol::AnthropicMessages;
        let start = observer.observe(&p,&json!({"type":"message_start","message":{"usage":{"input_tokens":20,"cache_read_input_tokens":10,"cache_creation_input_tokens":3,"output_tokens":1}}})).unwrap().unwrap();
        assert_eq!(start["prompt_tokens"], 33);
        assert!(start["completion_tokens"].is_null());
        let end = json!({"type":"message_delta","usage":{"output_tokens":5}});
        let usage = observer.observe(&p, &end).unwrap().unwrap();
        assert_eq!(usage["completion_tokens"], 5);
        assert_eq!(usage["prompt_cache_hit_tokens"], 10);
        assert_eq!(observer.observe(&p, &end).unwrap().unwrap(), usage);
        let gemini = observer.observe(&Protocol::Gemini,&json!({"usageMetadata":{"promptTokenCount":50,"candidatesTokenCount":7,"thoughtsTokenCount":3,"cachedContentTokenCount":12}})).unwrap().unwrap();
        assert_eq!(gemini["completion_tokens"], 10);
        assert_eq!(gemini["prompt_tokens"], 50);
        assert!(
            observer
                .observe(&Protocol::Unknown, &end)
                .unwrap()
                .is_none()
        );
    }
}
