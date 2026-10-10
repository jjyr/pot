//! Native operator execution without a conversation, model or Sandbox Provider.
use super::*;
use futures::future::BoxFuture;
use pot_core::{ExecutionContext, ExecutionFeedback, ExecutionRole, ToolExecutor};
use sha2::{Digest, Sha256};

struct Feedback {
    host: Host,
    cancellation: PathBuf,
}
impl ExecutionFeedback for Feedback {
    fn cancelled(&self) -> bool {
        *self.host.stop.borrow() || self.cancellation.exists()
    }
    fn cancellation(&self) -> BoxFuture<'_, ()> {
        Box::pin(async move {
            while !self.cancelled() {
                tokio::time::sleep(std::time::Duration::from_millis(50)).await;
            }
        })
    }
    fn record_turn(&self, _: &str) -> Result<()> {
        bail!("Host operator has no conversation")
    }
    fn input_streaming(&self, _: &str) -> Result<()> {
        bail!("Host operator has no model input")
    }
}

#[derive(Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
struct Receipt {
    schema_version: u32,
    request: Value,
    boot_id: String,
    result: Option<Value>,
}

fn receipt_path(host: &Host, run: &str, call: &str) -> Result<PathBuf> {
    uuid::Uuid::parse_str(run)?;
    anyhow::ensure!(
        !call.is_empty() && call.len() <= 256,
        "Invalid call identity"
    );
    let digest = Sha256::digest(serde_json::to_vec(&json!([run, call]))?);
    let key: String = digest.iter().map(|byte| format!("{byte:02x}")).collect();
    Ok(host.data.join("host-operations").join(key))
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub(super) struct ReceiptQuery {
    run_id: String,
    call_id: String,
}
pub(super) async fn query(
    State(host): State<Host>,
    headers: HeaderMap,
    Query(query): Query<ReceiptQuery>,
) -> ApiResult<Json<Value>> {
    host.authorize(&headers)?;
    let path = receipt_path(&host, &query.run_id, &query.call_id)
        .map_err(|e| error(StatusCode::BAD_REQUEST, "identity", &e.to_string()))?;
    if !path.exists() {
        return Ok(Json(json!({"state":"not_accepted"})));
    }
    let receipt: Receipt =
        serde_json::from_slice(&std::fs::read(path).map_err(|e| internal(e.into()))?)
            .map_err(|e| internal(e.into()))?;
    if receipt.schema_version != 1 {
        return Err(error(
            StatusCode::CONFLICT,
            "receipt",
            "Unsupported receipt schema",
        ));
    }
    Ok(Json(
        json!({"state": if receipt.result.is_some() { "completed" } else if receipt.boot_id == host.boot { "accepted" } else { "uncertain" }, "boot_id": receipt.boot_id, "result": receipt.result}),
    ))
}

pub(super) async fn execute(host: &Host, request: Operation) -> Result<Value> {
    anyhow::ensure!(host.host_enabled, "Host execution resource is disabled");
    let environment = host
        .host_environment
        .as_ref()
        .ok_or_else(|| anyhow!("Host execution service is disabled"))?;
    // Native Runtime authority was checked by the route. Peer authority must
    // use its own scoped grants before this executor can be shared remotely.
    let original = json!({"run_id":request.run_id,"call_id":request.call_id,
        "environment_ref":request.environment_ref,"name":request.name,"args":request.args});
    let path = receipt_path(host, &request.run_id, &request.call_id)?;
    std::fs::create_dir_all(path.parent().unwrap())?;
    // Persist before effects and serialize concurrent retries. Cancellation is
    // independent of this gate, so it can interrupt a long-running operation.
    let _gate = host.host_operations.lock().await;
    if path.exists() {
        let receipt: Receipt = serde_json::from_slice(&std::fs::read(&path)?)?;
        anyhow::ensure!(
            receipt.schema_version == 1 && receipt.request == original,
            "Operation identity conflict"
        );
        return receipt.result.ok_or_else(|| {
            anyhow!("Operation outcome uncertain; inspect and reconcile before new effects")
        });
    }
    let feedback = Arc::new(Feedback {
        host: host.clone(),
        cancellation: host
            .data
            .join("environment-cancellations")
            .join(&request.run_id),
    });
    anyhow::ensure!(!feedback.cancelled(), "Run has been cancelled");
    pot_core::environment_tools::validate(&request.name, &request.args)?;
    let workspace = host
        .data
        .join("host-runs")
        .join(&request.run_id)
        .join("workspace");
    std::fs::create_dir_all(&workspace)?;
    let policy = pot_core::ShellSettings::default();
    let mut args = request.args;
    pot_tool_executor::tools::normalize(&request.name, &mut args, &workspace, &policy)?;
    let owner = format!("operator:{}", request.run_id);
    let executor = environment.host_executor(&owner, 1);
    let context = ExecutionContext {
        instructions: String::new(),
        skill_resources: Default::default(),
        data: workspace.clone(),
        workspace,
        owner: owner.clone(),
        role: ExecutionRole::Worker,
        backend_url: String::new(),
        lane_token: String::new(),
        main_prompt: String::new(),
        feedback: feedback.clone(),
        tool_executor: None,
    };
    let mut receipt = Receipt {
        schema_version: 1,
        request: original,
        boot_id: host.boot.clone(),
        result: None,
    };
    execution::private_json(&path, &serde_json::to_value(&receipt)?)?;
    let mut emit = |_| Ok(());
    let result = tokio::select! {
        value = executor.execute(&context, &policy, &request.name, &args, &request.call_id, &mut emit) => value,
        _ = feedback.cancellation() => {
            environment.finish_host(&owner).await?;
            Err(anyhow!("Run has been cancelled"))
        },
    };
    let value = match result {
        Ok(result) => json!({"result":result}),
        Err(error) => json!({"error":error.to_string(),"isError":true}),
    };
    receipt.result = Some(value.clone());
    execution::private_json(&path, &serde_json::to_value(&receipt)?)?;
    Ok(value)
}
