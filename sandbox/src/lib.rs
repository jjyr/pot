//! Managed environment state and execution, independent of product orchestration.
mod files;
mod manager;
mod provider;
mod template;
pub use files::private_json;
pub use manager::{AccessGrant, Lease, Manager};
pub use provider::{Capabilities, LINUX_PROVIDER, Provider, ProviderContext, ProviderDescriptor};
pub use template::{ImageFormat, TemplateDescriptor};

#[cfg(test)]
mod tests;
