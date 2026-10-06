//! Original CLI execution adapters. Runtime access is limited to ExecutionFeedback.
pub mod codex;
mod files;
pub mod grok;
pub mod pi;
use anyhow::{Result, anyhow, bail};
pub use files::{executable, private_json};
use futures::future::BoxFuture;
use pot_core as driver;
use pot_core::*;
pub use pot_core::{DriverHost, SessionHandle};
use serde_json::{Value, json};
use std::{
    process::Stdio,
    sync::{Arc, Mutex},
};
fn cancelled(s: &ExecutionContext) -> bool {
    s.feedback.cancelled()
}
