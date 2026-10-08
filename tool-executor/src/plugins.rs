//! Generic persistent Node plugin runners in the existing credential-free Guest cgroup.
use anyhow::{Result, anyhow, ensure};
use pot_core::plugins::{PluginBundle, validate_call};
use serde_json::{Value, json};
use std::{collections::BTreeMap, path::PathBuf};
use tokio::{
    io::{AsyncBufReadExt, AsyncWriteExt, BufReader},
    process::{Child, ChildStdin, ChildStdout, Command},
};
struct Instance {
    child: Child,
    input: ChildStdin,
    output: BufReader<ChildStdout>,
    directory: PathBuf,
    digest: String,
    mode: String,
}
impl Instance {
    async fn start(bundle: &PluginBundle, mode: &str) -> Result<Self> {
        let directory = std::env::temp_dir().join(format!("pot-plugin-{}", uuid::Uuid::new_v4()));
        std::fs::create_dir(&directory)?;
        let build = || -> Result<Child> {
            for (name, code) in &bundle.files {
                let path = directory.join(name);
                std::fs::create_dir_all(path.parent().unwrap())?;
                std::fs::write(path, code)?;
            }
            Ok(Command::new("node")
                .args(["--input-type=module", "-e", include_str!("plugin-host.mjs")])
                .arg(directory.join(&bundle.manifest.entrypoint))
                .stdin(std::process::Stdio::piped())
                .stdout(std::process::Stdio::piped())
                .stderr(std::process::Stdio::null())
                .kill_on_drop(true)
                .spawn()?)
        };
        let mut child = match build() {
            Ok(child) => child,
            Err(e) => {
                let _ = std::fs::remove_dir_all(&directory);
                return Err(e);
            }
        };
        let input = child.stdin.take().unwrap();
        let output = BufReader::new(child.stdout.take().unwrap());
        Ok(Self {
            child,
            input,
            output,
            directory,
            digest: bundle.digest.clone(),
            mode: mode.into(),
        })
    }
    async fn exchange(&mut self, args: &Value) -> Result<Value> {
        self.input
            .write_all(
                format!(
                    "{}\n",
                    json!({"tool":args["tool"],"input":args["input"],"mode":args["mode"]})
                )
                .as_bytes(),
            )
            .await?;
        self.input.flush().await?;
        use tokio::io::AsyncReadExt;
        let mut line = Vec::new();
        (&mut self.output)
            .take(2 * 1024 * 1024)
            .read_until(b'\n', &mut line)
            .await?;
        ensure!(
            line.last() == Some(&b'\n'),
            "Plugin response truncated or process ended; outcome unknown"
        );
        let value: Value = serde_json::from_slice(&line)?;
        ensure!(value.is_object(), "Invalid plugin response");
        let content = value["content"]
            .as_array()
            .ok_or_else(|| anyhow!("Invalid plugin content"))?;
        ensure!(content.len() <= 1024, "Too many plugin output blocks");
        for block in content {
            match block["type"].as_str() {
                Some("text") => ensure!(block["text"].is_string(), "Invalid plugin text"),
                Some("image") => {
                    ensure!(
                        self.mode == "mixed",
                        "Image output requires visual model capability"
                    );
                    crate::validate_plugin_image(block)?;
                }
                _ => anyhow::bail!("Unsupported plugin output"),
            }
        }
        Ok(value)
    }
    async fn shutdown(mut self) -> Result<()> {
        drop(self.input);
        if tokio::time::timeout(std::time::Duration::from_secs(5), self.child.wait())
            .await
            .is_err()
        {
            self.child.kill().await?;
            self.child.wait().await?;
        }
        std::fs::remove_dir_all(self.directory)?;
        Ok(())
    }
}
#[derive(Default)]
pub struct Plugins {
    instances: BTreeMap<String, Instance>,
}
impl Plugins {
    pub fn sessions(&self) -> Vec<String> {
        self.instances.keys().cloned().collect()
    }
    pub async fn execute(&mut self, args: &Value) -> Result<Value> {
        let bundle = validate_call(args)?;
        let session = if let Some(session) = args["session_id"].as_str() {
            let instance = self
                .instances
                .get(session)
                .ok_or_else(|| anyhow!("Plugin session expired; explicitly start a new REPL"))?;
            ensure!(
                instance.digest == bundle.digest && instance.mode == args["mode"].as_str().unwrap(),
                "Plugin session binding changed"
            );
            session.to_owned()
        } else {
            ensure!(self.instances.len() < 8, "Plugin session limit reached");
            let session = uuid::Uuid::new_v4().to_string();
            self.instances.insert(
                session.clone(),
                Instance::start(&bundle, args["mode"].as_str().unwrap()).await?,
            );
            session
        };
        if args["input"]["reset"] == true {
            self.instances.remove(&session).unwrap().shutdown().await?;
            return Ok(json!({"closed":true}));
        }
        let result = tokio::time::timeout(
            std::time::Duration::from_secs(30),
            self.instances.get_mut(&session).unwrap().exchange(args),
        )
        .await;
        match result {
            Ok(Ok(mut value)) => {
                value["session_id"] = json!(session);
                Ok(value)
            }
            other => {
                self.instances.remove(&session).unwrap().shutdown().await?;
                match other {
                    Ok(Err(e)) => Err(e),
                    _ => Err(anyhow!(
                        "Plugin deadline exceeded; session ended, prior effects may have occurred"
                    )),
                }
            }
        }
    }
    pub async fn shutdown(&mut self) -> Result<()> {
        for (_, instance) in std::mem::take(&mut self.instances) {
            instance.shutdown().await?;
        }
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    fn args(input: Value) -> Value {
        pot_core::plugins::call(
            PluginBundle::builtin("computer-use").unwrap(),
            "cua_repl",
            input,
            false,
        )
        .unwrap()
    }
    #[tokio::test]
    async fn repl_variables_errors_reset_and_package_identity_are_persistent() {
        let mut plugins = Plugins::default();
        let first = plugins
            .execute(&args(
                json!({"code":"let counter = await Promise.resolve(41); nodeRepl.write(counter);"}),
            ))
            .await
            .unwrap();
        let id = first["session_id"].as_str().unwrap();
        assert_eq!(plugins.sessions(), [id]);
        assert!(first["content"].to_string().contains("Computer-use API"));
        let next = plugins
            .execute(&args(
                json!({"session_id":id,"code":"counter++; nodeRepl.write(counter);"}),
            ))
            .await
            .unwrap();
        assert_eq!(next["content"][0]["text"], "42");
        let failed = plugins
            .execute(&args(
                json!({"session_id":id,"code":"throw new Error('fixture error');"}),
            ))
            .await
            .unwrap();
        assert_eq!(failed["isError"], true);
        assert_eq!(failed["session_id"], id);
        let recovered = plugins
            .execute(&args(
                json!({"session_id":id,"code":"nodeRepl.write(counter);"}),
            ))
            .await
            .unwrap();
        assert_eq!(recovered["content"][0]["text"], "42");
        assert!(plugins.execute(&args(json!({"reset":true}))).await.is_err());
        assert_eq!(plugins.sessions(), [id]);
        let mut changed = args(json!({"session_id":id,"code":"nodeRepl.write(counter);"}));
        let mut bundle: PluginBundle = serde_json::from_value(changed["plugin"].clone()).unwrap();
        bundle.manifest.version = "changed".into();
        bundle.digest = bundle.hash().unwrap();
        changed["plugin"] = serde_json::to_value(bundle).unwrap();
        assert!(
            plugins
                .execute(&changed)
                .await
                .unwrap_err()
                .to_string()
                .contains("binding changed")
        );
        let closed = plugins
            .execute(&args(json!({"session_id":id,"reset":true})))
            .await
            .unwrap();
        assert_eq!(closed["closed"], true);
        assert!(plugins.sessions().is_empty());
        assert!(
            plugins
                .execute(&args(json!({"session_id":id,"code":"counter;"})))
                .await
                .is_err()
        );
    }
    #[tokio::test]
    async fn generic_executor_loads_an_independent_plugin_and_keeps_tool_state() {
        let mut bundle = PluginBundle::builtin("computer-use").unwrap();
        bundle.manifest.id = "counter".into();
        bundle.manifest.requirements = vec!["node".into()];
        bundle.manifest.tools = vec![
            json!({"name":"count","description":"count","inputSchema":{"type":"object","properties":{"session_id":{"type":"string"}},"additionalProperties":false}}),
        ];
        bundle.files.insert("index.mjs".into(), "export function createPlugin({emit}) { let n=0; return {async execute(){emit({type:'text',text:String(++n)});},async dispose(){}}; }".into());
        bundle.digest = bundle.hash().unwrap();
        let mut plugins = Plugins::default();
        let first = plugins
            .execute(&pot_core::plugins::call(bundle.clone(), "count", json!({}), false).unwrap())
            .await
            .unwrap();
        assert_eq!(first["content"][0]["text"], "1");
        let second = plugins
            .execute(
                &pot_core::plugins::call(
                    bundle,
                    "count",
                    json!({"session_id":first["session_id"]}),
                    false,
                )
                .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(second["content"][0]["text"], "2");
        plugins.shutdown().await.unwrap();
    }
}
