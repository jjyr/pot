//! GPUI browser client. The authenticated transport is a same-origin JS adapter.
#[cfg(target_family = "wasm")]
mod web;

#[cfg(target_family = "wasm")]
use pot_ui::design;
