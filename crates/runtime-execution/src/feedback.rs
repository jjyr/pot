use anyhow::Result;
use futures::future::BoxFuture;
use pot_core::{ExecutionFeedback, ExecutionObservation};
use serde_json::Value;
use std::{path::PathBuf, sync::Arc};
use tokio::sync::watch;

pub(super) struct Feedback {
    pub inner: Arc<dyn ExecutionFeedback>,
    pub manager: super::Manager,
    pub cancel: watch::Receiver<bool>,
    pub stopped: watch::Receiver<bool>,
    pub execution_id: String,
}
async fn signalled(mut receiver: watch::Receiver<bool>) {
    loop {
        if *receiver.borrow_and_update() {
            return;
        }
        if receiver.changed().await.is_err() {
            return;
        }
    }
}
impl ExecutionFeedback for Feedback {
    fn resolve_execution_operation(&self, name: &str, args: Value) -> BoxFuture<'_, Result<Value>> {
        self.inner.resolve_execution_operation(name, args)
    }
    fn prepare_execution_operation(&self, name: &str, args: Value) -> BoxFuture<'_, Result<Value>> {
        self.inner.prepare_execution_operation(name, args)
    }

    fn execution_home(&self) -> Option<PathBuf> {
        self.inner.execution_home()
    }
    fn prepare_permission_operation(&self, name: &str, args: Value) -> Result<Value> {
        self.inner.prepare_permission_operation(name, args)
    }
    fn owner_authorization_available(&self) -> Result<bool> {
        self.inner.owner_authorization_available()
    }
    fn execute_authorized_host(&self, name: &str, args: Value) -> BoxFuture<'_, Result<Value>> {
        self.inner.execute_authorized_host(name, args)
    }
    fn request_permission(&self, request: Value) -> BoxFuture<'_, Result<Value>> {
        Box::pin(async move {
            tokio::select! {
                result = self.inner.request_permission(request) => result,
                _ = self.cancellation() => anyhow::bail!("Execution cancelled"),
            }
        })
    }
    fn observe(&self, event: &ExecutionObservation) -> Result<()> {
        self.manager.observe(&self.execution_id, event);
        self.inner.observe(event)
    }
    fn steering(&self, finalizing: bool) -> Result<Vec<(String, String)>> {
        self.inner.steering(finalizing)
    }
    fn acknowledge_steering(&self, ids: &[String]) -> Result<()> {
        self.inner.acknowledge_steering(ids)
    }
    fn fetch_steering(&self, finalizing: bool) -> BoxFuture<'_, Result<Vec<(String, String)>>> {
        self.inner.fetch_steering(finalizing)
    }
    fn commit_steering<'a>(&'a self, ids: &'a [String]) -> BoxFuture<'a, Result<()>> {
        self.inner.commit_steering(ids)
    }
    fn diagnostic(&self, event: &Value) -> Result<()> {
        let mut event = event.clone();
        if let Some(object) = event.as_object_mut() {
            object.insert("execution_id".into(), self.execution_id.clone().into());
        }
        self.inner.diagnostic(&event)
    }
    fn ready(&self) -> BoxFuture<'_, Result<()>> {
        Box::pin(async move {
            if self.cancelled() {
                return Ok(());
            }
            tokio::select! {
                result = self.inner.ready() => result,
                _ = self.cancellation() => Ok(()),
            }
        })
    }
    // Reconciliation must finish even after cancellation; never short-circuit settlement.
    fn settle(&self) -> BoxFuture<'_, Result<()>> {
        self.inner.settle()
    }
    fn load_context(&self) -> Result<Option<Value>> {
        self.inner.load_context()
    }
    fn save_context(&self, value: &Value) -> Result<()> {
        self.inner.save_context(value)
    }
    fn cancelled(&self) -> bool {
        *self.cancel.borrow() || *self.stopped.borrow() || self.inner.cancelled()
    }
    fn cancellation(&self) -> BoxFuture<'_, ()> {
        Box::pin(async move {
            if self.cancelled() {
                return;
            }
            tokio::select! {
                _ = signalled(self.cancel.clone()) => {},
                _ = signalled(self.stopped.clone()) => {},
                _ = self.inner.cancellation() => {},
            }
        })
    }
    fn record_turn(&self, request: &str) -> Result<()> {
        self.inner.record_turn(request)
    }
    fn input_streaming(&self, request: &str) -> Result<()> {
        self.inner.input_streaming(request)
    }
}

/// Preserve uncertainty when an executor cannot confirm process cleanup.
pub(super) struct Tools {
    pub inner: Arc<dyn pot_core::ToolExecutor>,
    pub cleanup_failed: Arc<std::sync::atomic::AtomicBool>,
}
impl pot_core::ToolExecutor for Tools {
    fn routes_execution_targets(&self) -> bool {
        self.inner.routes_execution_targets()
    }
    fn execute<'a>(
        &'a self,
        context: &'a pot_core::ExecutionContext,
        policy: &'a pot_core::ShellSettings,
        name: &'a str,
        args: &'a Value,
        call: &'a str,
        emit: &'a mut (dyn FnMut(pot_core::Update) -> Result<()> + Send),
    ) -> BoxFuture<'a, Result<Value>> {
        self.inner.execute(context, policy, name, args, call, emit)
    }
    fn finish<'a>(&'a self, context: &'a pot_core::ExecutionContext) -> BoxFuture<'a, Result<()>> {
        Box::pin(async move {
            let result = if context.feedback.cancelled() {
                self.inner.abort(context).await
            } else {
                self.inner.finish(context).await
            };
            if result.is_err() {
                self.cleanup_failed
                    .store(true, std::sync::atomic::Ordering::SeqCst);
            }
            result
        })
    }
    fn abort<'a>(&'a self, context: &'a pot_core::ExecutionContext) -> BoxFuture<'a, Result<()>> {
        Box::pin(async move {
            let result = self.inner.abort(context).await;
            if result.is_err() {
                self.cleanup_failed
                    .store(true, std::sync::atomic::Ordering::SeqCst);
            }
            result
        })
    }
}
