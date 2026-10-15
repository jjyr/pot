//! The Pot application UI and portable presentation state. Hosts provide platform services.
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

#[cfg(feature = "presentation")]
pub mod pages;
#[cfg(feature = "presentation")]
pub mod tasks;

#[cfg(feature = "presentation")]
pub mod usage;
pub mod usage_state;

pub mod page_state;
pub mod permission_display;

#[cfg(feature = "presentation")]
pub mod resources;

#[cfg(feature = "presentation")]
pub mod permissions;

#[cfg(feature = "presentation")]
pub mod assistants;

pub mod machine_state;
#[cfg(feature = "presentation")]
pub mod machines;

#[cfg(feature = "presentation")]
pub mod machine_connections;

pub mod onboarding;

#[cfg(feature = "presentation")]
pub mod application;
extern crate self as pot_ui;
