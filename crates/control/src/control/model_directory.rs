//! Public model facts, cached once per Control. Request paths never fetch upstream.
use anyhow::{Context, Result, ensure};
use futures::{StreamExt, stream};
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use std::{
    collections::BTreeMap,
    path::PathBuf,
    sync::{Arc, RwLock},
    time::Duration,
};

const PERIOD_MS: i64 = 24 * 60 * 60 * 1000;
const MAX_BYTES: usize = 16 * 1024 * 1024;
const SOURCE: &str = "https://models.dev/api.json?type=all";
pub(crate) const COMMON: &[&str] = &[
    "openai",
    "anthropic",
    "google",
    "deepseek",
    "openrouter",
    "opencode",
    "opencode-go",
    "groq",
];
#[derive(Clone, Default, Serialize, Deserialize)]
pub(crate) struct Limits {
    #[serde(default)]
    pub context: u64,
    #[serde(default)]
    pub input: u64,
    #[serde(default)]
    pub output: u64,
}
#[derive(Clone, Default, Serialize, Deserialize)]
struct Route {
    npm: Option<String>,
    api: Option<String>,
}
#[derive(Clone, Default, Serialize, Deserialize)]
struct Modalities {
    #[serde(default)]
    input: Vec<String>,
    #[serde(default)]
    output: Vec<String>,
}
#[derive(Clone, Serialize, Deserialize)]
struct Model {
    name: String,
    limit: Limits,
    #[serde(default)]
    modalities: Modalities,
    #[serde(default)]
    tool_call: bool,
    #[serde(default)]
    provider: Route,
    status: Option<String>,
    #[serde(rename = "type")]
    kind: Option<String>,
}
#[derive(Clone, Serialize, Deserialize)]
struct Provider {
    name: String,
    npm: String,
    api: Option<String>,
    doc: Option<String>,
    models: BTreeMap<String, Model>,
}
type Providers = BTreeMap<String, Provider>;
#[derive(Clone, Serialize, Deserialize)]
struct Snapshot {
    providers: Providers,
    #[serde(default)]
    logos: BTreeMap<String, String>,
    etag: Option<String>,
}
#[derive(Default, Serialize, Deserialize)]
struct Check {
    attempted_at: i64,
}
pub(crate) struct Directory {
    root: PathBuf,
    snapshot: RwLock<Snapshot>,
}
fn valid_id(id: &str) -> bool {
    !id.is_empty()
        && id.len() <= 128
        && id
            .bytes()
            .all(|b| b.is_ascii_alphanumeric() || b"-_.".contains(&b))
}
fn parse(bytes: &[u8]) -> Result<Providers> {
    ensure!(bytes.len() <= MAX_BYTES, "Model directory too large");
    let providers: Providers = serde_json::from_slice(bytes)?;
    ensure!(
        !providers.is_empty() && providers.len() <= 2000,
        "Invalid provider count"
    );
    for (id, p) in &providers {
        ensure!(
            valid_id(id) && !p.name.is_empty() && p.name.len() <= 256 && p.models.len() <= 10000,
            "Invalid provider metadata"
        );
        for (id, m) in &p.models {
            ensure!(
                !id.is_empty() && id.len() <= 256 && m.name.len() <= 512,
                "Invalid model metadata"
            );
            ensure!(
                [m.limit.context, m.limit.input, m.limit.output]
                    .iter()
                    .all(|n| *n <= 9_007_199_254_740_991),
                "Invalid model limits"
            );
        }
    }
    Ok(providers)
}
fn route(id: &str, p: &Provider, m: Option<&Model>) -> Option<(String, &'static str)> {
    let npm = m.and_then(|m| m.provider.npm.as_deref()).unwrap_or(&p.npm);
    let protocol = match npm {
        "@ai-sdk/openai-compatible"
        | "@openrouter/ai-sdk-provider"
        | "@ai-sdk/groq"
        | "@ai-sdk/cerebras"
        | "@ai-sdk/deepinfra" => "openai_chat",
        "@ai-sdk/openai" => "openai_responses",
        "@ai-sdk/anthropic" => "anthropic_messages",
        "@ai-sdk/google" => "gemini",
        _ => return None,
    };
    let base = m
        .and_then(|m| m.provider.api.as_deref())
        .or(p.api.as_deref())
        .or(match id {
            "openai" => Some("https://api.openai.com/v1"),
            "anthropic" => Some("https://api.anthropic.com/v1"),
            "google" => Some("https://generativelanguage.googleapis.com/v1beta"),
            "groq" => Some("https://api.groq.com/openai/v1"),
            "cerebras" => Some("https://api.cerebras.ai/v1"),
            "deepinfra" => Some("https://api.deepinfra.com/v1/openai"),
            _ => None,
        })?;
    let url = reqwest::Url::parse(base).ok()?;
    if url.scheme() != "https"
        || url.host_str().is_none()
        || !url.username().is_empty()
        || url.password().is_some()
        || url.query().is_some()
        || url.fragment().is_some()
    {
        return None;
    }
    let base = base.trim_end_matches('/');
    let base = if url.path().is_empty() || url.path() == "/" {
        format!("{base}/v1")
    } else {
        base.to_owned()
    };
    let suffix = match protocol {
        "openai_responses" => "/responses",
        "anthropic_messages" => "/messages",
        "gemini" => "/models/{model}:streamGenerateContent",
        _ => "/chat/completions",
    };
    let endpoint = if base.ends_with(suffix) {
        base
    } else {
        format!("{base}{suffix}")
    };
    Some((endpoint, protocol))
}
fn usable(m: &Model) -> bool {
    m.kind.is_none()
        && m.status.as_deref() != Some("deprecated")
        && m.tool_call
        && m.modalities.input.iter().any(|s| s == "text")
        && m.modalities.output.iter().any(|s| s == "text")
}
// The reduced upstream catalog has no model-owner field. Recognize official
// model families without rewriting the exact model ID used for lookup.
fn official_providers(model: &str) -> &'static [&'static str] {
    const FAMILIES: &[(&[&str], &[&str])] = &[
        (&["gpt-", "chatgpt-", "o1", "o3", "o4"], &["openai"]),
        (&["claude-"], &["anthropic"]),
        (&["gemini-", "gemma-"], &["google"]),
        (&["deepseek-"], &["deepseek"]),
        (&["qwen", "qwq-", "qvq-"], &["alibaba", "alibaba-cn"]),
        (&["kimi-", "moonshot-"], &["moonshotai", "moonshotai-cn"]),
        (&["glm-"], &["zhipuai", "zai"]),
        (&["grok-"], &["xai"]),
        (
            &[
                "mistral-",
                "open-mistral-",
                "magistral-",
                "ministral-",
                "codestral-",
                "devstral-",
                "pixtral-",
                "voxtral-",
            ],
            &["mistral"],
        ),
        (&["command-", "north-", "aya-", "tiny-aya-"], &["cohere"]),
        (&["minimax-"], &["minimax", "minimax-cn"]),
        (&["mimo-"], &["xiaomi"]),
        (&["mercury-"], &["inception"]),
        (&["sonar"], &["perplexity"]),
        (&["morph-"], &["morph"]),
        (&["step-", "stepaudio-"], &["stepfun"]),
        (&["muse-"], &["meta"]),
    ];
    let model = model.to_ascii_lowercase();
    FAMILIES
        .iter()
        .find(|(prefixes, _)| prefixes.iter().any(|prefix| model.starts_with(prefix)))
        .map_or(&[], |(_, providers)| *providers)
}
fn find_model<'a>(
    providers: &'a Providers,
    provider: Option<&str>,
    model: &str,
) -> Option<&'a Model> {
    if let Some(provider) = provider {
        return providers.get(provider)?.models.get(model);
    }
    official_providers(model)
        .iter()
        .filter_map(|id| providers.get(*id))
        .find_map(|p| p.models.get(model))
        .or_else(|| providers.values().find_map(|p| p.models.get(model)))
}
fn valid_logo(logo: &str) -> bool {
    let s = logo.to_ascii_lowercase();
    s.len() <= 128 * 1024
        && s.contains("<svg")
        && s.contains("</svg>")
        && ![
            "<script",
            "<image",
            "<foreignobject",
            "<!entity",
            "<!doctype",
            "href=",
            "url(http",
            "url('http",
            "url(\"http",
        ]
        .iter()
        .any(|v| s.contains(v))
}
impl Directory {
    pub(crate) fn open(root: PathBuf) -> Self {
        let bundled: Snapshot =
            serde_json::from_str(include_str!("../../assets/model-directory.json"))
                .expect("Bundled model directory");
        let snapshot = std::fs::read(root.join("models-dev/catalog.json"))
            .ok()
            .filter(|b| b.len() <= 32 * 1024 * 1024)
            .and_then(|b| serde_json::from_slice::<Snapshot>(&b).ok())
            .filter(|s| {
                parse(&serde_json::to_vec(&s.providers).unwrap_or_default()).is_ok()
                    && s.logos.values().all(|v| valid_logo(v))
            })
            .unwrap_or(bundled);
        Self {
            root,
            snapshot: RwLock::new(snapshot),
        }
    }
    pub(crate) fn options(&self) -> Value {
        let snapshot = self.snapshot.read().unwrap();
        let mut providers = Vec::new();
        for (id, p) in &snapshot.providers {
            let models: Vec<_> = p.models.iter().filter(|(_, m)| usable(m)).filter_map(|(mid, m)| {
                let (endpoint, protocol) = route(id, p, Some(m))?;
                Some(json!({"id":mid,"name":m.name,"endpoint":endpoint,"protocol":protocol,"context":m.limit.context,"input":m.limit.input,"output":m.limit.output,"image":m.modalities.input.iter().any(|s|s=="image")}))
            }).collect();
            let Some(first) = models.first() else {
                continue;
            };
            let (endpoint, protocol) = route(id, p, None)
                .map(|(e, p)| (json!(e), json!(p)))
                .unwrap_or_else(|| (first["endpoint"].clone(), first["protocol"].clone()));
            providers.push(json!({"id":id,"name":p.name,"endpoint":endpoint,"protocol":protocol,"doc":p.doc,"common":COMMON.contains(&id.as_str()),"logo":snapshot.logos.get(id),"models":models}));
        }
        providers.sort_by_key(|v| {
            (
                COMMON
                    .iter()
                    .position(|id| v["id"] == *id)
                    .unwrap_or(COMMON.len()),
                v["name"].as_str().unwrap_or("").to_lowercase(),
            )
        });
        json!(providers)
    }
    fn model(&self, provider: Option<&str>, model: &str) -> Option<Model> {
        let snapshot = self.snapshot.read().unwrap();
        find_model(&snapshot.providers, provider, model).cloned()
    }
    pub(crate) fn model_defaults(&self) -> Value {
        let snapshot = self.snapshot.read().unwrap();
        let defaults: BTreeMap<_, _> = snapshot
            .providers
            .values()
            .flat_map(|p| p.models.keys())
            .collect::<std::collections::BTreeSet<_>>()
            .into_iter()
            .map(|id| {
                let model = find_model(&snapshot.providers, None, id).unwrap();
                (id, &model.limit)
            })
            .collect();
        json!(defaults)
    }
    fn default_limits(limits: &Limits, context_override: Option<u64>) -> (u64, u64) {
        let context = context_override.unwrap_or(if limits.context > 0 {
            limits.context
        } else {
            32768
        });
        let output = (if limits.output > 0 {
            limits.output
        } else {
            8192
        })
        .min(context.saturating_sub(1))
        .max(1);
        (context, output)
    }
    #[cfg(test)]
    fn defaults(&self, provider: Option<&str>, model: &str) -> (u64, u64) {
        Self::default_limits(
            &self
                .model(provider, model)
                .map(|m| m.limit)
                .unwrap_or_default(),
            None,
        )
    }
    pub(crate) fn apply(&self, profile: &pot_core::Profile) -> Result<pot_core::Profile> {
        let mut profile = profile.clone();
        if let Some(settings) = &profile.model_settings {
            let model = self.model(settings.catalog_provider.as_deref(), &profile.model);
            let limits = model.as_ref().map(|m| m.limit.clone()).unwrap_or_default();
            let (context, output) = Self::default_limits(&limits, settings.context_window);
            profile.params.agent.context_window = context;
            profile.params.max_output_tokens = settings.max_output_tokens.unwrap_or(output);
            ensure!(
                profile.params.max_output_tokens > 0
                    && profile.params.max_output_tokens < profile.params.agent.context_window,
                "Output limit must be positive and smaller than the context window"
            );
            ensure!(
                limits.output == 0 || profile.params.max_output_tokens <= limits.output,
                "Output limit exceeds the model maximum"
            );
            ensure!(
                profile.params.agent.context_window > 1,
                "Context window must be greater than one"
            );
            profile.params.agent.context_reserve = profile
                .params
                .agent
                .context_reserve
                .min(profile.params.agent.context_window / 4);
            profile.params.agent.max_input_tokens = (limits.input > 0).then_some(limits.input);
            // Import only capabilities supported by Pot's existing adapters.
            // User-authored prompt/tool catalog overrides retain precedence.
            if let Some(model) = model {
                let mut defaults = json!({"input_modalities":if model.modalities.input.iter().any(|v|v=="image") { vec!["text","image"] } else { vec!["text"] }});
                let mut catalog =
                    profile
                        .params
                        .agent
                        .model_catalog
                        .take()
                        .unwrap_or(pot_core::ModelCatalog {
                            schema_version: 1,
                            default: json!({}),
                            models: Default::default(),
                        });
                pot_core::merge(&mut defaults, &catalog.default);
                catalog.default = defaults;
                profile.params.agent.model_catalog = Some(catalog);
            }
        }
        profile.validate()?;
        Ok(profile)
    }
    fn due(&self, now: i64) -> i64 {
        let previous = std::fs::read(self.root.join("models-dev/check.json"))
            .ok()
            .and_then(|b| serde_json::from_slice::<Check>(&b).ok())
            .map_or(0, |c| c.attempted_at);
        if previous > now {
            0
        } else {
            PERIOD_MS
                .saturating_sub(now.saturating_sub(previous))
                .max(0)
        }
    }
    pub(crate) async fn run(
        self: Arc<Self>,
        mut stop: tokio::sync::watch::Receiver<bool>,
        changes: pot_core::changes::Changes,
    ) {
        let mut next_attempt = 0;
        loop {
            if *stop.borrow() {
                return;
            }
            let wait = self
                .due(crate::now())
                .max(next_attempt - crate::now())
                .max(0);
            tokio::select! {
                _ = tokio::time::sleep(Duration::from_millis(wait as u64)) => {},
                _ = stop.changed() => return,
            }
            next_attempt = crate::now().saturating_add(PERIOD_MS);
            tokio::select! {
                result = self.refresh(SOURCE, "https://models.dev/logos") => match result {
                    Ok(()) => changes.notify("settings"),
                    Err(error) => eprintln!("Model directory refresh: {error:#}"),
                },
                _ = stop.changed() => return,
            }
        }
    }
    async fn refresh(&self, source: &str, logo_base: &str) -> Result<()> {
        // Record the attempt before networking. Restarts and errors do not cause retries.
        self.write(
            "check.json",
            &serde_json::to_vec(&Check {
                attempted_at: crate::now(),
            })?,
        )
        .await?;
        let client = reqwest::Client::builder()
            .timeout(Duration::from_secs(20))
            .retry(reqwest::retry::never())
            .redirect(reqwest::redirect::Policy::none())
            .build()?;
        let mut request = client.get(source);
        let previous = self.snapshot.read().unwrap().clone();
        if let Some(etag) = &previous.etag {
            request = request.header(reqwest::header::IF_NONE_MATCH, etag);
        }
        let response = request.send().await?;
        let (providers, etag) = if response.status() == reqwest::StatusCode::NOT_MODIFIED {
            (previous.providers.clone(), previous.etag.clone())
        } else {
            let response = response.error_for_status()?;
            let etag = response
                .headers()
                .get(reqwest::header::ETAG)
                .and_then(|v| v.to_str().ok())
                .map(str::to_owned);
            (parse(&bounded(response, MAX_BYTES).await?)?, etag)
        };
        let mut logos = previous.logos;
        let fetched: Vec<_> = stream::iter(providers.keys().cloned())
            .map(|id| {
                let client = client.clone();
                async move {
                    let result = async {
                        let response = client
                            .get(format!("{logo_base}/{id}.svg"))
                            .timeout(Duration::from_secs(6))
                            .send()
                            .await?
                            .error_for_status()?;
                        let bytes = bounded(response, 128 * 1024).await?;
                        let logo = String::from_utf8(bytes)?;
                        ensure!(valid_logo(&logo), "Invalid provider logo");
                        Ok::<_, anyhow::Error>(logo)
                    }
                    .await;
                    (id, result)
                }
            })
            .buffer_unordered(8)
            .collect()
            .await;
        for (id, logo) in fetched {
            if let Ok(logo) = logo {
                logos.insert(id, logo);
            }
        }
        let next = Snapshot {
            providers,
            logos,
            etag,
        };
        self.write("catalog.json", &serde_json::to_vec(&next)?)
            .await?;
        *self.snapshot.write().unwrap() = next;
        Ok(())
    }
    async fn write(&self, name: &str, bytes: &[u8]) -> Result<()> {
        let root = self.root.join("models-dev");
        tokio::fs::create_dir_all(&root).await?;
        let temporary = root.join(format!("{name}.{}.tmp", uuid::Uuid::new_v4()));
        tokio::fs::write(&temporary, bytes).await?;
        if let Err(error) = tokio::fs::rename(&temporary, root.join(name)).await {
            let _ = tokio::fs::remove_file(&temporary).await;
            return Err(error.into());
        }
        Ok(())
    }
}
async fn bounded(response: reqwest::Response, max: usize) -> Result<Vec<u8>> {
    ensure!(
        response.content_length().is_none_or(|n| n <= max as u64),
        "Model metadata response too large"
    );
    let mut stream = response.bytes_stream();
    let mut bytes = Vec::new();
    while let Some(chunk) = stream.next().await {
        let chunk = chunk.context("Read model metadata")?;
        ensure!(
            bytes.len().saturating_add(chunk.len()) <= max,
            "Model metadata response too large"
        );
        bytes.extend_from_slice(&chunk);
    }
    Ok(bytes)
}

