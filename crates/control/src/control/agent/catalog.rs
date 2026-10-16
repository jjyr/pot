//! Current configured AI connections and model facts for autonomous task selection.
use super::*;
pub fn inspect(s: &Service) -> Result<Value> {
    initialize(s)?;
    let profiles = configured_profiles(s)?;
    let directory = s.control_config.models.options();
    let catalog: Vec<_> = profiles
        .iter()
        .map(|p| {
            let resolved = crate::gateway::resolve(s, p);
            let eligible = resolved.is_ok();
            let unavailable = resolved.as_ref().err().map(|_| "Provider configuration unavailable");
            let configured_models=crate::gateway::setup::saved_models(s,&p.id)?;
            let models = directory.as_array().into_iter().flatten()
                .find(|v| v["id"].as_str() == p.model_settings.as_ref().and_then(|v| v.catalog_provider.as_deref()))
                .map(|v| v["models"].clone()).unwrap_or_else(|| json!([]));
            Ok(json!({"id":p.id,"name":p.name,"provider":p.provider,"model":p.model,"models":models,"configured_models":configured_models,
            "eligible":eligible,"unavailable_reason":unavailable,
            "capabilities":resolved.as_ref().ok().map(|spec|json!({"native_compaction":true,"safe_checkpoint_resume":spec.supports_checkpoint(),"live_steering":true,"model_switch":false,"usage_reporting":spec.supports_checkpoint(),"request_level_budget_enforcement":spec.supports_checkpoint(),"tool_policy_enforcement":true,"mcp_tools":false,"subagents_disableable":true}))}))
        })
        .collect::<Result<_>>()?;
    Ok(json!({"profiles":catalog}))
}

pub fn context(s: &Service) -> Result<String> {
    Ok(format!(
        "\n\n[Available configured AI Providers and models. Choose a Profile ID and optional concrete model autonomously for each task, guided by the user and AGENTS.md. No route confirmation or user allowlist is required. Omission snapshots this assistant’s active model configuration. Unavailable explicit routes fail visibly. Model selection grants no machine/tool authority.]\n{}",
        inspect(s)?
    ))
}
