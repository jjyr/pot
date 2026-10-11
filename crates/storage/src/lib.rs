//! Backend persistence, for product-owned threads, contexts and usage.
pub mod store;
pub use store::{Store, id, now};