#[cfg(test)]
mod tests {
    use super::*;
    fn directory() -> Directory {
        Directory::open(std::env::temp_dir().join(format!("pot-models-{}", uuid::Uuid::new_v4())))
    }
    fn fixture() -> Value {
        json!({"fixture":{"name":"Fixture","npm":"@ai-sdk/openai-compatible","api":"https://fixture.invalid/v1","models":{
            "chat":{"name":"Chat","tool_call":true,"modalities":{"input":["text","image"],"output":["text"]},"limit":{"context":64000,"input":50000,"output":4000}},
            "native":{"name":"Native","tool_call":true,"modalities":{"input":["text"],"output":["text"]},"limit":{"context":200000,"output":32000},"provider":{"npm":"@ai-sdk/anthropic","api":"https://fixture.invalid/claude/v1"}},
            "embed":{"name":"Embed","type":"embedding","limit":{"context":0,"output":0}},
            "old":{"name":"Old","tool_call":true,"modalities":{"input":["text"],"output":["text"]},"status":"deprecated","limit":{"context":0,"output":0}}
        }}})
    }
    fn install(d: &Directory) {
        d.snapshot.write().unwrap().providers =
            parse(&serde_json::to_vec(&fixture()).unwrap()).unwrap();
    }
    #[test]
    fn bundled_directory_is_offline_complete_and_filters_supported_routes() {
        let d = directory();
        let snapshot = d.snapshot.read().unwrap();
        parse(&serde_json::to_vec(&snapshot.providers).unwrap()).unwrap();
        assert!(snapshot.logos.len() >= 8);
        assert!(snapshot.logos.values().all(|logo| valid_logo(logo)));
        drop(snapshot);
        let options = d.options();
        assert!(options.as_array().unwrap().len() > 100);
        for id in COMMON {
            assert!(
                options.as_array().unwrap().iter().any(|p| p["id"] == *id),
                "{id}"
            );
        }
        assert!(
            !options
                .as_array()
                .unwrap()
                .iter()
                .any(|p| p["id"] == "amazon-bedrock")
        );
        install(&d);
        let options = d.options();
        let models = options[0]["models"].as_array().unwrap();
        assert_eq!(models.len(), 2);
        let native = models.iter().find(|m| m["id"] == "native").unwrap();
        assert_eq!(native["protocol"], "anthropic_messages");
        assert_eq!(
            native["endpoint"],
            "https://fixture.invalid/claude/v1/messages"
        );
    }
    #[test]
    fn exact_identity_defaults_manual_overrides_and_zero_limits() {
        let d = directory();
        install(&d);
        assert_eq!(d.defaults(Some("fixture"), "chat"), (64000, 4000));
        assert_eq!(d.defaults(Some("unknown"), "chat"), (32768, 8192));
        assert_eq!(d.defaults(Some("fixture"), "unlisted"), (32768, 8192));
        assert_eq!(d.defaults(Some("fixture"), "old"), (32768, 8192));
        assert_eq!(d.defaults(Some("fixture"), "native"), (200000, 32000));
        let config = crate::execution::Config {
            model: "chat".into(),
            ..Default::default()
        };
        let mut profile = pot_core::Profile::api(&config);
        profile.model_settings = Some(pot_core::ModelSettings {
            catalog_provider: Some("fixture".into()),
            ..Default::default()
        });
        let automatic = d.apply(&profile).unwrap();
        assert_eq!(automatic.params.agent.context_window, 64000);
        assert_eq!(automatic.params.agent.max_input_tokens, Some(50000));
        assert_eq!(automatic.params.max_output_tokens, 4000);
        assert!(
            automatic
                .params
                .agent
                .model_descriptor(&automatic.provider, &automatic.model)
                .unwrap()
                .vision()
        );
        assert_eq!(profile.params.agent.context_window, 32768);
        profile.params.agent.model_catalog = Some(
            serde_json::from_value(
                json!({"schema_version":1,"default":{"input_modalities":["text"]},"models":{}}),
            )
            .unwrap(),
        );
        assert!(
            !d.apply(&profile)
                .unwrap()
                .params
                .agent
                .model_descriptor(&profile.provider, &profile.model)
                .unwrap()
                .vision()
        );
        profile.model_settings.as_mut().unwrap().context_window = Some(16000);
        profile.model_settings.as_mut().unwrap().max_output_tokens = Some(1000);
        let manual = d.apply(&profile).unwrap();
        assert_eq!(manual.params.agent.context_window, 16000);
        assert_eq!(manual.params.max_output_tokens, 1000);
        profile.model_settings.as_mut().unwrap().context_window = Some(1000);
        assert!(d.apply(&profile).is_err());
        profile.model_settings.as_mut().unwrap().context_window = None;
        profile.model_settings.as_mut().unwrap().max_output_tokens = Some(4001);
        assert!(d.apply(&profile).is_err());
        profile.model_settings.as_mut().unwrap().max_output_tokens = None;
        assert_eq!(
            d.apply(&profile).unwrap().params.agent.context_window,
            64000
        );
    }
    #[test]
    fn custom_models_prefer_official_then_first_provider_and_share_public_defaults() {
        let d = directory();
        install(&d);
        {
            let mut snapshot = d.snapshot.write().unwrap();
            let mut proxy = snapshot.providers["fixture"].clone();
            let mut official = proxy.clone();
            proxy
                .models
                .insert("claude-test".into(), proxy.models["chat"].clone());
            official
                .models
                .insert("claude-test".into(), official.models["native"].clone());
            official.models.remove("chat");
            // Metadata remains available even without a supported ready-connection route.
            official.npm = "unsupported-sdk".into();
            snapshot.providers.insert("aaa-proxy".into(), proxy);
            snapshot.providers.insert("anthropic".into(), official);
            snapshot
                .providers
                .get_mut("fixture")
                .unwrap()
                .models
                .get_mut("chat")
                .unwrap()
                .limit
                .context = 80000;
        }
        assert_eq!(d.defaults(None, "claude-test"), (200000, 32000));
        assert_eq!(d.defaults(None, "chat"), (64000, 4000));
        assert_eq!(d.defaults(None, "unlisted"), (32768, 8192));
        assert_eq!(d.defaults(None, "Claude-test"), (32768, 8192));
        assert_eq!(d.defaults(Some("aaa-proxy"), "claude-test"), (64000, 4000));
        assert_eq!(d.defaults(Some("anthropic"), "chat"), (32768, 8192));
        let public = d.model_defaults();
        assert_eq!(public["claude-test"]["context"], 200000);
        assert_eq!(public["claude-test"]["output"], 32000);
        assert_eq!(public["chat"]["context"], 64000);
        let mut profile = pot_core::Profile::api(&crate::execution::Config {
            model: "claude-test".into(),
            ..Default::default()
        });
        profile.model_settings = Some(pot_core::ModelSettings::default());
        let resolved = d.apply(&profile).unwrap();
        assert_eq!(resolved.params.agent.context_window, 200000);
        assert_eq!(resolved.params.max_output_tokens, 32000);
        profile.model_settings.as_mut().unwrap().context_window = Some(16000);
        assert_eq!(d.apply(&profile).unwrap().params.max_output_tokens, 15999);
        // Missing the exact ID in the official provider still falls back to the proxy.
        d.snapshot
            .write()
            .unwrap()
            .providers
            .get_mut("anthropic")
            .unwrap()
            .models
            .remove("claude-test");
        assert_eq!(d.defaults(None, "claude-test"), (64000, 4000));
    }
    #[test]
    fn default_output_respects_the_selected_context_and_unknown_limits() {
        let limits = Limits {
            context: 16000,
            input: 0,
            output: 32000,
        };
        assert_eq!(Directory::default_limits(&limits, None), (16000, 15999));
        assert_eq!(
            Directory::default_limits(&limits, Some(64000)),
            (64000, 32000)
        );
        assert_eq!(
            Directory::default_limits(&Limits::default(), None),
            (32768, 8192)
        );
        assert_eq!(
            Directory::default_limits(&Limits::default(), Some(4000)),
            (4000, 3999)
        );
    }
    #[tokio::test]
    async fn refresh_is_atomic_conditional_and_failure_waits_24_hours_across_restart() {
        use axum::{
            Router,
            http::{HeaderMap, StatusCode},
            routing::get,
        };
        let d = directory();
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let address = listener.local_addr().unwrap();
        let app=Router::new().route("/api",get(|headers:HeaderMap|async move {
            assert!(!headers.contains_key("authorization"));
            if headers.get("if-none-match").is_some_and(|v|v=="fixture-etag") {
                (StatusCode::NOT_MODIFIED,[("etag","fixture-etag")],String::new())
            } else { (StatusCode::OK,[("etag","fixture-etag")],fixture().to_string()) }
        })).route("/bad",get(||async {"{broken"})).route("/empty",get(||async {"{}"})).route("/error",get(||async {StatusCode::BAD_GATEWAY})).route("/logos/{id}",get(||async {"<svg xmlns=\"http://www.w3.org/2000/svg\" viewBox=\"0 0 24 24\"><path d=\"M0 0h12v12z\"/></svg>"}));
        let server = tokio::spawn(async move { axum::serve(listener, app).await.unwrap() });
        let base = format!("http://{address}");
        d.refresh(&format!("{base}/api"), &format!("{base}/logos"))
            .await
            .unwrap();
        let path = d.root.join("models-dev/catalog.json");
        let saved = std::fs::read(&path).unwrap();
        assert_eq!(d.options()[0]["id"], "fixture");
        assert!(d.options()[0]["logo"].as_str().unwrap().contains("<svg"));
        let existing_spec = d
            .apply(&{
                let mut profile = pot_core::Profile::api(&crate::execution::Config {
                    model: "chat".into(),
                    ..Default::default()
                });
                profile.model_settings = Some(pot_core::ModelSettings {
                    catalog_provider: Some("fixture".into()),
                    ..Default::default()
                });
                profile
            })
            .unwrap()
            .resolve()
            .unwrap();
        for suffix in ["api", "bad", "empty", "error"] {
            let result = d
                .refresh(&format!("{base}/{suffix}"), &format!("{base}/logos"))
                .await;
            assert_eq!(result.is_ok(), suffix == "api");
            assert_eq!(std::fs::read(&path).unwrap(), saved);
            assert_eq!(d.options()[0]["id"], "fixture");
            let reopened = Directory::open(d.root.clone());
            assert_eq!(reopened.options()[0]["id"], "fixture");
            assert!(reopened.due(crate::now()) > PERIOD_MS - 10000);
            assert_eq!(reopened.due(crate::now() + PERIOD_MS), 0);
        }
        install(&d);
        d.snapshot
            .write()
            .unwrap()
            .providers
            .get_mut("fixture")
            .unwrap()
            .models
            .get_mut("chat")
            .unwrap()
            .limit
            .context = 128000;
        assert_eq!(existing_spec.context_window, 64000);
        server.abort();
        let _ = std::fs::remove_dir_all(&d.root);
    }
    #[test]
    fn malformed_cache_uses_bundle_and_metadata_never_selects_execution_authority() {
        let d = directory();
        std::fs::create_dir_all(d.root.join("models-dev")).unwrap();
        std::fs::write(d.root.join("models-dev/catalog.json"), "broken").unwrap();
        assert!(
            Directory::open(d.root.clone())
                .options()
                .as_array()
                .unwrap()
                .len()
                > 100
        );
        assert!(!valid_logo("<svg><script>bad</script></svg>"));
        assert!(parse(b"{}").is_err());
        let mut bad = fixture();
        bad["fixture"]["models"]["chat"]["limit"]["context"] = json!(-1);
        assert!(parse(&serde_json::to_vec(&bad).unwrap()).is_err());
        let _ = std::fs::remove_dir_all(d.root);
    }
}
