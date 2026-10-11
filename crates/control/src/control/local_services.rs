//! In-memory Control services for the local Agent. No loopback HTTP or credentials.
use crate::*;
use futures::{StreamExt, future::BoxFuture, stream::BoxStream};

pub(crate) struct LocalServices(pub Service);
fn failure(error: (StatusCode, Json<ApiError>)) -> anyhow::Error {
    anyhow!(
        "Control service rejected request ({}): {}",
        error.0,
        serde_json::to_value(error.1.0).unwrap_or_default()
    )
}
impl pot_core::ExecutionServices for LocalServices {
    fn model(
        &self,
        request: pot_core::ModelRequest,
    ) -> BoxFuture<'_, Result<BoxStream<'_, Result<Vec<u8>>>>> {
        Box::pin(async move {
            let spec = agent::thread(&self.0)?.config;
            anyhow::ensure!(
                request.provider == spec.provider
                    && request.model == spec.model
                    && request.protocol == spec.gateway_protocol,
                "Model differs from fixed run configuration"
            );
            let response = gateway::call(
                &self.0,
                request.body,
                request.compaction_id.as_deref(),
                None,
            )
            .await
            .map_err(failure)?;
            Ok(response
                .into_body()
                .into_data_stream()
                .map(|chunk| chunk.map(|b| b.to_vec()).map_err(Into::into))
                .boxed())
        })
    }
    fn rpc<'a>(
        &'a self,
        session: Option<&'a str>,
        id: &'a str,
        method: &'a str,
        params: Value,
    ) -> BoxFuture<'a, Result<(Value, Option<String>)>> {
        Box::pin(async move {
            let response = tool_bridge::dispatch(
                self.0.clone(),
                session,
                json!({"jsonrpc":"2.0","id":id,"method":method,"params":params}),
            )
            .await
            .map_err(failure)?;
            let session = response
                .headers()
                .get("mcp-session-id")
                .and_then(|v| v.to_str().ok())
                .map(str::to_owned);
            let body =
                axum::body::to_bytes(response.into_body(), execution::MAX_MODEL_BODY).await?;
            let value: Value = serde_json::from_slice(&body)?;
            anyhow::ensure!(
                value["error"].is_null(),
                "Control tool call failed: {}",
                value["error"]
            );
            Ok((value["result"].clone(), session))
        })
    }
    fn compaction(&self, event: Value) -> BoxFuture<'_, Result<()>> {
        Box::pin(async move {
            let _ = execution::record_compaction(&self.0, event).map_err(failure)?;
            Ok(())
        })
    }
}
