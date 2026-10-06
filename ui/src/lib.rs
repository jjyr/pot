//! Shared Pot presentation and chat state. Platform shells own I/O and navigation.
#[cfg(feature = "presentation")]
extern crate gpui_kit as gpui;
#[cfg(feature = "presentation")]
pub mod chat;
#[cfg(feature = "presentation")]
pub mod design;
pub mod localization;
#[cfg(feature = "presentation")]
pub mod markdown;
pub mod state;
