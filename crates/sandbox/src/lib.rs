//! Managed Sandbox state and execution, independent of product orchestration.
mod desktop;
mod files;
pub use desktop::{
    DesktopCapabilities, DesktopIo, DesktopProtocol, DesktopSession, DesktopStream, RfbGate,
};
mod manager;
mod mappings;
pub use mappings::{HostMapping, host_mappings};
mod provider;
pub use files::private_json;
pub use manager::{AccessGrant, Lease, Manager};
pub use provider::{
    Capabilities, LINUX_PODMAN_PROVIDER, MACOS_APPLE_CONTAINER_PROVIDER, PreparedResources,
    Provider, ProviderContext, ProviderDescriptor, ScopeCleanupPolicy,
};

#[cfg(test)]
mod tests;
