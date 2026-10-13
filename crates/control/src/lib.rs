//! Reusable backend application services. The executable and route assembly live in pot-server.
pub mod control_resources;
pub mod desktop;
pub mod execution;
pub mod execution_target;
pub mod files;
pub mod host;
mod instructions;
pub mod permissions;
pub mod remote_execution;
pub mod sandbox;
pub mod sandbox_resources;
mod skills;
mod store;
pub mod terminal;
pub mod tool_bridge;
use anyhow::{Result, anyhow, bail};
use axum::{
    Json,
    extract::{Query, State},
    http::{HeaderMap, StatusCode},
    response::{
        IntoResponse,
        sse::{Event as SseEvent, KeepAlive, Sse},
    },
};
use pot_protocol::*;
pub mod component;

#[cfg(test)]
use axum::{
    Router,
    routing::{get, post},
};
use serde::Deserialize;
use serde_json::{Value, json};
use std::{
    convert::Infallible,
    path::PathBuf,
    sync::{Arc, Mutex},
};
use store::{Store, id, now};
use tokio::sync::{broadcast, mpsc};

pub mod control;
pub(crate) use control::services::{
    AgentResources, AgentService, ConversationService, ExecutionService, TaskService,
};
pub(crate) use control::*;
pub(crate) use control::{assistant_instructions, budget, local_services, model_config};
