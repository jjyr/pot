use axum::{
    Router,
    routing::{get, post},
};
use pot_protocol::*;
use pot_runtime::{Service, api::*, usage};
/// Routes preserve the Control API while centralizing server assembly.
pub fn router(s: Service) -> Router {
    Router::new()
        .route("/api/v1/authorization/start", post(authorization::start))
        .route("/api/v1/authorization/status", get(authorization::status))
        .route("/api/v1/authorization/cancel", post(authorization::cancel))
        .route(
            "/api/v1/authorization/callback",
            post(authorization::callback),
        )
        .route("/api/v1/agents/tools", get(discovery::list))
        .route("/api/v1/agents/refresh", post(discovery::refresh))
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
        .route(
            "/api/v1/schedules",
            get(schedules::list).post(schedules::command),
        )
        .route("/api/v1/tasks/{id}", get(tasks::snapshot))
        .route("/api/v1/tasks/{id}/messages", post(tasks::input))
        .route("/api/v1/tasks/{id}/cancel", post(tasks::cancel))
        .route(
            "/api/v1/agent",
            get(agent::inspect).post(agent::migration::settings),
        )
        .route("/api/v1/agent/migrate", post(agent::migration::migrate))
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
        .route(PATH_MESSAGES, post(send))
        .route(PATH_CANCEL, post(cancel))
        .route("/api/v1/turn/steer", post(control::steer))
        .route("/api/v1/tasks/{id}/steer", post(control::steer_task))
        .route("/api/v1/turn/control", post(control::control))
        .route("/api/v1/tasks/{id}/control", post(control::control_task))
        .route("/api/v1/agent/diagnostics", get(control::diagnostics))
        .route(PATH_AUTHENTICATE, post(authenticate))
        .with_state(s)
}
