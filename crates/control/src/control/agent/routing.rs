//! Routing policy is user-owned; engine tool hints cannot change it.
use super::*;
use serde::{Deserialize, Serialize};
use std::collections::BTreeMap;

const POLICY: &str = "task.capacity.v1";
#[derive(Clone, Debug, Default, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Policy {
    #[serde(default)]
    pub revision: u64,
    #[serde(default)]
    pub profile_limits: BTreeMap<String, usize>,
    #[serde(default)]
    pub provider_limits: BTreeMap<String, usize>,
}
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum Source {
    #[default]
    User,
    Schedule,
    Tool,
}
pub fn policy(root: &Service) -> Result<Policy> {
    configuration_owner(root)
        .conversation
        .inner
        .lock()
        .unwrap()
        .store
        .get(POLICY)?
        .map(|s| Ok(serde_json::from_str(&s)?))
        .unwrap_or_else(|| Ok(Policy::default()))
}
pub async fn inspect(State(root): State<Service>, h: HeaderMap) -> ApiResult<Json<Policy>> {
    root.authorized(&h)?;
    Ok(Json(policy(&root).map_err(internal)?))
}
pub async fn configure(
    State(root): State<Service>,
    h: HeaderMap,
    Json(mut p): Json<Policy>,
) -> ApiResult<Json<Policy>> {
    root.authorized(&h)?;
    initialize(&root).map_err(internal)?;
    let root = configuration_owner(&root);
    let inner = root.conversation.inner.lock().unwrap();
    let old: Policy = inner
        .store
        .get(POLICY)
        .map_err(internal)?
        .map(|s| serde_json::from_str(&s))
        .transpose()
        .map_err(|e| internal(e.into()))?
        .unwrap_or_default();
    if p.revision != old.revision {
        return Err(error(
            StatusCode::CONFLICT,
            "routing_revision",
            "Routing policy changed; reload before saving",
        ));
    }
    let profiles = profiles(&inner.store).map_err(internal)?;
    if p.profile_limits
        .keys()
        .any(|id| !profiles.iter().any(|v| &v.id == id))
        || p.provider_limits
            .keys()
            .any(|id| !profiles.iter().any(|v| &v.provider == id))
        || p.profile_limits
            .values()
            .chain(p.provider_limits.values())
            .any(|n| !(1..=1000).contains(n))
    {
        return Err(error(
            StatusCode::BAD_REQUEST,
            "invalid_routing_policy",
            "Use known profiles/providers and limits between 1 and 1000",
        ));
    }
    p.revision = p
        .revision
        .checked_add(1)
        .ok_or_else(|| internal(anyhow!("Routing revision overflow")))?;
    inner
        .store
        .set(
            POLICY,
            &serde_json::to_string(&p).map_err(|e| internal(e.into()))?,
        )
        .map_err(internal)?;
    root.invalidate("settings");
    Ok(Json(p))
}
pub fn resolve(
    root: &Service,
    profile: Option<&str>,
    model: Option<&str>,
    source: Source,
    requirements: &TaskRequirements,
) -> Result<(ResolvedSpec, TaskRouting)> {
    initialize(root)?;
    if profile.is_none() && model.is_none() {
        let spec = thread(root)?.config;
        check_requirements(&spec, requirements)?;
        let routing = TaskRouting {
            profile: spec.source_profile_id.clone(),
            revision: spec.source_profile_revision,
            provider: spec.provider.clone(),
            reason: "chat_default".into(),
            selection_reason: None,
        };
        return Ok((spec, routing));
    }
    let selected = match profile {
        Some(id) => id.to_owned(),
        None => thread(root)?.config.source_profile_id,
    };
    let mut profile = configured_profiles(root)?
        .into_iter()
        .find(|p| p.id == selected)
        .ok_or_else(|| anyhow!("Selected Provider is unavailable"))?;
    if let Some(model) = model {
        if model.trim() != profile.model
            && let Some(settings) = profile.model_settings.as_mut()
        {
            settings.context_window = None;
            settings.max_output_tokens = None;
        }
        profile.model = model.trim().to_owned();
    }
    let spec = crate::gateway::resolve(root, &profile)?;
    check_requirements(&spec, requirements)?;
    let routing = TaskRouting {
        profile: selected,
        revision: profile.revision,
        provider: spec.provider.clone(),
        reason: match source {
            Source::User => "user_requested",
            Source::Schedule => "schedule",
            Source::Tool => "tool_selection",
        }
        .into(),
        selection_reason: None,
    };
    Ok((spec, routing))
}
pub fn check_requirements(spec: &ResolvedSpec, requirements: &TaskRequirements) -> Result<()> {
    // Desktop isolation has not passed D4 acceptance. Shell access is not a declaration of that capability.
    anyhow::ensure!(
        !requirements.desktop,
        "Desktop task capability has not passed acceptance"
    );
    anyhow::ensure!(
        !requirements.exact_budget || spec.supports_checkpoint(),
        "Profile cannot enforce exact request budgets"
    );
    Ok(())
}
impl Policy {
    pub fn admits(&self, candidate: &ResolvedSpec, active: &[ResolvedSpec]) -> bool {
        self.profile_limits
            .get(&candidate.source_profile_id)
            .is_none_or(|cap| {
                active
                    .iter()
                    .filter(|s| s.source_profile_id == candidate.source_profile_id)
                    .count()
                    < *cap
            })
            && self
                .provider_limits
                .get(&candidate.provider)
                .is_none_or(|cap| {
                    active
                        .iter()
                        .filter(|s| s.provider == candidate.provider)
                        .count()
                        < *cap
                })
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn routing_schema_has_no_user_model_selection_policy() {
        assert!(serde_json::from_value::<Policy>(json!({"follow_chat":true})).is_err());
        assert!(serde_json::from_value::<Policy>(json!({"allowed_tool_profiles":[]})).is_err());
    }
}
