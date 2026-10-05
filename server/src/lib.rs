//! Standalone backend hosting; no frontend, Helper or platform UI dependencies.
mod config;
mod hosting;
mod routes;
pub use config::ServerConfig;
pub use hosting::Server;
pub use routes::router;
