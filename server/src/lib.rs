//! Standalone backend hosting; no frontend, Helper or platform UI dependencies.
pub mod access;
mod config;
mod hosting;
mod routes;
pub use config::{ServerConfig, default_root};
pub use hosting::Server;
pub use routes::router;

mod local;
pub use local::{LocalClient, LocalReply};

mod settings;
pub use settings::apply_settings;

pub use pot_runtime::execution::private_json as save_private_json;
