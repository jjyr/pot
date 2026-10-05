//! Bounded API smoke-test adapter. No automatic retries or unrestricted tools.
use super::*;
use futures::StreamExt;
use std::collections::BTreeMap;

#[derive(Deserialize)]
pub struct Config {
    pub base_url: String,
    pub api_key: String,
    pub model: String,
    pub request_limit: u64,
    pub max_tokens: u64,
}
impl Config {
    pub fn load(path: &std::path::Path) -> Result<Self> {
        let c: Self = serde_json::from_slice(&std::fs::read(path)?)?;
        anyhow::ensure!(
            c.base_url == "https://api.deepseek.com",
            "Unexpected DeepSeek endpoint"
        );
        anyhow::ensure!(c.model == "deepseek-v4-flash", "Unexpected Flash model");
        anyhow::ensure!(
            !c.api_key.is_empty() && c.request_limit <= 3 && (1..=128).contains(&c.max_tokens),
            "Invalid smoke-test budget"
        );
        Ok(c)
    }
}
pub use pot_storage::budget::{BudgetSettings, reserve_in};
fn reserve_attempt(data: &std::path::Path, limit: u64) -> Result<u64> {
    reserve_in(&data.join("deepseek-attempts"), limit)
}
fn write_file(workspace: &std::path::Path, args: &str) -> Result<String> {
    let args: Value = serde_json::from_str(args)?;
    let name = args["name"]
        .as_str()
        .ok_or_else(|| anyhow!("Missing name"))?;
    let content = args["content"]
        .as_str()
        .ok_or_else(|| anyhow!("Missing content"))?;
    anyhow::ensure!(
        !name.is_empty()
            && name.len() < 100
            && name
                .bytes()
                .all(|b| b.is_ascii_alphanumeric() || b"-_.".contains(&b))
            && !name.starts_with('.'),
        "Only a plain filename is allowed"
    );
    anyhow::ensure!(content.len() <= 4096, "File too large");
    // create_new prevents following an existing symlink or replacing a file.
    use std::io::Write;
    let mut file = std::fs::OpenOptions::new()
        .write(true)
        .create_new(true)
        .open(workspace.join(name))?;
    file.write_all(content.as_bytes())?;
    Ok(format!("Created {name} ({} bytes)", content.len()))
}
pub async fn run(s: &Service, req: SendMessage, path: &std::path::Path) -> Result<()> {
    let mut user = s
        .inner
        .lock()
        .unwrap()
        .store
        .duplicate(&req.request_id)?
        .ok_or_else(|| anyhow!("Missing request"))?;
    let mut reply = Message {
        id: id(),
        conversation_id: "main".into(),
        role: Role::Assistant,
        text: String::new(),
        status: MessageStatus::Streaming,
        created_at_ms: now(),
        request_id: None,
    };
    s.save(reply.clone())?;
    let result:Result<()>=async {
        let c=Config::load(path)?;
        let saved=s.inner.lock().unwrap().store.get("deepseek.history")?;
        let mut history:Vec<Value>=saved.map(|h|serde_json::from_str(&h)).transpose()?.unwrap_or_else(||vec![json!({"role":"system","content":"Be concise. Use write_file only when requested. It creates a new small file in the sandbox workspace. Never claim a tool succeeded without its result."})]);
        history.push(json!({"role":"user","content":req.text}));
        anyhow::ensure!(serde_json::to_vec(&history)?.len() <= 12000,"Smoke-test context limit reached");
        let client=reqwest::Client::builder().retry(reqwest::retry::never()).timeout(std::time::Duration::from_secs(40)).build()?;
        for round in 0..2 {
            let attempt=reserve_attempt(path.parent().ok_or_else(||anyhow!("Missing config directory"))?,c.request_limit)?;
            s.inner.lock().unwrap().store.set("deepseek.requests",&attempt.to_string())?;
            s.publish_budget()?;
            let body=json!({"model":c.model,"messages":history,"stream":true,"stream_options":{"include_usage":true},"max_tokens":c.max_tokens,"thinking":{"type":"disabled"},"tools":[{"type":"function","function":{"name":"write_file","description":"Create a new small text file in the sandbox workspace","parameters":{"type":"object","properties":{"name":{"type":"string"},"content":{"type":"string"}},"required":["name","content"],"additionalProperties":false}}}],"tool_choice":if round==0{"auto"}else{"none"}});
            let response=client.post(format!("{}/chat/completions",c.base_url)).bearer_auth(&c.api_key).json(&body).send().await?;
            anyhow::ensure!(response.status().is_success(),"DeepSeek HTTP {} (no retry)",response.status());
            let mut stream=response.bytes_stream();let mut buffer=Vec::new();let mut text=String::new();let mut calls:BTreeMap<u64,(String,String,String)>=BTreeMap::new();let mut finished=false;let mut done=false;
            while let Some(chunk)=stream.next().await {
                buffer.extend_from_slice(&chunk?);
                anyhow::ensure!(buffer.len()<1024*1024,"Oversized SSE event");
                while let Some(end)=buffer.iter().position(|b|*b==b'\n') {
                    let line:Vec<u8>=buffer.drain(..=end).collect();let line=std::str::from_utf8(&line)?.trim();
                    let Some(data)=line.strip_prefix("data:").map(str::trim)else{continue};
                    if data=="[DONE]"{done=true;continue;}
                    let event:Value=serde_json::from_str(data)?;
                    anyhow::ensure!(event.get("error").is_none(),"DeepSeek stream error");
                    if event.get("usage").is_some_and(|v|!v.is_null()) {
                        let inner=s.inner.lock().unwrap();
                        inner.store.set("deepseek.last_usage",&event["usage"].to_string())?;
                        let mut usage:Vec<Value>=inner.store.get("deepseek.usage")?.map(|v|serde_json::from_str(&v)).transpose()?.unwrap_or_default();
                        usage.push(event["usage"].clone());
                        inner.store.set("deepseek.usage",&serde_json::to_string(&usage)?)?;
                    }
                    let choice=&event["choices"][0];let delta=&choice["delta"];
                    if let Some(t)=delta["content"].as_str(){text.push_str(t);reply.text.push_str(t);s.save(reply.clone())?;}
                    if let Some(parts)=delta["tool_calls"].as_array(){for p in parts {
                        let index=p["index"].as_u64().ok_or_else(||anyhow!("Missing tool index"))?;
                        anyhow::ensure!(index==0,"Only one file tool per turn is allowed");
                        let call=calls.entry(index).or_default();
                        if let Some(v)=p["id"].as_str(){call.0.push_str(v);}
                        if let Some(v)=p["function"]["name"].as_str(){call.1.push_str(v);}
                        if let Some(v)=p["function"]["arguments"].as_str(){call.2.push_str(v);}
                    }}
                    if let Some(reason)=choice["finish_reason"].as_str(){anyhow::ensure!(reason=="stop" || reason=="tool_calls","Output token limit or unexpected finish reason");finished=true;}
                }
            }
            anyhow::ensure!(done && finished,"Incomplete DeepSeek stream");
            if calls.is_empty(){history.push(json!({"role":"assistant","content":text}));s.inner.lock().unwrap().store.set("deepseek.history",&serde_json::to_string(&history)?)?;return Ok(());}
            anyhow::ensure!(round==0,"Tool round budget exhausted");
            let mut tool_calls=Vec::new();let mut results=Vec::new();
            for (_, (call_id,name,args)) in calls {
                anyhow::ensure!(!call_id.is_empty() && name=="write_file","Unsupported tool");
                tool_calls.push(json!({"id":call_id,"type":"function","function":{"name":name,"arguments":args}}));
                let output=match write_file(&s.workspace,&args){Ok(v)=>v,Err(e)=>format!("Tool failed: {e}")};
                s.save(Message{id:id(),conversation_id:"main".into(),role:Role::Tool,text:output.clone(),status:if output.starts_with("Tool failed:"){MessageStatus::Failed}else{MessageStatus::Complete},created_at_ms:now(),request_id:None})?;
                results.push(json!({"role":"tool","tool_call_id":call_id,"content":output}));
            }
            history.push(json!({"role":"assistant","content":text,"tool_calls":tool_calls}));history.extend(results);
            s.inner.lock().unwrap().store.set("deepseek.history",&serde_json::to_string(&history)?)?;
        }
        bail!("Turn budget exhausted")
    }.await;
    match result {
        Ok(()) => {
            reply.status = MessageStatus::Complete;
            user.status = MessageStatus::Complete;
        }
        Err(e) => {
            reply.status = MessageStatus::Failed;
            user.status = MessageStatus::Failed;
            reply.text.push_str(&format!("\n{e}"));
        }
    }
    s.save(reply)?;
    s.save(user)?;
    Ok(())
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn attempts_survive_reopen_and_never_exceed_budget() {
        let root = std::env::temp_dir().join(id());
        std::fs::create_dir(&root).unwrap();
        assert_eq!(reserve_attempt(&root, 2).unwrap(), 1);
        assert_eq!(reserve_attempt(&root, 2).unwrap(), 2);
        assert!(reserve_attempt(&root, 2).is_err());
        std::fs::remove_dir_all(root).unwrap();
    }
    #[test]
    fn file_tool_rejects_escape_and_overwrite() {
        let root = std::env::temp_dir().join(id());
        std::fs::create_dir(&root).unwrap();
        assert!(write_file(&root, r#"{"name":"../escape","content":"x"}"#).is_err());
        write_file(&root, r#"{"name":"check.txt","content":"ok"}"#).unwrap();
        assert!(write_file(&root, r#"{"name":"check.txt","content":"changed"}"#).is_err());
        assert_eq!(
            std::fs::read_to_string(root.join("check.txt")).unwrap(),
            "ok"
        );
        std::fs::remove_dir_all(root).unwrap();
    }
}
