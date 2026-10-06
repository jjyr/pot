//! Stateless HTTP MCP facade. Lane bearer + turn-bound session identity are
//! checked before the shared dispatcher; MCP IDs reuse Pot receipt semantics.
use crate::*;
use axum::response::Response;

pub fn schemas(summary: bool) -> Vec<Value> {
    let all: Vec<Value> = serde_json::from_str(pot_core::TOOL_SCHEMA).expect("bundled schemas");
    all.into_iter()
        .filter(|tool| {
            !summary
                || matches!(
                    tool["name"].as_str(),
                    Some("pot_list_tasks" | "pot_get_task")
                )
        })
        .collect()
}
fn current_turn(s: &Service) -> ApiResult<String> {
    let inner = s.inner.lock().unwrap();
    let turn = inner
        .turn
        .as_ref()
        .filter(|t| !t.cancelling)
        .ok_or_else(|| error(StatusCode::CONFLICT, "no_turn", "No active turn"))?;
    inner
        .store
        .message(&turn.user_message_id)
        .map_err(internal)?
        .and_then(|m| m.request_id)
        .ok_or_else(|| error(StatusCode::CONFLICT, "no_request", "Turn request missing"))
}
pub async fn mcp(
    State(root): State<Service>,
    h: HeaderMap,
    Json(call): Json<Value>,
) -> ApiResult<Response> {
    let s = tasks::execution_service(&root, &h)?;
    let turn = current_turn(&s)?;
    if call["jsonrpc"] != "2.0" {
        return Err(error(
            StatusCode::BAD_REQUEST,
            "invalid_rpc",
            "Expected JSON-RPC 2.0",
        ));
    }
    let method = call["method"].as_str().unwrap_or("");
    let mut session = None;
    if method != "initialize" {
        let key = h
            .get("mcp-session-id")
            .and_then(|v| v.to_str().ok())
            .filter(|s| uuid::Uuid::parse_str(s).is_ok())
            .ok_or_else(|| {
                error(
                    StatusCode::BAD_REQUEST,
                    "missing_session",
                    "MCP session required",
                )
            })?;
        let identity = s
            .inner
            .lock()
            .unwrap()
            .store
            .get(&format!("mcp.session:{key}"))
            .map_err(internal)?;
        if identity.as_deref() != Some(&turn) {
            return Err(error(
                StatusCode::CONFLICT,
                "stale_session",
                "MCP session belongs to another turn",
            ));
        }
    }
    if method == "notifications/initialized" {
        return Ok(StatusCode::ACCEPTED.into_response());
    }
    let rpc_id = call
        .get("id")
        .filter(|v| v.is_string() || v.is_number())
        .ok_or_else(|| error(StatusCode::BAD_REQUEST, "invalid_id", "Request ID required"))?;
    let response = match method {
        "initialize" => {
            let key = id();
            s.inner.lock().unwrap().store.set(&format!("mcp.session:{key}"),&turn).map_err(internal)?;
            session = Some(key);
            json!({"protocolVersion":"2025-03-26","capabilities":{"tools":{}},"serverInfo":{"name":"pot","version":env!("CARGO_PKG_VERSION")}})
        }
        "ping" => json!({}),
        "tools/list" => json!({"tools":if s.is_worker {vec![]} else {schemas(turn.starts_with(orchestration::RESULT_PREFIX))}}),
        "tools/call" => {
            if s.is_worker { return Err(error(StatusCode::FORBIDDEN,"worker_orchestration","Workers cannot invoke orchestration")); }
            let name = call["params"]["name"].as_str().unwrap_or("");
            if !schemas(turn.starts_with(orchestration::RESULT_PREFIX)).iter().any(|v|v["name"]==name) { return Err(error(StatusCode::FORBIDDEN,"tool_unavailable","Tool is unavailable for this role")); }
            let dispatch: orchestration::Call = serde_json::from_value(json!({"name":name,"call_id":format!("mcp:{rpc_id}"),"args":call["params"].get("arguments").cloned().unwrap_or_else(||json!({}))})).map_err(|e|internal(e.into()))?;
            match orchestration::invoke(State(root),h,Json(dispatch)).await {
                Ok(Json(value)) => json!({"content":[{"type":"text","text":value.to_string()}],"isError":false}),
                Err((status,Json(value))) => json!({"content":[{"type":"text","text":format!("{}: {}",status.as_u16(),value.message)}],"isError":true}),
            }
        }
        _ => return Ok(Json(json!({"jsonrpc":"2.0","id":rpc_id,"error":{"code":-32601,"message":"Method not found"}})).into_response()),
    };
    let mut response = Json(json!({"jsonrpc":"2.0","id":rpc_id,"result":response})).into_response();
    if let Some(key) = session {
        response
            .headers_mut()
            .insert("mcp-session-id", key.parse().unwrap());
    }
    Ok(response)
}

