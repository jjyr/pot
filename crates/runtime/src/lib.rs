//! Runtime composition, lifecycle and a shared local Agent execution manager.
use anyhow::{Result, anyhow};
use pot_control::{
    component::Component,
    control::{Service, assistants::Hub},
    host::Host,
    sandbox::Manager,
};
use pot_protocol::RuntimeEndpoint;
use std::{
    path::PathBuf,
    sync::{
        Arc,
        atomic::{AtomicBool, Ordering},
    },
};
#[derive(Clone, Debug)]
pub struct RuntimeConfig {
    pub data: PathBuf,
    pub workspace: PathBuf,
}
pub struct Backend {
    host: Host,
    control: Option<Component>,
    activated: AtomicBool,
}
pub async fn initialize(config: RuntimeConfig) -> Result<Backend> {
    Ok(Backend {
        host: Host::open(&config.data)?,
        control: None,
        activated: AtomicBool::new(false),
    })
}
impl Backend {
    pub fn instance_lock(&self) -> Arc<std::fs::File> {
        self.host.instance_lock()
    }
    pub fn state(&self) -> Result<Service> {
        self.optional_state()
            .ok_or_else(|| anyhow!("Control is not assembled"))
    }
    pub fn optional_state(&self) -> Option<Service> {
        self.control.as_ref().map(Component::state)
    }
    pub fn assistants(&self) -> Option<Arc<Hub>> {
        self.control.as_ref().map(Component::assistants)
    }
    pub fn executions(&self) -> &pot_runtime_execution::Manager {
        self.host.executions()
    }
    pub fn host(&self) -> &Host {
        &self.host
    }
    pub fn data_dir(&self) -> &std::path::Path {
        self.host.data_dir()
    }
    pub fn endpoint(&self, port: u16) -> RuntimeEndpoint {
        self.host.endpoint(port)
    }
    pub fn activate(&self) -> Result<()> {
        anyhow::ensure!(
            !self.activated.swap(true, Ordering::SeqCst),
            "Runtime already activated"
        );
        if let Some(control) = &self.control {
            control.activate()?;
        }
        Ok(())
    }
    pub fn enable_host_resource(&mut self) {
        self.host.enable_execution();
        if let Some(control) = &self.control {
            control.enable_host();
        }
    }
    pub fn sandbox_manager(&self) -> Option<Arc<Manager>> {
        self.host.sandbox_manager()
    }
    pub fn register_sandbox_providers(
        &mut self,
        providers: Vec<Arc<dyn pot_sandbox::Provider>>,
    ) -> Result<Arc<Manager>> {
        self.host.register_providers(providers)
    }
    pub async fn enable_assistant_resources(&mut self, config: RuntimeConfig) -> Result<()> {
        if self.control.is_some() {
            return Ok(());
        }
        anyhow::ensure!(
            config.data == self.host.data_dir(),
            "Control and local Runner must share the Runtime data root"
        );
        let control =
            pot_control::component::open(config.data, config.workspace, vec![], &mut self.host)
                .await?;
        if self.activated.load(Ordering::SeqCst) {
            control.activate()?;
        }
        self.host.advertise_control();
        self.control = Some(control);
        Ok(())
    }
    pub async fn shutdown(&mut self) {
        self.host.request_stop();
        if let Some(control) = &mut self.control {
            control.shutdown().await;
            match tokio::time::timeout(
                std::time::Duration::from_secs(20),
                self.executions().drain(),
            )
            .await
            {
                Ok(Ok(())) => {}
                Ok(Err(error)) => eprintln!("Runtime Agent shutdown unconfirmed: {error}"),
                Err(error) => eprintln!("Runtime Agent shutdown timed out: {error}"),
            }
        } else if let Err(error) = self.host.shutdown().await {
            eprintln!("{error}");
        }
    }
}
impl Drop for Backend {
    fn drop(&mut self) {
        self.host.request_stop();
    }
}
#[cfg(test)]
mod host_tests;
#[cfg(test)]
mod runner_tests;
