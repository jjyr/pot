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
    initialize(s)?;
    let mut profiles = configured_profiles(s)?;
    profiles.sort_by(|a, b| a.id.cmp(&b.id));
    let mut rows = vec![];
    for p in profiles {
        let mut models = crate::gateway::setup::saved_models(s, &p.id)?;
        models.sort();
        models.dedup();
        let row = json!({"profile":p.id,"name":p.name,"default_model":p.model,
            "eligible":crate::gateway::resolve(s,&p).is_ok(),
            "models":models.iter().take(5).collect::<Vec<_>>(),"more_models":models.len()>5});
        let mut candidate = rows.clone();
        candidate.push(row.clone());
        if serde_json::to_vec(&candidate)?.len() > 3500 {
            break;
        }
        rows.push(row);
    }
    Ok(format!(
        "Configured Provider/model summary (bounded; may omit choices). Choose autonomously; omission uses your active route. Use get_model_catalog for complete configured choices and model facts before selecting an omitted model. Explicit user choices take precedence; unavailable routes fail visibly.\n{}",
        json!({"profiles":rows})
    ))
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Query {
    profile: Option<String>,
    #[serde(default)]
    offset: usize,
    limit: Option<usize>,
}

pub fn query(s: &Service, args: Value) -> Result<Value> {
    let query: Query = serde_json::from_value(args)?;
    let limit = query.limit.unwrap_or(20).clamp(1, 100);
    let mut catalog = inspect(s)?;
    let profiles = catalog["profiles"].as_array_mut().unwrap();
    profiles.sort_by(|a, b| a["id"].as_str().cmp(&b["id"].as_str()));
    if let Some(profile) = query.profile {
        profiles.retain(|p| p["id"] == profile);
        anyhow::ensure!(!profiles.is_empty(), "Configured Provider not found");
        for p in profiles {
            for key in ["models", "configured_models"] {
                let all = p[key].as_array().cloned().unwrap_or_default();
                p[format!("{key}_total")] = json!(all.len());
                p[key] = json!(
                    all.into_iter()
                        .skip(query.offset)
                        .take(limit)
                        .collect::<Vec<_>>()
                );
            }
        }
    } else {
        let total = profiles.len();
        let page = profiles.iter().skip(query.offset).take(limit)
            .map(|p| json!({"id":p["id"],"name":p["name"],"model":p["model"],"eligible":p["eligible"]})).collect::<Vec<_>>();
        catalog = json!({"profiles":page,"total":total,"next_offset":(query.offset.saturating_add(limit)<total).then_some(query.offset.saturating_add(limit))});
    }
    Ok(catalog)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn routing_summary_is_bounded_and_full_models_are_queryable_by_page() {
        let s = crate::tasks::tests::fixture();
        initialize(&s).unwrap();
        let profiles = configured_profiles(&s).unwrap();
        let id = &profiles[0].id;
        let models: Vec<_> = (0..80).map(|i| format!("model-{i:03}")).collect();
        s.conversation
            .inner
            .lock()
            .unwrap()
            .store
            .set(
                &format!("providers.models:{id}"),
                &json!(models).to_string(),
            )
            .unwrap();
        let summary = context(&s).unwrap();
        assert!(summary.len() < 4096);
        assert!(summary.contains("model-004"));
        assert!(!summary.contains("model-005"));
        let page = query(&s, json!({"profile":id,"offset":5,"limit":3})).unwrap();
        assert_eq!(page["profiles"][0]["configured_models_total"], 80);
        assert_eq!(
            page["profiles"][0]["configured_models"],
            json!(["model-005", "model-006", "model-007"])
        );
        let discovery = query(&s, json!({"limit":1})).unwrap();
        assert_eq!(discovery["profiles"][0]["id"], *id);
        assert!(discovery["profiles"][0].get("models").is_none());
        assert!(query(&s, json!({"profile":"missing"})).is_err());
        assert!(query(&s, json!({"unexpected":true})).is_err());
        std::fs::remove_dir_all(&s.data).unwrap();
    }
}
