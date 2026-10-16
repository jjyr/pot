use axum::{
    Router,
    routing::{get, post},
};
use pot_control::control::{Service, api::*, usage};
use pot_protocol::*;
/// Routes preserve the Control API while centralizing server assembly.
pub fn router(s: Service) -> Router {
    Router::new()
        .route("/api/v1/desktop/sessions", post(pot_control::desktop::open))
        .route(
            "/api/v1/desktop/sessions/close",
            post(pot_control::desktop::close),
        )
        .route("/desktop/{id}/", get(pot_control::desktop::page))
        .route(
            "/desktop/{id}/{name}",
            get(pot_control::desktop::localization_asset),
        )
        .route(
            "/desktop/{id}/api/status",
            get(pot_control::desktop::status),
        )
        .route(
            "/desktop/{id}/api/control",
            post(pot_control::desktop::control),
        )
        .route(
            "/desktop/{id}/api/browser",
            post(pot_control::desktop::browser),
        )
        .route(
            "/desktop/{id}/api/metrics",
            get(pot_control::desktop::metrics),
        )
        .route("/desktop/{id}/rfb", get(pot_control::desktop::connect))
        .route("/desktop-assets/{*path}", get(pot_control::desktop::asset))
        .route(
            "/api/v1/execution_targets",
            get(pot_control::execution_target::list),
        )
        .route(
            "/api/v1/sandboxes",
            get(sandbox::list).post(sandbox::manage),
        )
        .route("/api/v1/sandboxes/shared", post(sandbox::shared_lifecycle))
        .route(
            "/api/v1/sandbox-resources",
            get(pot_control::sandbox_resources::status)
                .post(pot_control::sandbox_resources::command),
        )
        .route(
            "/api/v1/permissions",
            get(pot_control::permissions::inspect).post(pot_control::permissions::decide),
        )
        .route("/api/v1/execution", post(sandbox::execute))
        .route(
            "/api/v1/terminals",
            get(terminal::list).post(terminal::create),
        )
        .route("/api/v1/terminals/{id}/socket", get(terminal::connect))
        .route("/api/v1/terminals/{id}/close", post(terminal::close))
        .route(
            "/api/v1/providers",
            get(gateway::setup::get).post(gateway::setup::save),
        )
        .route("/api/v1/providers/check", post(gateway::setup::check))
        .route(
            "/api/v1/providers/default",
            post(gateway::setup::set_default),
        )
        .route(
            "/gw/{provider}/{*path}",
            post(gateway::routed_protocol).layer(axum::extract::DefaultBodyLimit::max(
                execution::MAX_MODEL_BODY,
            )),
        )
        .route("/api/agent/compactions", post(execution::compaction_event))
        .route("/api/v1/orchestration", post(orchestration::invoke))
        .route("/api/v1/mcp", post(tool_bridge::mcp))
        .route(PATH_TASKS, get(tasks::list).post(tasks::create))
        .route("/api/v1/tasks/query", get(tasks::query))
        .route("/api/v1/tasks/{id}/files", get(pot_control::files::chunk))
        .route(
            "/api/v1/tasks/{id}/file-download",
            get(pot_control::files::download),
        )
        .route(
            "/api/v1/schedules",
            get(schedules::list).post(schedules::command),
        )
        .route("/api/v1/tasks/{id}", get(tasks::snapshot))
        .route("/api/v1/tasks/{id}/events", get(tasks::events))
        .route("/api/v1/tasks/{id}/messages", post(tasks::input))
        .route("/api/v1/tasks/{id}/interrupt", post(tasks::interrupt))
        .route("/api/v1/tasks/{id}/cancel", post(tasks::cancel))
        .route(
            "/api/v1/agent",
            get(agent::inspect).post(agent::settings::settings),
        )
        .route("/api/v1/agent/config", post(agent::settings::select))
        .route(
            "/api/v1/agent/routing",
            get(agent::routing::inspect).post(agent::routing::configure),
        )
        .route(PATH_SNAPSHOT, get(snapshot))
        .route("/api/v1/usage", get(usage))
        .route("/api/v1/usage/summary", get(usage::summary))
        .route("/api/v1/usage/policy", post(usage::policy))
        .route(
            "/api/v1/usage/providers",
            get(usage::providers::inspect).post(usage::providers::configure),
        )
        .route("/api/v1/usage/resume", post(usage::resume))
        .route("/api/v1/usage/cancel", post(usage::cancel_wait))
        .route(PATH_EVENTS, get(events_handler))
        .route(
            "/api/v1/completions",
            get(pot_control::control::completions),
        )
        .route(PATH_MESSAGES, post(send))
        .route(PATH_CANCEL, post(cancel))
        .route("/api/v1/turn/steer", post(control::steer))
        .route("/api/v1/tasks/{id}/steer", post(control::steer_task))
        .route("/api/v1/turn/control", post(control::control))
        .route("/api/v1/tasks/{id}/control", post(control::control_task))
        .route("/api/v1/agent/diagnostics", get(control::diagnostics))
        .with_state(s)
}

