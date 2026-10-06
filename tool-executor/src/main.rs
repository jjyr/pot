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
    fn cancelled(&self) -> bool {
        false
    }
    fn cancellation(&self) -> BoxFuture<'_, ()> {
        Box::pin(std::future::pending())
    }
    fn native_identity(&self, _: &str) -> Result<()> {
        Ok(())
    }
    fn binding_broken(&self, _: &str) -> Result<()> {
        Ok(())
    }
    fn needs_rebind(&self, _: &str) -> Result<()> {
        Ok(())
    }
    fn record_turn(&self, _: &str) -> Result<()> {
        Ok(())
    }
    fn input_streaming(&self, _: &str) -> Result<()> {
        Ok(())
    }
    fn begin_usage(&self, _: &str, _: &ResolvedSpec, _: u64) -> Result<()> {
        Ok(())
    }
    fn initialize_usage(&self, _: &str) -> Result<()> {
        Ok(())
    }
    fn cumulative_usage(&self, _: &str, _: &str, _: &Value) -> Result<()> {
        Ok(())
    }
    fn incomplete_usage(&self, _: &str) -> Result<()> {
        Ok(())
    }
}
#[tokio::main]
async fn main() -> Result<()> {
    let mut lines = BufReader::new(tokio::io::stdin()).lines();
    let mut processes = Processes::default();
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
        let result = match tools::validate(name, args) {
            Err(e) => Err(e),
            Ok(()) if name == "apply_patch" => tools::patch(&context, args),
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
        };
        println!(
            "{}",
            json!({"result":result.unwrap_or_else(|e|json!({"isError":true,"error":e.to_string()}))})
        );
    }
    processes.shutdown().await
}
