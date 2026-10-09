//! Credential-free JSON-lines runner; the root-owned Sandbox broker contains its entire cgroup.
use anyhow::Result;
use futures::future::BoxFuture;
use pot_core::*;
use pot_tool_executor::{Processes, tools};
use serde_json::{Value, json};
use std::{path::PathBuf, sync::Arc};
use tokio::io::{AsyncBufReadExt, BufReader};
struct Feedback;
impl ExecutionFeedback for Feedback {
    fn execution_home(&self) -> Option<PathBuf> {
        std::env::var_os("HOME").map(PathBuf::from)
    }
    fn cancelled(&self) -> bool {
        false
    }
    fn cancellation(&self) -> BoxFuture<'_, ()> {
        Box::pin(std::future::pending())
    }
    fn record_turn(&self, _: &str) -> Result<()> {
        Ok(())
    }
    fn input_streaming(&self, _: &str) -> Result<()> {
        Ok(())
    }
}
#[tokio::main]
async fn main() -> Result<()> {
    if std::env::args().nth(1).as_deref() == Some("--capabilities") {
        println!(
            "{}",
            json!({"protocol_version":1,"plugins":{"protocol_version":1,"runtime":"node","requirements":["node","chromium"]},"observation_modes":["text","mixed"]})
        );
        return Ok(());
    }
    let mut lines = BufReader::new(tokio::io::stdin()).lines();
    let mut processes = Processes::default();
    let mut plugins = pot_tool_executor::plugins::Plugins::default();
    while let Some(line) = lines.next_line().await? {
        let request: Value = serde_json::from_str(&line)?;
        let policy: ShellSettings = serde_json::from_value(request["policy"].clone())?;
        AgentSettings {
            shell: policy.clone(),
            ..Default::default()
        }
        .validate()?;
        let context = ExecutionContext {
            instructions: String::new(),
            skill_resources: Default::default(),
            data: PathBuf::new(),
            workspace: PathBuf::from(request["cwd"].as_str().unwrap()),
            owner: String::new(),
            role: ExecutionRole::Worker,
            backend_url: String::new(),
            lane_token: String::new(),
            main_prompt: String::new(),
            feedback: Arc::new(Feedback),
            tool_executor: None,
        };
        let name = request["operation"].as_str().unwrap_or("");
        let args = &request["args"];
        let result = if name == "inspect" {
            let mut result = processes.inspect();
            let sessions = plugins.sessions();
            if !sessions.is_empty() {
                result["active"] = json!(true);
                result["sessions"]
                    .as_array_mut()
                    .unwrap()
                    .extend(sessions.into_iter().map(Value::String));
            }
            Ok(result)
        } else {
            match tools::validate(name, args) {
                Err(e) => Err(e),
                Ok(()) if name == "apply_patch" => tools::patch(&context, args),
                Ok(()) if name == "plugin_call" => plugins.execute(args).await,
                Ok(()) => {
                    processes
                        .execute(
                            &context,
                            &policy,
                            name,
                            args,
                            request["id"].as_str().unwrap_or(""),
                            &mut |event| {
                                println!("{}", json!({"event":event}));
                                Ok(())
                            },
                        )
                        .await
                }
            }
        };
        println!(
            "{}",
            json!({"result":result.unwrap_or_else(|e|json!({"isError":true,"error":e.to_string()}))})
        );
    }
    plugins.shutdown().await?;
    processes.shutdown().await
}
