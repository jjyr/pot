//! Authenticated outward services for destination-local Agents. Effect replies are
//! read from durable receipts; an ambiguous send is never submitted twice.
use super::*;
use futures_util::StreamExt;
use pot_runtime::{remote_environment::RemoteBinding, task_agent::control::Operation};

const RESPONSE_LIMIT: usize = 16 * 1024 * 1024;
#[derive(Clone, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub(super) struct Request {
    caller: String,
    recipient: String,
    issued_at_ms: i64,
    accept_before_ms: i64,
    nonce: String,
    binding: RemoteBinding,
    lease_id: String,
    id: String,
    operation: Option<Operation>,
    query: bool,
}

pub(super) async fn service(
    State(resources): State<Arc<Resources>>,
    headers: HeaderMap,
    Json(signed): Json<Signed<Request>>,
) -> Response {
    let result = handle(resources.clone(), &headers, &signed).await;
    match result {
        Ok(value) => match resources
            .control
            .sign(json!({"digest":protocol_digest(&signed).unwrap_or_default(),"value":value}))
        {
            Ok(reply) => Json(reply).into_response(),
            Err(_) => StatusCode::INTERNAL_SERVER_ERROR.into_response(),
        },
        Err(error) => (
            StatusCode::BAD_REQUEST,
            Json(json!({"error":error.to_string()})),
        )
            .into_response(),
    }
}
async fn handle(
    resources: Arc<Resources>,
    headers: &HeaderMap,
    signed: &Signed<Request>,
) -> Result<Value> {
    let authority = &resources.control;
    let request = &signed.payload;
    ensure!(
        request.recipient == authority.identity.runtime_id
            && request.issued_at_ms.abs_diff(now()) <= 30_000,
        "Service recipient or deadline rejected"
    );
    let token = headers
        .get("authorization")
        .and_then(|h| h.to_str().ok())
        .and_then(|h| h.strip_prefix("Bearer "))
        .unwrap_or("");
    {
        let book = authority.book.lock().unwrap();
        let member = book
            .members
            .get(&request.caller)
            .filter(|m| m.enabled)
            .ok_or_else(|| anyhow::anyhow!("Unknown execution member"))?;
        ensure!(
            token.len() == member.token.len()
                && openssl::memcmp::eq(token.as_bytes(), member.token.as_bytes()),
            "Member credential rejected"
        );
        member.identity.verify(request, &signed.signature)?;
    }
    let run = authority.client_run(&request.binding)?;
    ensure!(
        request.caller == request.binding.environment.runtime_id
            && run.lease_id.as_ref() == Some(&request.lease_id)
            && run.task_launch.is_some()
            && (request.operation.is_none() || !run.cancelled),
        "Service assignment mismatch"
    );
    let hub = resources
        .assistant_hub()
        .await
        .ok_or_else(|| anyhow::anyhow!("Task owner unavailable"))?;
    let worker = hub.task_service(&request.binding.owner)?;
    worker.validate_task_agent_binding(&request.binding)?;
    ensure!(
        !request.id.is_empty() && request.id.len() <= 256,
        "Invalid service operation identity"
    );
    let path = authority.data.join("task-service-receipts").join(digest(&(
        &request.caller,
        &request.binding,
        &request.id,
    ))?);
    let host = resources.host().await;
    let boot = host.describe()["boot_id"].as_str().unwrap().to_owned();
    if request.query {
        ensure!(
            matches!(request.operation, Some(Operation::Ready)),
            "Only readiness is a query"
        );
        return worker
            .task_agent_operation(&request.binding, Operation::Ready)
            .await;
    }
    let _guard = authority.operations.lock().await;
    if path.exists() {
        let receipt: Receipt = serde_json::from_slice(&std::fs::read(&path)?)?;
        if let Some(operation) = &request.operation {
            ensure!(
                receipt.request == intent_value(&request.binding, operation)?,
                "Service operation conflicts with accepted input"
            );
        }
        return Ok(authority.operation_result(&receipt, &path, &boot));
    }
    let Some(operation) = &request.operation else {
        return Ok(json!({"state":"not_accepted"}));
    };
    ensure!(!run.cancel_requested, "Task cancellation requested");
    ensure!(
        request.accept_before_ms > now()
            && request.accept_before_ms - request.issued_at_ms <= LEASE_MS,
        "Execution lease expired before service acceptance"
    );
    std::fs::create_dir_all(path.parent().unwrap())?;
    let receipt = Receipt {
        schema_version: VERSION,
        request: intent_value(&request.binding, operation)?,
        boot_id: boot.clone(),
        result: None,
    };
    private_json(&path, &receipt)?;
    authority.active.lock().unwrap().insert(path.clone());
    let active = ActiveOperation {
        authority: authority.clone(),
        path: path.clone(),
    };
    let answer = authority.operation_result(&receipt, &path, &boot);
    let operation = operation.clone();
    let binding = request.binding.clone();
    authority.track(tokio::spawn(async move {
        let _active = active;
        let outcome: Result<Value> = async {
            match operation {
                Operation::Model { request } => {
                    let response = worker.task_agent_model(&binding, request).await?;
                    let status = response.status().as_u16();
                    let mut stream = response.into_body().into_data_stream();
                    let mut bytes = Vec::new();
                    while let Some(chunk) = stream.next().await {
                        let chunk = chunk?;
                        ensure!(
                            bytes.len() + chunk.len() <= RESPONSE_LIMIT,
                            "Model response exceeds limit; original request is not replayed"
                        );
                        bytes.extend_from_slice(&chunk);
                    }
                    Ok(json!({"status":status,"body":B64.encode(bytes)}))
                }
                operation => worker.task_agent_operation(&binding, operation).await,
            }
        }
        .await;
        let mut receipt = receipt;
        receipt.result = Some(match outcome {
            Ok(value) => json!({"ok":true,"value":value}),
            Err(error) => json!({"ok":false,"error":error.to_string()}),
        });
        if let Err(error) = private_json(&path, &receipt) {
            eprintln!("Task service receipt remains uncertain: {error}");
        }
    }));
    Ok(answer)
}

// Persist identity and payload digest, not a duplicate of the live model context.
fn intent_value(binding: &RemoteBinding, operation: &Operation) -> Result<Value> {
    Ok(json!({"binding":binding,"operation":digest(operation)?}))
}
pub(super) fn receipt_files(directory: &Path) -> Result<Vec<PathBuf>> {
    let mut paths = Vec::new();
    for entry in std::fs::read_dir(directory)? {
        let entry = entry?;
        if entry.file_type()?.is_file() && entry.path().extension().is_none() {
            paths.push(entry.path());
        }
    }
    Ok(paths)
}
mod port;
pub(super) use port::Port;
