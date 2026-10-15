//! GPUI browser client. The authenticated transport is a same-origin JS adapter.
#[cfg(target_family = "wasm")]
mod crash;
#[cfg(target_family = "wasm")]
mod web;
