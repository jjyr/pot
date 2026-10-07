//! Optional owner-instance browser access. Root bearer never crosses this boundary.
mod desktop;
pub(crate) mod push;
mod routes;
mod state;
pub use routes::{admin_router, public_router};
pub use state::{Access, AccessConfig};
