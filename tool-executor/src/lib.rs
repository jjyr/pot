//! Explicit local execution backend, used inside Sandbox runners and isolated eval fixtures.
mod processes;
mod snapshot;
mod terminal;
pub mod tools;
use anyhow::Result;
use futures::future::BoxFuture;
use pot_core::{ExecutionContext, ShellSettings, ToolExecutor, Update};
pub use processes::Processes;
use serde_json::Value;
#[derive(Default)]
pub struct LocalTools(tokio::sync::Mutex<Processes>);
impl ToolExecutor for LocalTools {
    fn execute<'a>(
        &'a self,
        context: &'a ExecutionContext,
        policy: &'a ShellSettings,
        name: &'a str,
        args: &'a Value,
        call: &'a str,
        emit: &'a mut (dyn FnMut(Update) -> Result<()> + Send),
    ) -> BoxFuture<'a, Result<Value>> {
        Box::pin(async move {
            tools::validate(name, args)?;
            if name == "apply_patch" {
                tools::patch(context, args)
            } else {
                self.0
                    .lock()
                    .await
                    .execute(context, policy, name, args, call, emit)
                    .await
            }
        })
    }
    fn finish<'a>(&'a self, _: &'a ExecutionContext) -> BoxFuture<'a, Result<()>> {
        Box::pin(async { self.0.lock().await.shutdown().await })
    }
}
