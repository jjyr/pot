//! Standalone backend hosting; no frontend, Helper or platform UI dependencies.
pub mod access;
mod config;
mod control;
mod hosting;
mod resources;
mod routes;
mod web_control;
pub use config::{ServerConfig, WebControlConfig, default_root};
pub use hosting::Server;
pub use routes::router;

mod client;
pub use client::{ClientTransport, RemoteClient};

mod local;
pub use local::{LocalClient, LocalReply};

mod settings;
mod upgrade;
pub use settings::apply_settings;
