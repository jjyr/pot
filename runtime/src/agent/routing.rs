//! Routing policy is user-owned; engine tool hints cannot change it.
use super::*;
use serde::{Deserialize, Serialize};
use std::collections::{BTreeMap, BTreeSet};

const POLICY: &str = "agent.routing.v1";
#[derive(Clone, Debug, Default, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Policy {
    #[serde(default)]
    pub worker_profile: Option<String>,
    #[serde(default)]
    pub revision: u64,
    #[serde(default)]
    pub allowed_tool_profiles: BTreeSet<String>,
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
    root.inner
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
    let inner = root.inner.lock().unwrap();
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
    if p.worker_profile
        .iter()
        .any(|id| !profiles.iter().any(|v| &v.id == id))
    {
        return Err(error(
            StatusCode::BAD_REQUEST,
            "invalid_worker_profile",
            "Use a known worker profile",
        ));
    }
    if p.allowed_tool_profiles
        .iter()
        .chain(p.profile_limits.keys())
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
    Ok(Json(p))
}
pub fn allow_hint(root: &Service, hint: Option<&str>) -> Result<()> {
    if let Some(hint) = hint {
        anyhow::ensure!(
            policy(root)?.allowed_tool_profiles.contains(hint),
            "Profile hint is not allow-listed by the user"
        );
    }
    Ok(())
}
pub fn resolve(
    root: &Service,
    profile: Option<&str>,
    source: Source,
    requirements: &TaskRequirements,
) -> Result<(ResolvedSpec, TaskRouting)> {
    initialize(root)?;
    if source == Source::Tool {
        allow_hint(root, profile)?;
    }
    let (selected, reason) = match profile {
        Some(id) => (
            id.to_owned(),
            match source {
                Source::User => "user_requested",
                Source::Schedule => "schedule",
                Source::Tool => "tool_hint",
            },
        ),
        None => (
            policy(root)?.worker_profile.unwrap_or(
                root.inner
                    .lock()
                    .unwrap()
                    .store
                    .get("agent.default.worker")?
                    .unwrap_or_else(|| "pi-default".into()),
            ),
            "worker_default",
        ),
    };
    let profile = profiles(&root.inner.lock().unwrap().store)?
        .into_iter()
        .find(|p| p.id == selected)
        .ok_or_else(|| anyhow!("Selected worker profile is missing"))?;
    let spec = crate::gateway::resolve(root, &profile)?;
    check_requirements(&spec, requirements)?;
    let routing = TaskRouting {
        profile: selected,
        revision: profile.revision,
        provider: spec.provider.clone(),
        reason: reason.into(),
    };
    Ok((spec, routing))
}
pub fn check_requirements(spec: &ResolvedSpec, requirements: &TaskRequirements) -> Result<()> {
    anyhow::ensure!(
        spec.engine != Engine::Agy,
        "AGY task roles require a verified tool isolation contract; choose Codex or Claude"
    );
    let c = &spec.capabilities;
    anyhow::ensure!(
        !requirements.tools || c.tool_policy_enforcement,
        "Profile cannot enforce task tool policy"
    );
    // Desktop isolation has not passed D4 acceptance. Shell access is not a declaration of that capability.
    anyhow::ensure!(
        !requirements.desktop,
        "Desktop task capability has not passed acceptance"
    );
    anyhow::ensure!(
        !requirements.exact_budget || c.request_level_budget_enforcement,
        "Profile cannot enforce exact request budgets"
    );
    anyhow::ensure!(
        !requirements.live_steering || c.live_steering,
        "Profile does not support live steering"
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