#[cfg(test)]
mod tests {
    use super::*;
    fn lane(summary: bool) -> Service {
        let s = crate::tests::service();
        let request = if summary {
            "pot-result:summary"
        } else {
            "turn"
        };
        s.save(Message {
            id: "message".into(),
            conversation_id: "main".into(),
            role: Role::User,
            text: "test".into(),
            status: MessageStatus::Accepted,
            created_at_ms: now(),
            request_id: Some(request.into()),
        })
        .unwrap();
        s.turn(Some(TurnState {
            user_message_id: "message".into(),
            cancelling: false,
        }));
        s
    }
    fn headers() -> HeaderMap {
        let mut h = HeaderMap::new();
        h.insert("authorization", "Bearer test-token".parse().unwrap());
        h
    }
    async fn init(s: &Service) -> HeaderMap {
        let response = mcp(
            State(s.clone()),
            headers(),
            Json(json!({"jsonrpc":"2.0","id":1,"method":"initialize"})),
        )
        .await
        .unwrap();
        let mut h = headers();
        h.insert(
            "mcp-session-id",
            response.headers()["mcp-session-id"].clone(),
        );
        h
    }
    #[tokio::test]
    async fn mcp_identity_expires_at_turn_boundary_and_summary_cannot_mutate() {
        let s = lane(true);
        let h = init(&s).await;
        let call = json!({"jsonrpc":"2.0","id":"call","method":"tools/call","params":{"name":"pot_create_task","arguments":{"text":"unauthorized"}}});
        assert_eq!(
            mcp(State(s.clone()), h.clone(), Json(call.clone()))
                .await
                .unwrap_err()
                .0,
            StatusCode::FORBIDDEN
        );
        s.turn(None);
        assert_eq!(
            mcp(State(s), h, Json(call)).await.unwrap_err().0,
            StatusCode::CONFLICT
        );
    }
    #[tokio::test]
    async fn mcp_calls_share_durable_dispatcher_receipts_and_detect_conflicts() {
        let s = lane(false);
        let h = init(&s).await;
        let call = json!({"jsonrpc":"2.0","id":"call","method":"tools/call","params":{"name":"pot_list_tasks","arguments":{}}});
        for _ in 0..2 {
            let response = mcp(State(s.clone()), h.clone(), Json(call.clone()))
                .await
                .unwrap();
            let bytes = axum::body::to_bytes(response.into_body(), 65536)
                .await
                .unwrap();
            let result: Value = serde_json::from_slice(&bytes).unwrap();
            assert_eq!(result["result"]["isError"], false);
        }
        let mut conflicting = call;
        conflicting["params"]["arguments"] = json!({"scope":"all"});
        let response = mcp(State(s.clone()), h, Json(conflicting)).await.unwrap();
        let bytes = axum::body::to_bytes(response.into_body(), 65536)
            .await
            .unwrap();
        let result: Value = serde_json::from_slice(&bytes).unwrap();
        assert_eq!(result["result"]["isError"], true);
        assert!(
            result["result"]["content"][0]["text"]
                .as_str()
                .unwrap()
                .contains("409")
        );
    }
}
