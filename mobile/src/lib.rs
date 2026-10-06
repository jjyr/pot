//! GPUI browser client. The authenticated transport is a same-origin JS adapter.
#[cfg(target_family = "wasm")]
mod web;

#[cfg(target_family = "wasm")]
mod gpui {
    pub use gpui_kit::*;
}
#[cfg(target_family = "wasm")]
#[path = "../../app/src/design.rs"]
#[allow(dead_code)]
mod design;