/// Owner clients explicitly select a conversational owner; private integration tokens
/// resolve to their originating assistant without granting access to another owner.
pub fn assistant_routes(hub: std::sync::Arc<pot_control::control::assistants::Hub>) -> Router {
    Router::new()
        .route("/api/v1/assistant-tasks", get(assistant_tasks))
        .route(pot_protocol::message::API, post(assistant_mail))
        .route(
            "/api/v1/assistant-notifications",
            post(assistant_notifications),
        )
        .route(
            "/api/v1/assistants",
            get(assistant_list).post(assistant_create),
        )
        .route(
            "/api/v1/assistants/{owner}/definition",
            post(assistant_rename),
        )
        .route(
            "/api/v1/assistants/{owner}/instructions",
            get(assistant_rules).post(assistant_save_rules),
        )
        .route(
            "/api/v1/assistants/{owner}/{*path}",
            axum::routing::any(assistant_dispatch),
        )
        .with_state(hub)
}
async fn assistant_notifications(
    axum::extract::State(hub): AssistantState,
    headers: axum::http::HeaderMap,
    axum::Json(args): axum::Json<serde_json::Value>,
) -> axum::response::Response {
    use axum::response::IntoResponse;
    if !hub.authorize(&headers) {
        return rejected(
            axum::http::StatusCode::UNAUTHORIZED,
            "Owner authority required",
        );
    }
    let after = if args["initialize"] == true {
        None
    } else {
        match serde_json::from_value::<std::collections::BTreeMap<String, i64>>(
            args["after"].clone(),
        ) {
            Ok(after) if after.len() <= 100 && after.values().all(|v| *v >= 0) => Some(after),
            _ => {
                return rejected(
                    axum::http::StatusCode::BAD_REQUEST,
                    "Invalid notification cursors",
                );
            }
        }
    };
    match hub.notifications(after.as_ref()) {
        Ok(feed) => axum::Json(feed).into_response(),
        Err(error) => rejected(
            axum::http::StatusCode::INTERNAL_SERVER_ERROR,
            &error.to_string(),
        ),
    }
}
type AssistantState = axum::extract::State<std::sync::Arc<pot_control::control::assistants::Hub>>;
fn rejected(status: axum::http::StatusCode, message: &str) -> axum::response::Response {
    use axum::response::IntoResponse;
    (
        status,
        axum::Json(serde_json::json!({"code":"assistant","message":message})),
    )
        .into_response()
}
async fn assistant_list(
    axum::extract::State(hub): AssistantState,
    headers: axum::http::HeaderMap,
) -> axum::response::Response {
    use axum::response::IntoResponse;
    if !hub.authorize(&headers) {
        return rejected(
            axum::http::StatusCode::UNAUTHORIZED,
            "Owner authority required",
        );
    }
    match hub.catalogue() {
        Ok(catalogue) => axum::Json(catalogue).into_response(),
        Err(error) => rejected(
            axum::http::StatusCode::INTERNAL_SERVER_ERROR,
            &error.to_string(),
        ),
    }
}
async fn assistant_create(
    axum::extract::State(hub): AssistantState,
    headers: axum::http::HeaderMap,
    axum::Json(args): axum::Json<serde_json::Value>,
) -> axum::response::Response {
    use axum::response::IntoResponse;
    if !hub.authorize(&headers) {
        return rejected(
            axum::http::StatusCode::UNAUTHORIZED,
            "Owner authority required",
        );
    }
    let configuration = match serde_json::from_value(args["configuration"].clone()) {
        Ok(configuration) => configuration,
        Err(e) => return rejected(axum::http::StatusCode::BAD_REQUEST, &e.to_string()),
    };
    match hub.create_configured(
        args["request_id"].as_str().unwrap_or(""),
        args["name"].as_str().unwrap_or("Pot"),
        &configuration,
        args["text"].as_str().unwrap_or(""),
    ) {
        Ok(d) => axum::Json(d).into_response(),
        Err(e) => rejected(axum::http::StatusCode::CONFLICT, &e.to_string()),
    }
}

