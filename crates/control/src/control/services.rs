//! Local service state and scoped adapters. No service starts a background loop here.
use crate::*;

/// Per-owner execution metadata. Shared product services live separately.
#[derive(Clone)]
pub struct OwnerScope {
    pub(crate) runtime_id: String,
    pub(crate) assistant_id: String,
    pub(crate) skill_selection: Option<Vec<String>>,
    #[cfg(test)]
    pub(crate) test_tool_executor: Option<Arc<dyn pot_core::ToolExecutor>>,
    pub(crate) remote_context: Arc<
        Mutex<
            std::collections::BTreeMap<
                pot_core::ExecutionTarget,
                crate::remote_execution::RemoteContext,
            >,
        >,
    >,
    pub(crate) sandbox_id: Option<String>,
    pub(crate) execution_target: Option<pot_core::ExecutionTarget>,
    pub(crate) usage: Arc<Mutex<usage::Ledger>>,
    pub(crate) usage_scope: String,
    pub(crate) token: String,
    pub(crate) boot: String,
    pub(crate) workspace: PathBuf,
    pub(crate) api_budget: Option<budget::BudgetSettings>,
    pub(crate) execution: Option<execution::Config>,
    #[cfg(test)]
    pub(crate) agent_upstream: String,
    pub(crate) is_worker: bool,
    pub(crate) data: PathBuf,
    pub(crate) control_config: Arc<crate::model_config::ControlConfig>,
    pub(crate) cancel_notify: Arc<tokio::sync::Notify>,
    pub(crate) stop: tokio::sync::watch::Sender<bool>,
}

#[derive(Clone)]
pub(crate) struct ConversationService {
    pub(crate) inner: Arc<Mutex<Inner>>,
    pub(crate) events: broadcast::Sender<Event>,
    pub(crate) commands: mpsc::UnboundedSender<SendMessage>,
}

#[derive(Clone)]
pub(crate) struct TaskService {
    pub(crate) hub: Arc<Mutex<tasks::Hub>>,
    pub(crate) capacity: Arc<Mutex<tasks::Capacity>>,
}

#[derive(Clone)]
pub(crate) struct AgentService {
    pub(crate) executions: pot_runtime_execution::Manager,
    #[cfg(test)]
    pub(crate) extensions: Option<Arc<dyn pot_agent::AgentExtensions>>,
    pub(crate) resources: Arc<AgentResources>,
}

/// Agent resource paths are independent of Execution target inventory/lifecycle.
pub(crate) struct AgentResources {
    pub(crate) assistants: Mutex<std::sync::Weak<assistants::Hub>>,
    pub(crate) mail_work: Mutex<std::collections::BTreeSet<String>>,
    pub(crate) home: PathBuf,
    pub(crate) skill_user: PathBuf,
    pub(crate) skill_builtin: PathBuf,
    pub(crate) global_instructions: Mutex<instructions::GlobalInstructions>,
}
impl AgentResources {
    pub(crate) fn paths(data: &std::path::Path) -> Self {
        #[cfg(test)]
        let home = data.parent().unwrap_or(data).join("agent-home");
        #[cfg(not(test))]
        let home = std::env::var_os("POT_AGENT_HOME")
            .map(PathBuf::from)
            .unwrap_or_else(|| {
                PathBuf::from(std::env::var_os("HOME").unwrap_or_default()).join(".pot/agent")
            });
        #[cfg(test)]
        let skill_user = data.parent().unwrap_or(data).join("skills-user");
        #[cfg(not(test))]
        let skill_user = std::env::var_os("POT_AGENT_SKILLS_DIR")
            .map(PathBuf::from)
            .unwrap_or_else(|| home.join("skills"));
        #[cfg(not(test))]
        let _ = data;
        let skill_builtin = std::env::var_os("POT_BUILTIN_SKILLS_DIR")
            .map(PathBuf::from)
            .unwrap_or_else(|| PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../../skills"));
        Self {
            home,
            skill_user,
            skill_builtin,
            global_instructions: Mutex::new(Default::default()),
            assistants: Default::default(),
            mail_work: Default::default(),
        }
    }
}

#[derive(Clone)]
pub(crate) struct ExecutionService {
    pub(crate) remote:
        Arc<std::sync::RwLock<Option<Arc<dyn crate::remote_execution::RemoteExecution>>>>,
    pub(crate) host: Arc<execution_target::HostExecution>,
    pub(crate) host_enabled: Arc<std::sync::atomic::AtomicBool>,
    pub(crate) sandboxes: Arc<sandbox::Manager>,
    pub(crate) permissions: Arc<permissions::Hub>,
    pub(crate) terminals: Arc<Mutex<terminal::Hub>>,
}

impl std::ops::Deref for Service {
    type Target = OwnerScope;
    fn deref(&self) -> &Self::Target {
        &self.scope
    }
}
impl std::ops::DerefMut for Service {
    fn deref_mut(&mut self) -> &mut Self::Target {
        &mut self.scope
    }
}
