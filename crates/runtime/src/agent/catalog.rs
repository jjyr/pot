//! Runtime-provided, authorization-filtered execution targets. Rules live in AGENTS.md.
use super::*;
pub fn inspect(s: &Service) -> Result<Value> {
    initialize(s)?;
    let policy = routing::policy(s)?;
    let profiles = profiles(&s.conversation.inner.lock().unwrap().store)?;
    let catalog: Vec<_> = profiles
        .iter()
        .map(|p| {
            let resolved = crate::gateway::resolve(s, p);
            let eligible = policy.allowed_tool_profiles.contains(&p.id) && resolved.is_ok();
            let unavailable = if !policy.allowed_tool_profiles.contains(&p.id) {
                Some("Not authorized for main-agent task selection")
            } else if resolved.is_err() {
                Some("Connection configuration unavailable; open AI Configuration")
            } else {
                None
            };
            json!({"id":p.id,"name":p.name,"provider":p.provider,"model":p.model,
            "eligible":eligible,"unavailable_reason":unavailable,
            "capabilities":resolved.as_ref().ok().map(|spec|json!({"native_compaction":true,"safe_checkpoint_resume":spec.supports_checkpoint(),"live_steering":true,"model_switch":false,"usage_reporting":spec.supports_checkpoint(),"request_level_budget_enforcement":spec.supports_checkpoint(),"tool_policy_enforcement":true,"mcp_tools":false,"subagents_disableable":true}))})
        })
        .collect();
    Ok(json!({"profiles":catalog}))
}

pub fn context(s: &Service) -> Result<String> {
    Ok(format!(
        "\n\n[Current configured execution targets. Use eligible Profile IDs; unavailable choices require clarification or recovery, never silent substitution. AGENTS.md rules do not authorize additional targets.]\n{}",
        inspect(s)?
    ))
}