async fn assistant_rename(
    axum::extract::State(hub): AssistantState,
    axum::extract::Path(owner): axum::extract::Path<String>,
    headers: axum::http::HeaderMap,
    axum::Json(args): axum::Json<serde_json::Value>,
) -> axum::response::Response {
    use axum::response::IntoResponse;
    if !hub.authorize(&headers) {
        return rejected(
            axum::http::StatusCode::UNAUTHORIZED,
            "Owner authority required",
        );
    }
    if args.get("configuration").is_some() {
        return match hub.edit_configured(&owner, &args) {
            Ok(v) => axum::Json(v).into_response(),
            Err(e) => rejected(axum::http::StatusCode::CONFLICT, &e.to_string()),
        };
    }
    match hub.rename(
        &owner,
        args["name"].as_str().unwrap_or(""),
        args["expected_revision"].as_u64().unwrap_or(0),
    ) {
        Ok(d) => axum::Json(d).into_response(),
        Err(e) => rejected(axum::http::StatusCode::CONFLICT, &e.to_string()),
    }
}
async fn assistant_dispatch(
    axum::extract::State(hub): AssistantState,
    axum::extract::Path((owner, path)): axum::extract::Path<(String, String)>,
    mut request: axum::extract::Request,
) -> axum::response::Response {
    use tower::ServiceExt;
    if !hub.authorize(request.headers()) {
        return rejected(
            axum::http::StatusCode::UNAUTHORIZED,
            "Owner authority required",
        );
    }
    let service = match hub.service(&owner) {
        Ok(s) => s,
        Err(e) => return rejected(axum::http::StatusCode::NOT_FOUND, &e.to_string()),
    };
    let query = request
        .uri()
        .query()
        .map(|q| format!("?{q}"))
        .unwrap_or_default();
    *request.uri_mut() = format!("/api/v1/{path}{query}").parse().unwrap();
    hub.operator_headers(&service, request.headers_mut());
    // A fresh router must extract only its own path parameters. Preserve the
    // underlying HTTP upgrade for scoped terminal/desktop WebSockets.
    let upgrade = request
        .extensions_mut()
        .remove::<hyper::upgrade::OnUpgrade>();
    request.extensions_mut().clear();
    if let Some(upgrade) = upgrade {
        request.extensions_mut().insert(upgrade);
    }
    router(service).oneshot(request).await.unwrap()
}

async fn assistant_rules(
    axum::extract::State(hub): AssistantState,
    axum::extract::Path(owner): axum::extract::Path<String>,
    headers: axum::http::HeaderMap,
) -> axum::response::Response {
    use axum::response::IntoResponse;
    if !hub.authorize(&headers) {
        return rejected(
            axum::http::StatusCode::UNAUTHORIZED,
            "Owner authority required",
        );
    }
    match hub.rules(&owner) {
        Ok(v) => axum::Json(v).into_response(),
        Err(e) => rejected(axum::http::StatusCode::CONFLICT, &e.to_string()),
    }
}
async fn assistant_save_rules(
    axum::extract::State(hub): AssistantState,
    axum::extract::Path(owner): axum::extract::Path<String>,
    headers: axum::http::HeaderMap,
    axum::Json(args): axum::Json<serde_json::Value>,
) -> axum::response::Response {
    use axum::response::IntoResponse;
    if !hub.authorize(&headers) {
        return rejected(
            axum::http::StatusCode::UNAUTHORIZED,
            "Owner authority required",
        );
    }
    let Some(text) = args["text"].as_str() else {
        return rejected(axum::http::StatusCode::BAD_REQUEST, "Text is required");
    };
    match hub.save_rules(
        &owner,
        text,
        args["expected_revision"].as_str().unwrap_or(""),
    ) {
        Ok(v) => axum::Json(v).into_response(),
        Err(e) => rejected(axum::http::StatusCode::CONFLICT, &e.to_string()),
    }
}

async fn assistant_tasks(
    axum::extract::State(hub): AssistantState,
    headers: axum::http::HeaderMap,
    axum::extract::Query(query): axum::extract::Query<TaskQuery>,
) -> axum::response::Response {
    use axum::response::IntoResponse;
    if !hub.authorize(&headers) {
        return rejected(
            axum::http::StatusCode::UNAUTHORIZED,
            "Owner authority required",
        );
    }
    match hub.tasks_page(&query) {
        Ok(v) => axum::Json(v).into_response(),
        Err(e) => rejected(axum::http::StatusCode::BAD_REQUEST, &e.to_string()),
    }
}

async fn assistant_mail(
    axum::extract::State(hub): AssistantState,
    headers: axum::http::HeaderMap,
    axum::Json(request): axum::Json<pot_protocol::message::RelayRequest>,
) -> axum::response::Response {
    use axum::response::IntoResponse;
    if !hub.authorize(&headers) {
        return rejected(
            axum::http::StatusCode::UNAUTHORIZED,
            "Owner authority required",
        );
    }
    match hub.relay(request) {
        Ok(value) => axum::Json(value).into_response(),
        Err(error) => rejected(axum::http::StatusCode::CONFLICT, &error.to_string()),
    }
}
