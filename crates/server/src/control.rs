//! A standalone Control owns members; execution nodes connect outward to one Control.
use anyhow::{Result, ensure};
use base64::{Engine, engine::general_purpose::URL_SAFE_NO_PAD as B64};
use pot_core::{ExecutionTarget, ExecutionTargetKind as Target};
use ring::{
    rand::{SecureRandom, SystemRandom},
    signature::{self, KeyPair},
};
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use std::{
    collections::BTreeMap,
    path::{Path, PathBuf},
    sync::{Arc, Mutex},
};

mod management;
mod remote;
mod task_agent;
mod task_services;
const VERSION: u32 = 1;
const CODE_MS: i64 = 300_000;
const LEASE_MS: i64 = 30_000;
#[derive(Debug)]
struct RemoteRejection(String);
impl std::fmt::Display for RemoteRejection {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "Remote machine rejected request: {}", self.0)
    }
}
impl std::error::Error for RemoteRejection {}
fn now() -> i64 {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .unwrap()
        .as_millis() as i64
}
fn secret() -> Result<String> {
    let mut bytes = [0; 32];
    SystemRandom::new()
        .fill(&mut bytes)
        .map_err(|_| anyhow::anyhow!("Random source unavailable"))?;
    Ok(B64.encode(bytes))
}
#[derive(Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
struct ClientCredential {
    schema_version: u32,
    token: String,
}
impl Authority {
    pub(super) fn is_member(&self) -> bool {
        self.book.lock().unwrap().control.is_some()
    }
    // Only the explicit Control HTTPS listener accepts this durable client authority.
    // Local process tokens, boot identities and member credentials remain independent.
    fn client_token(&self) -> Result<String> {
        let path = self.data.join("control-client-credential.json");
        let credential: ClientCredential = if path.exists() {
            serde_json::from_slice(&std::fs::read(&path)?)?
        } else {
            let credential = ClientCredential {
                schema_version: 1,
                token: secret()?,
            };
            private_json(&path, &credential)?;
            credential
        };
        ensure!(
            credential.schema_version == 1 && B64.decode(&credential.token)?.len() == 32,
            "Invalid Control client credential"
        );
        Ok(credential.token)
    }
}
fn control_client_router(
    router: axum::Router,
    client_token: String,
    native_token: String,
) -> axum::Router {
    router.layer(axum::middleware::from_fn(
        move |mut request: axum::extract::Request, next: axum::middleware::Next| {
            let client_token = client_token.clone();
            let native_token = native_token.clone();
            async move {
                let supplied = request
                    .headers()
                    .get("authorization")
                    .and_then(|h| h.to_str().ok())
                    .and_then(|h| h.strip_prefix("Bearer "));
                if !request.headers().contains_key("origin")
                    && supplied.is_some_and(|token| {
                        token.len() == client_token.len()
                            && openssl::memcmp::eq(token.as_bytes(), client_token.as_bytes())
                    })
                {
                    request.headers_mut().insert(
                        "authorization",
                        format!("Bearer {native_token}").parse().unwrap(),
                    );
                }
                next.run(request).await
            }
        },
    ))
}
// Serialize signed protocol data independently of serde_json feature unification.
// App builds may preserve object insertion order while standalone Linux sorts it.
fn canonical_bytes(value: &impl Serialize) -> Result<Vec<u8>> {
    let mut value = serde_json::to_value(value)?;
    value.sort_all_objects();
    Ok(serde_json::to_vec(&value)?)
}
fn protocol_digest(value: &impl Serialize) -> Result<String> {
    Ok(B64.encode(ring::digest::digest(&ring::digest::SHA256, &canonical_bytes(value)?).as_ref()))
}
// Durable local identities retain their existing encoding and paths.
fn digest(value: &impl Serialize) -> Result<String> {
    Ok(B64
        .encode(ring::digest::digest(&ring::digest::SHA256, &serde_json::to_vec(value)?).as_ref()))
}
pub(super) fn private_json(path: &Path, value: &impl Serialize) -> Result<()> {
    use std::{io::Write, os::unix::fs::OpenOptionsExt};
    let temp = path.with_extension("tmp");
    let mut file = std::fs::OpenOptions::new()
        .write(true)
        .create(true)
        .truncate(true)
        .mode(0o600)
        .open(&temp)?;
    file.write_all(&serde_json::to_vec_pretty(value)?)?;
    file.sync_all()?;
    std::fs::rename(temp, path)?;
    std::fs::File::open(path.parent().unwrap())?.sync_all()?;
    Ok(())
}
#[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub(crate) struct Identity {
    pub runtime_id: String,
    pub public_key: String,
    pub fingerprint: String,
}
impl Identity {
    fn validate(&self) -> Result<()> {
        uuid::Uuid::parse_str(&self.runtime_id)?;
        let key = B64.decode(&self.public_key)?;
        ensure!(
            key.len() == 32 && self.fingerprint == digest(&self.public_key)?,
            "Invalid Runtime identity"
        );
        Ok(())
    }
    fn verify(&self, payload: &impl Serialize, signature: &str) -> Result<()> {
        self.validate()?;
        signature::UnparsedPublicKey::new(&signature::ED25519, B64.decode(&self.public_key)?)
            .verify(&canonical_bytes(payload)?, &B64.decode(signature)?)
            .map_err(|_| anyhow::anyhow!("Runtime signature rejected"))
    }
}
#[derive(Clone, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
struct Keys {
    schema_version: u32,
    runtime_id: String,
    pkcs8: String,
}
#[derive(Clone, Serialize, Deserialize)]
struct Member {
    #[serde(default)]
    tags: Vec<String>,
    identity: Identity,
    token: String,
    enabled: bool,
    inventory: Value,
    last_seen_ms: Option<i64>,
}
#[derive(Clone, Serialize, Deserialize)]
struct Binding {
    identity: Identity,
    token: String,
    url: String,
    ca_pem: Option<String>,
}
#[derive(Clone, Serialize, Deserialize)]
struct Redemption {
    request: JoinRequest,
    token: String,
}
#[derive(Clone, Serialize, Deserialize)]
struct Code {
    expires_at_ms: i64,
    redemption: Option<Redemption>,
}
#[derive(Clone, Serialize, Deserialize)]
struct Lease {
    id: String,
    control_id: String,
    owner: String,
    run_id: String,
    generation: u64,
    target: Target,
    session: String,
    boot_id: String,
    expires_at_ms: i64,
    cancelled: bool,
    resources_revision: Option<String>,
    #[serde(default)]
    replacement: Option<String>,
}
#[derive(Clone, Serialize, Deserialize, PartialEq)]
#[serde(deny_unknown_fields)]
pub(super) struct Join {
    request_id: String,
    url: String,
    invitation: String,
    expected_runtime_id: String,
    expected_fingerprint: String,
    #[serde(default)]
    ca_pem: Option<String>,
}
#[derive(Clone, Serialize, Deserialize)]
struct Pending {
    request: Join,
    completed: bool,
}
#[derive(Clone, Default, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
struct Book {
    #[serde(default)]
    local_tags: Option<Vec<String>>,
    client_runs: BTreeMap<String, remote::ClientRun>,
    client_management: BTreeMap<String, management::Intent>,
    schema_version: u32,
    control: Option<Binding>,
    codes: BTreeMap<String, Code>,
    leases: BTreeMap<String, Lease>,
    members: BTreeMap<String, Member>,
    pending: BTreeMap<String, Pending>,
}
#[derive(Clone, Serialize, Deserialize, PartialEq)]
#[serde(deny_unknown_fields)]
struct JoinRequest {
    #[serde(default)]
    os: String,
    schema_version: u32,
    request_id: String,
    caller: Identity,
    invitation: String,
}
#[derive(Clone, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
struct Poll {
    schema_version: u32,
    caller_id: String,
    recipient_id: String,
    issued_at_ms: i64,
    nonce: String,
    reply: Option<Signed<Value>>,
}
struct Delivery {
    member: String,
    request: Signed<Envelope>,
    reply: tokio::sync::oneshot::Sender<Signed<Value>>,
}
#[derive(Clone, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
struct Signed<T> {
    payload: T,
    signature: String,
}
#[derive(Clone, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
struct Envelope {
    schema_version: u32,
    caller_id: String,
    recipient_id: String,
    issued_at_ms: i64,
    command_id: String,
    body: Rpc,
}
#[derive(Clone, Serialize, Deserialize)]
#[serde(tag = "action", rename_all = "snake_case", deny_unknown_fields)]
pub(super) enum Rpc {
    LaunchTask {
        lease_id: String,
        launch: Box<pot_runner::Launch>,
    },
    InspectTask {
        task_id: String,
        run_id: String,
    },
    RecoverTaskLease {
        lease_id: String,
    },
    ReadFile {
        owner: String,
        target: Target,
        query: pot_protocol::FileQuery,
    },
    SandboxLifecycle {
        operation_id: String,
        accept_before_ms: i64,
        target: Target,
        args: Value,
    },
    InspectManagement {
        operation_id: String,
    },
    Inventory,
    ConfigureSandboxResources {
        budget: Value,
    },
    Resources {
        lease_id: String,
        resources: pot_runner::resources::GlobalResources,
    },
    Context {
        lease_id: String,
    },
    Prepare {
        lease_id: String,
        name: String,
        args: Value,
    },
    Acquire {
        owner: String,
        run_id: String,
        generation: u64,
        target: Target,
    },
    Renew {
        lease_id: String,
    },
    Execute {
        lease_id: String,
        call_id: String,
        name: String,
        args: Value,
    },
    Inspect {
        lease_id: String,
        call_id: String,
    },
    Cancel {
        lease_id: String,
    },
}
#[derive(Clone, Serialize, Deserialize)]
struct Receipt {
    schema_version: u32,
    request: Value,
    boot_id: String,
    result: Option<Value>,
}

pub(crate) struct Authority {
    pub(super) ui_changed: pot_core::changes::Signal,
    identity: Identity,
    key: signature::Ed25519KeyPair,
    data: PathBuf,
    book: Mutex<Book>,
    operations: tokio::sync::Mutex<()>,
    management_calls: tokio::sync::Mutex<()>,
    task_journal: Mutex<Option<Arc<pot_runner::Journal>>>,
    stopping: std::sync::atomic::AtomicBool,
    tasks: Mutex<Vec<tokio::task::JoinHandle<()>>>,
    active: Mutex<std::collections::BTreeSet<PathBuf>>,
    control_allowed: bool,
    deliveries: Mutex<BTreeMap<String, Delivery>>,
    queue: Mutex<std::collections::VecDeque<String>>,
    changed: tokio::sync::Notify,
    member_worker: tokio::sync::Mutex<Option<tokio::task::JoinHandle<()>>>,
    listener: tokio::sync::Mutex<
        Option<(
            axum_server::Handle<std::net::SocketAddr>,
            tokio::task::JoinHandle<()>,
        )>,
    >,
}
impl Authority {
    pub(super) fn local_tags(&self) -> Vec<String> {
        let book = self.book.lock().unwrap();
        book.local_tags.clone().unwrap_or_else(|| {
            pot_protocol::references::default_labels(book.control.is_none(), std::env::consts::OS)
        })
    }
    fn set_tags(&self, runtime: &str, mut tags: Vec<String>) -> Result<Value> {
        ensure!(
            tags.len() <= 64
                && tags
                    .iter()
                    .all(|t| pot_protocol::references::valid_label(t)),
            "Use up to 64 lowercase labels (letters, digits, dash, underscore)"
        );
        tags.sort();
        tags.dedup();
        self.update(|book| {
            ensure!(book.control.is_none(), "Labels are managed by Control");
            if runtime == self.identity.runtime_id {
                book.local_tags = Some(tags.clone());
            } else {
                book.members
                    .get_mut(runtime)
                    .filter(|m| m.enabled)
                    .ok_or_else(|| anyhow::anyhow!("Machine is not connected"))?
                    .tags = tags.clone();
            }
            Ok(json!({"tags":tags}))
        })
    }

    #[cfg(test)]
    pub fn open(data: &Path, runtime_id: &str) -> Result<Arc<Self>> {
        Self::open_policy(data, runtime_id, true)
    }
    pub fn open_policy(data: &Path, runtime_id: &str, control_allowed: bool) -> Result<Arc<Self>> {
        let key_path = data.join("control-identity.json");
        let keys: Keys = if key_path.exists() {
            serde_json::from_slice(&std::fs::read(&key_path)?)?
        } else {
            let document = signature::Ed25519KeyPair::generate_pkcs8(&SystemRandom::new())
                .map_err(|_| anyhow::anyhow!("Identity generation failed"))?;
            let keys = Keys {
                schema_version: VERSION,
                runtime_id: runtime_id.into(),
                pkcs8: B64.encode(document.as_ref()),
            };
            private_json(&key_path, &keys)?;
            keys
        };
        ensure!(
            keys.schema_version == VERSION && keys.runtime_id == runtime_id,
            "Runtime identity belongs to another Runtime or schema"
        );
        let key = signature::Ed25519KeyPair::from_pkcs8(&B64.decode(&keys.pkcs8)?)
            .map_err(|_| anyhow::anyhow!("Invalid private Runtime identity"))?;
        let public_key = B64.encode(key.public_key().as_ref());
        let identity = Identity {
            runtime_id: runtime_id.into(),
            fingerprint: digest(&public_key)?,
            public_key,
        };
        let path = data.join("runtime-control.json");
        let book: Book = if path.exists() {
            let book: Book = serde_json::from_slice(&std::fs::read(&path)?)?;
            ensure!(book.schema_version == 2, "Unsupported Control schema");
            book
        } else {
            Book {
                schema_version: 2,
                ..Default::default()
            }
        };
        Ok(Arc::new(Self {
            ui_changed: Default::default(),
            identity,
            key,
            data: data.into(),
            book: Mutex::new(book),
            operations: Default::default(),
            management_calls: Default::default(),
            task_journal: Default::default(),
            stopping: Default::default(),
            tasks: Default::default(),
            active: Default::default(),
            listener: Default::default(),
            control_allowed,
            deliveries: Default::default(),
            queue: Default::default(),
            changed: Default::default(),
            member_worker: Default::default(),
        }))
    }
    fn save(&self, book: &Book) -> Result<()> {
        private_json(&self.data.join("runtime-control.json"), book)
    }
    fn update<T>(&self, change: impl FnOnce(&mut Book) -> Result<T>) -> Result<T> {
        ensure!(
            !self.stopping.load(std::sync::atomic::Ordering::SeqCst),
            "Control authority is shutting down"
        );
        let mut committed = self.book.lock().unwrap();
        let mut next = committed.clone();
        let result = change(&mut next)?;
        self.save(&next)?;
        let projection = |book: &Book| {
            let mut value =
                json!({"members":book.members,"control":book.control,"local_tags":book.local_tags});
            if let Some(members) = value["members"].as_object_mut() {
                for member in members.values_mut() {
                    if let Some(object) = member.as_object_mut() {
                        object.remove("last_seen_ms");
                    }
                }
            }
            value
        };
        let changed = projection(&committed) != projection(&next);
        *committed = next;
        if changed {
            self.ui_changed.notify();
        }
        Ok(result)
    }
    fn sign<T: Serialize>(&self, payload: T) -> Result<Signed<T>> {
        let signature = B64.encode(self.key.sign(&canonical_bytes(&payload)?).as_ref());
        Ok(Signed { payload, signature })
    }
    fn is_control(&self) -> bool {
        std::fs::read(self.data.join("control-listener.json"))
            .ok()
            .and_then(|b| serde_json::from_slice::<Option<Listen>>(&b).ok())
            .flatten()
            .is_some()
    }
    fn create_invitation(&self) -> Result<Value> {
        ensure!(
            self.control_allowed && self.is_control(),
            "Enable a standalone Control first"
        );
        let invitation = secret()?;
        self.update(|book| {
            ensure!(book.control.is_none(), "An execution member cannot become Control");
            book.codes.retain(|_, c| c.expires_at_ms > now());
            book.codes.insert(digest(&invitation)?, Code { expires_at_ms: now()+CODE_MS, redemption: None });
            Ok(json!({"invitation":invitation,"expires_at_ms":now()+CODE_MS,"identity":self.identity}))
        })
    }
    fn register(&self, signed: Signed<JoinRequest>) -> Result<Signed<Value>> {
        ensure!(self.control_allowed, "Embedded Runtime cannot be Control");
        let request = signed.payload;
        request.caller.verify(&request, &signed.signature)?;
        ensure!(
            request.schema_version == VERSION
                && request.caller.runtime_id != self.identity.runtime_id,
            "Invalid Control membership"
        );
        ensure!(
            pot_protocol::references::valid_label(&request.os),
            "Invalid OS label"
        );
        uuid::Uuid::parse_str(&request.request_id)?;
        ensure!(
            B64.decode(&request.invitation)?.len() == 32,
            "Invalid invitation"
        );
        self.update(|book| {
            ensure!(
                book.control.is_none(),
                "Execution node cannot register members"
            );
            let hash = digest(&request.invitation)?;
            let code = book
                .codes
                .get(&hash)
                .ok_or_else(|| anyhow::anyhow!("Invitation rejected"))?;
            ensure!(code.expires_at_ms > now(), "Invitation expired");
            let token = if let Some(redemption) = &code.redemption {
                ensure!(redemption.request == request, "Invitation already used");
                let member = book
                    .members
                    .get(&request.caller.runtime_id)
                    .ok_or_else(|| anyhow::anyhow!("Member removed"))?;
                ensure!(
                    member.enabled && member.token == redemption.token,
                    "Membership removed; create a new invitation"
                );
                redemption.token.clone()
            } else {
                ensure!(
                    !book
                        .members
                        .get(&request.caller.runtime_id)
                        .is_some_and(|m| m.enabled),
                    "Machine already joined"
                );
                secret()?
            };
            book.codes.get_mut(&hash).unwrap().redemption = Some(Redemption {
                request: request.clone(),
                token: token.clone(),
            });
            book.members
                .entry(request.caller.runtime_id.clone())
                .and_modify(|m| {
                    m.token = token.clone();
                    m.enabled = true;
                })
                .or_insert(Member {
                    tags: pot_protocol::references::default_labels(false, &request.os),
                    identity: request.caller.clone(),
                    token: token.clone(),
                    enabled: true,
                    inventory: json!({"items":[]}),
                    last_seen_ms: None,
                });
            ensure!(
                book.members[&request.caller.runtime_id].identity == request.caller,
                "Member identity changed"
            );
            self.sign(
                json!({"identity":self.identity,"request_digest":protocol_digest(&request)?,"token":token}),
            )
        })
    }
    fn control_session(&self, control: &str) -> Result<String> {
        let book = self.book.lock().unwrap();
        let binding = book
            .control
            .as_ref()
            .filter(|c| c.identity.runtime_id == control)
            .ok_or_else(|| anyhow::anyhow!("Caller is not the bound Control"))?;
        digest(&binding.token)
    }
    fn authorize(&self, token: &str, request: &Signed<Envelope>) -> Result<String> {
        let e = &request.payload;
        ensure!(
            !self.stopping.load(std::sync::atomic::Ordering::SeqCst),
            "Runtime shutting down"
        );
        ensure!(
            e.schema_version == VERSION
                && e.recipient_id == self.identity.runtime_id
                && e.issued_at_ms.abs_diff(now()) <= 30_000,
            "Command recipient or deadline rejected"
        );
        let book = self.book.lock().unwrap();
        let binding = book
            .control
            .as_ref()
            .filter(|c| c.identity.runtime_id == e.caller_id)
            .ok_or_else(|| anyhow::anyhow!("Unknown Control"))?;
        ensure!(
            token.len() == binding.token.len()
                && openssl::memcmp::eq(token.as_bytes(), binding.token.as_bytes()),
            "Control credential rejected"
        );
        binding.identity.verify(e, &request.signature)?;
        Ok(e.caller_id.clone())
    }
    fn acquire(
        &self,
        control: &str,
        owner: &str,
        run: &str,
        generation: u64,
        target: &Target,
        boot: &str,
    ) -> Result<Value> {
        ensure!(
            !owner.is_empty() && owner.len() <= 128 && generation > 0,
            "Invalid execution owner or generation"
        );
        uuid::Uuid::parse_str(run)?;
        self.update(|book| {
            let binding = book
                .control
                .as_ref()
                .filter(|c| c.identity.runtime_id == control)
                .ok_or_else(|| anyhow::anyhow!("Not the bound Control"))?;
            let session = digest(&binding.token)?;
            if let Some(lease) = book.leases.values().find(|l| {
                l.control_id == control
                    && l.owner == owner
                    && l.run_id == run
                    && l.generation == generation
                    && &l.target == target
            }) {
                ensure!(
                    self.lease_valid(book, lease, boot),
                    "Existing lease expired or fenced; reconcile the original run"
                );
                return Ok(json!(lease));
            }
            let lease = Lease {
                id: secret()?,
                control_id: control.into(),
                owner: owner.into(),
                run_id: run.into(),
                generation,
                target: target.clone(),
                session,
                boot_id: boot.into(),
                expires_at_ms: now() + LEASE_MS,
                cancelled: false,
                resources_revision: None,
                replacement: None,
            };
            book.leases.insert(lease.id.clone(), lease.clone());
            Ok(json!(lease))
        })
    }
    fn lease_valid(&self, book: &Book, lease: &Lease, boot: &str) -> bool {
        !self.stopping.load(std::sync::atomic::Ordering::SeqCst)
            && !lease.cancelled
            && lease.boot_id == boot
            && lease.expires_at_ms > now()
            && book.control.as_ref().is_some_and(|c| {
                c.identity.runtime_id == lease.control_id
                    && digest(&c.token).ok().as_ref() == Some(&lease.session)
            })
    }
    fn lease(&self, control: &str, id: &str, boot: &str, effect: bool) -> Result<Lease> {
        let book = self.book.lock().unwrap();
        let lease = book
            .leases
            .get(id)
            .ok_or_else(|| anyhow::anyhow!("Lease not found"))?;
        ensure!(
            lease.control_id == control,
            "Lease belongs to another control"
        );
        ensure!(
            !effect || self.lease_valid(&book, lease, boot),
            "Lease expired, cancelled or fenced"
        );
        Ok(lease.clone())
    }
    fn renew(&self, control: &str, id: &str, boot: &str) -> Result<Value> {
        self.lease(control, id, boot, true)?;
        self.update(|book| {
            let old = book.leases.get(id).unwrap();
            ensure!(
                self.lease_valid(book, old, boot),
                "Lease expired, cancelled or fenced"
            );
            let lease = book.leases.get_mut(id).unwrap();
            lease.expires_at_ms = now() + LEASE_MS;
            let value = json!(lease);
            Ok(value)
        })
    }
    fn cancel(&self, control: &str, id: &str) -> Result<Lease> {
        self.update(|book| {
            let lease = book
                .leases
                .get_mut(id)
                .ok_or_else(|| anyhow::anyhow!("Lease not found"))?;
            ensure!(
                lease.control_id == control,
                "Lease belongs to another control"
            );
            lease.cancelled = true;
            let result = lease.clone();
            Ok(result)
        })
    }
    fn receipt_path(&self, lease: &Lease, call: &str) -> Result<PathBuf> {
        ensure!(
            !call.is_empty() && call.len() <= 256,
            "Invalid operation identity"
        );
        Ok(self
            .data
            .join("execution-operations")
            .join(digest(&(lease.id.as_str(), call))?))
    }
    pub(super) fn metadata(&self) -> Value {
        let book = self.book.lock().unwrap();
        json!({"now_ms":now(),"identity":self.identity,"role":if book.control.is_some(){"member"}else if self.is_control(){"control"}else{"local"},
            "control":book.control.as_ref().map(|c|json!({"identity":c.identity,"url":c.url})),
            "members":book.members.values().map(|m|json!({"identity":m.identity,"enabled":m.enabled,"last_seen_ms":m.last_seen_ms,"inventory":m.inventory})).collect::<Vec<_>>()})
    }
}
use crate::resources::Resources;
use axum::{
    Json, Router,
    extract::State,
    http::{HeaderMap, StatusCode},
    response::{IntoResponse, Response},
    routing::get,
};

#[derive(Clone, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub(super) struct Listen {
    address: std::net::SocketAddr,
    certificate: PathBuf,
    private_key: PathBuf,
}
#[derive(Deserialize)]
#[serde(tag = "action", rename_all = "snake_case", deny_unknown_fields)]
pub(super) enum Operator {
    CreateInvitation,
    EnableControl {
        config: Listen,
    },
    DisableControl,
    Preview {
        url: String,
        ca_pem: Option<String>,
    },
    Join {
        request: Join,
    },
    Leave,
    RemoveMember {
        runtime_id: String,
    },
    SetTags {
        runtime_id: String,
        tags: Vec<String>,
    },
    Call {
        runtime_id: String,
        body: Rpc,
    },
}
fn response(result: Result<Value>) -> Response {
    match result {
        Ok(value) => Json(value).into_response(),
        Err(error) => (
            StatusCode::BAD_REQUEST,
            Json(json!({"error":format!("{error:#}")})),
        )
            .into_response(),
    }
}
pub(super) async fn metadata(
    State(resources): State<Arc<Resources>>,
    headers: HeaderMap,
) -> Response {
    if !resources.authorize(&headers) {
        return StatusCode::UNAUTHORIZED.into_response();
    }
    Json(resources.control.metadata()).into_response()
}
/// Browser/native machine controls deliberately exclude listener configuration and raw RPC.
pub(super) async fn connection_operator(
    state: State<Arc<Resources>>,
    headers: HeaderMap,
    Json(command): Json<Operator>,
) -> Response {
    if matches!(
        command,
        Operator::EnableControl { .. }
            | Operator::DisableControl
            | Operator::Call { .. }
            | Operator::Join { .. }
            | Operator::Leave
    ) {
        return StatusCode::FORBIDDEN.into_response();
    }
    operator(state, headers, Json(command)).await
}
pub(super) async fn operator(
    State(resources): State<Arc<Resources>>,
    headers: HeaderMap,
    Json(command): Json<Operator>,
) -> Response {
    if !resources.authorize(&headers) {
        return StatusCode::UNAUTHORIZED.into_response();
    }
    let authority = &resources.control;
    response(
        async {
            match command {
                Operator::CreateInvitation => authority.create_invitation(),
                Operator::EnableControl { config } => {
                    authority.start_listener(resources.clone(), config).await?;
                    Ok(json!({"control":true}))
                }
                Operator::DisableControl => {
                    authority.stop_listener(true).await?;
                    Ok(json!({"control":false}))
                }
                Operator::Preview { url, ca_pem } => {
                    Ok(json!({"identity":preview(&url,ca_pem.as_deref()).await?}))
                }
                Operator::Join { request } => {
                    let result = resources.join_control(request).await?;
                    authority.start_member(resources.clone()).await;
                    Ok(result)
                }
                Operator::Leave => {
                    authority.leave().await?;
                    Ok(json!({"left":true}))
                }
                Operator::SetTags { runtime_id, tags } => authority.set_tags(&runtime_id, tags),
                Operator::RemoveMember { runtime_id } => {
                    authority.remove_member(&runtime_id)?;
                    Ok(json!({"removed":true}))
                }
                Operator::Call { runtime_id, body } => authority.call(&runtime_id, body).await,
            }
        }
        .await,
    )
}
fn client(url: &str, ca: Option<&str>) -> Result<(reqwest::Client, url::Url)> {
    let url =
        url::Url::parse(url).map_err(|_| anyhow::anyhow!("Enter a valid HTTPS machine address"))?;
    ensure!(
        url.scheme() == "https"
            && url.host_str().is_some()
            && url.username().is_empty()
            && url.password().is_none()
            && url.path() == "/"
            && url.query().is_none()
            && url.fragment().is_none(),
        "Control address must be an HTTPS origin"
    );
    let mut builder = reqwest::Client::builder()
        .redirect(reqwest::redirect::Policy::none())
        .retry(reqwest::retry::never())
        .timeout(std::time::Duration::from_secs(10));
    if let Some(ca) = ca {
        builder = builder.add_root_certificate(reqwest::Certificate::from_pem(ca.as_bytes())?);
    }
    Ok((builder.build()?, url))
}
async fn bounded<T: serde::de::DeserializeOwned>(response: reqwest::Response) -> Result<T> {
    use futures_util::StreamExt;
    let status = response.status();
    let mut bytes = Vec::new();
    let mut stream = response.bytes_stream();
    while let Some(chunk) = stream.next().await {
        let chunk = chunk?;
        ensure!(
            bytes.len() + chunk.len() <= 1024 * 1024,
            "Control response exceeds protocol limit"
        );
        bytes.extend_from_slice(&chunk);
    }
    if !status.is_success() {
        // Diagnostics are not an authenticated operation receipt and never settle effects.
        let message = serde_json::from_slice::<Value>(&bytes)
            .ok()
            .and_then(|v| {
                v["error"]
                    .as_str()
                    .map(|s| s.chars().take(1024).collect::<String>())
            })
            .unwrap_or_else(|| "no diagnostic".into());
        anyhow::bail!("Control rejected request ({status}): {message}");
    }
    Ok(serde_json::from_slice(&bytes)?)
}
async fn preview(url: &str, ca: Option<&str>) -> Result<Identity> {
    let (client, url) = client(url, ca)?;
    let value: Value = bounded(client.get(url.join("control/v1/identity")?).send().await?).await?;
    ensure!(
        value["schema_version"] == VERSION,
        "Unsupported Control protocol"
    );
    let identity: Identity = serde_json::from_value(value["identity"].clone())?;
    identity.validate()?;
    Ok(identity)
}
impl Authority {
    pub(super) async fn join(&self, request: Join) -> Result<Value> {
        uuid::Uuid::parse_str(&request.request_id)?;
        let (client, url) = client(&request.url, request.ca_pem.as_deref())?;
        ensure!(!self.is_control(), "Control cannot join another Control");
        self.update(|book| {
            ensure!(
                book.control.as_ref().is_none_or(|c| c.identity.runtime_id
                    == request.expected_runtime_id
                    && c.identity.fingerprint == request.expected_fingerprint
                    && c.url == request.url
                    && c.ca_pem == request.ca_pem),
                "Already joined to a Control; leave explicitly first"
            );
            ensure!(
                book.pending
                    .values()
                    .all(|p| p.completed || p.request == request),
                "Unresolved join intent; retry original request"
            );
            if let Some(pending) = book.pending.get(&request.request_id) {
                ensure!(
                    pending.request == request,
                    "Connect identity conflicts with previous intent"
                );
            } else {
                book.pending.insert(
                    request.request_id.clone(),
                    Pending {
                        request: request.clone(),
                        completed: false,
                    },
                );
            }
            Ok(())
        })?;
        // Repeat completion never reenrolls or implicitly reconnects a disconnected control.
        {
            let book = self.book.lock().unwrap();
            if book.pending[&request.request_id].completed {
                return Ok(
                    json!({"identity":book.control.as_ref().filter(|c|c.identity.runtime_id==request.expected_runtime_id).ok_or_else(||anyhow::anyhow!("Completed control record missing"))?.identity}),
                );
            }
        }
        let identity = preview(&request.url, request.ca_pem.as_deref()).await?;
        ensure!(
            identity.runtime_id == request.expected_runtime_id
                && identity.fingerprint == request.expected_fingerprint,
            "Runtime identity differs from confirmed identity"
        );
        let enrollment = JoinRequest {
            schema_version: VERSION,
            os: std::env::consts::OS.into(),
            request_id: request.request_id.clone(),
            caller: self.identity.clone(),
            invitation: request.invitation.clone(),
        };
        let reply: Signed<Value> = bounded(
            client
                .post(url.join("control/v1/join")?)
                .json(&self.sign(enrollment.clone())?)
                .send()
                .await?,
        )
        .await?;
        identity.verify(&reply.payload, &reply.signature)?;
        ensure!(
            reply.payload["identity"] == json!(identity)
                && reply.payload["request_digest"] == protocol_digest(&enrollment)?,
            "Join response does not match request"
        );
        let token = reply.payload["token"]
            .as_str()
            .ok_or_else(|| anyhow::anyhow!("Missing control credential"))?
            .to_owned();
        ensure!(
            B64.decode(&token)?.len() == 32,
            "Invalid control credential"
        );
        self.update(|book| {
            ensure!(
                book.control.as_ref().is_none_or(|c| c.identity == identity),
                "Already joined to another Control"
            );
            book.control = Some(Binding {
                identity: identity.clone(),
                url: request.url.clone(),
                ca_pem: request.ca_pem.clone(),
                token,
            });
            let pending = book.pending.get_mut(&request.request_id).unwrap();
            pending.completed = true;
            // A completed intent needs no recoverable invitation.
            // Keep the original intent for exact idempotency, in this private store only.
            Ok(json!({"identity":identity}))
        })
    }
}
impl Authority {
    fn remove_member(&self, id: &str) -> Result<()> {
        self.update(|book| {
            let member = book
                .members
                .get_mut(id)
                .ok_or_else(|| anyhow::anyhow!("Member not found"))?;
            member.enabled = false;
            Ok(())
        })?;
        self.deliveries
            .lock()
            .unwrap()
            .retain(|_, d| d.member != id);
        self.changed.notify_waiters();
        Ok(())
    }
    async fn leave(&self) -> Result<()> {
        self.update(|book| {
            book.control = None;
            // Do not erase leases/receipts: unresolved effects remain fenced and inspectable.
            for lease in book.leases.values_mut() {
                lease.cancelled = true;
            }
            Ok(())
        })?;
        if let Some(task) = self.member_worker.lock().await.take() {
            task.abort();
            let _ = task.await;
        }
        Ok(())
    }
    async fn call(&self, member_id: &str, body: Rpc) -> Result<Value> {
        let member = self
            .book
            .lock()
            .unwrap()
            .members
            .get(member_id)
            .filter(|m| m.enabled)
            .cloned()
            .ok_or_else(|| anyhow::anyhow!("Machine is not joined to this Control"))?;
        let inventory = matches!(&body, Rpc::Inventory);
        let request = self.sign(Envelope {
            schema_version: VERSION,
            caller_id: self.identity.runtime_id.clone(),
            recipient_id: member_id.into(),
            issued_at_ms: now(),
            command_id: secret()?,
            body,
        })?;
        let key = protocol_digest(&request.payload)?;
        let (tx, rx) = tokio::sync::oneshot::channel();
        // One network delivery only. Timeout/lost reply must be reconciled using original durable receipt.
        self.deliveries.lock().unwrap().insert(
            key.clone(),
            Delivery {
                member: member_id.into(),
                request,
                reply: tx,
            },
        );
        self.queue.lock().unwrap().push_back(key.clone());
        let _delivery = PendingDelivery {
            authority: self,
            key: key.clone(),
        };
        self.changed.notify_waiters();
        let received = tokio::time::timeout(std::time::Duration::from_secs(8), rx).await;
        self.deliveries.lock().unwrap().remove(&key);
        self.queue.lock().unwrap().retain(|k| k != &key);
        let reply = received
            .map_err(|_| anyhow::anyhow!("Machine connection timed out; inspect original receipt"))?
            .map_err(|_| anyhow::anyhow!("Machine disconnected; inspect original receipt"))?;
        member.identity.verify(&reply.payload, &reply.signature)?;
        ensure!(
            reply.payload["request_digest"] == key,
            "Reply not bound to command"
        );
        let result = reply.payload["result"].clone();
        self.update(|book| {
            let current = book
                .members
                .get_mut(member_id)
                .ok_or_else(|| anyhow::anyhow!("Member removed"))?;
            ensure!(
                current.enabled
                    && current.identity == member.identity
                    && current.token == member.token,
                "Membership changed during request"
            );
            current.last_seen_ms = Some(now());
            if inventory && reply.payload["error"].is_null() {
                current.inventory = result.clone();
            }
            Ok(())
        })?;
        if let Some(error) = reply.payload["error"].as_str() {
            return Err(RemoteRejection(error.chars().take(1024).collect()).into());
        }
        Ok(result)
    }
    fn authenticate_poll(&self, token: &str, signed: &Signed<Poll>) -> Result<()> {
        let p = &signed.payload;
        ensure!(
            p.schema_version == VERSION
                && p.recipient_id == self.identity.runtime_id
                && p.issued_at_ms.abs_diff(now()) <= 30_000,
            "Poll recipient or deadline rejected"
        );
        ensure!(B64.decode(&p.nonce)?.len() == 32, "Invalid poll identity");
        let book = self.book.lock().unwrap();
        let member = book
            .members
            .get(&p.caller_id)
            .filter(|m| m.enabled)
            .ok_or_else(|| anyhow::anyhow!("Unknown member"))?;
        ensure!(
            token.len() == member.token.len()
                && openssl::memcmp::eq(token.as_bytes(), member.token.as_bytes()),
            "Member credential rejected"
        );
        member.identity.verify(p, &signed.signature)?;
        if let Some(reply) = &p.reply {
            member.identity.verify(&reply.payload, &reply.signature)?;
        }
        Ok(())
    }
    async fn next_command(&self, token: &str, request: Signed<Poll>) -> Result<Signed<Value>> {
        self.authenticate_poll(token, &request)?;
        let id = &request.payload.caller_id;
        if let Some(reply) = &request.payload.reply {
            let key = reply.payload["request_digest"]
                .as_str()
                .ok_or_else(|| anyhow::anyhow!("Missing reply binding"))?;
            let mut deliveries = self.deliveries.lock().unwrap();
            if let Some(delivery) = deliveries.get(key) {
                ensure!(delivery.member == *id, "Reply belongs to another machine");
            }
            if let Some(delivery) = deliveries.remove(key) {
                let _ = delivery.reply.send(reply.clone());
            }
        }
        self.update(|book| {
            book.members.get_mut(id).unwrap().last_seen_ms = Some(now());
            Ok(())
        })?;
        let until = tokio::time::Instant::now() + std::time::Duration::from_secs(1);
        loop {
            let notified = self.changed.notified();
            tokio::pin!(notified);
            notified.as_mut().enable();
            self.authenticate_poll(token, &request)?;
            let command = {
                let mut queue = self.queue.lock().unwrap();
                let deliveries = self.deliveries.lock().unwrap();
                let position = queue
                    .iter()
                    .position(|key| deliveries.get(key).is_some_and(|d| d.member == *id));
                position
                    .and_then(|i| queue.remove(i))
                    .and_then(|key| deliveries.get(&key).map(|d| d.request.clone()))
            };
            if command.is_some() || tokio::time::Instant::now() >= until {
                return self.sign(
                    json!({"request_digest":protocol_digest(&request.payload)?,"command":command}),
                );
            }
            let _ = tokio::time::timeout_at(until, notified).await;
        }
    }
    async fn start_member(self: &Arc<Self>, resources: Arc<Resources>) {
        let mut worker = self.member_worker.lock().await;
        if worker.as_ref().is_some_and(|w| !w.is_finished())
            || self.book.lock().unwrap().control.is_none()
        {
            return;
        }
        let authority = self.clone();
        *worker = Some(tokio::spawn(async move {
            let mut reply = None;
            loop {
                if authority.stopping.load(std::sync::atomic::Ordering::SeqCst) {
                    break;
                }
                let Some(binding) = authority.book.lock().unwrap().control.clone() else {
                    break;
                };
                let result = async {
                    let (client, url) = client(&binding.url, binding.ca_pem.as_deref())?;
                    let poll = authority.sign(Poll {
                        schema_version: VERSION,
                        caller_id: authority.identity.runtime_id.clone(),
                        recipient_id: binding.identity.runtime_id.clone(),
                        issued_at_ms: now(),
                        nonce: secret()?,
                        reply: reply.take(),
                    })?;
                    let response: Signed<Value> = bounded(
                        client
                            .post(url.join("control/v1/poll")?)
                            .bearer_auth(&binding.token)
                            .json(&poll)
                            .send()
                            .await?,
                    )
                    .await?;
                    binding
                        .identity
                        .verify(&response.payload, &response.signature)?;
                    ensure!(
                        response.payload["request_digest"] == protocol_digest(&poll.payload)?,
                        "Poll response identity mismatch"
                    );
                    if !response.payload["command"].is_null() {
                        let command: Signed<Envelope> =
                            serde_json::from_value(response.payload["command"].clone())?;
                        let control = authority.authorize(&binding.token, &command)?;
                        let key = protocol_digest(&command.payload)?;
                        let shared = match &command.payload.body {
                            Rpc::Acquire { target, .. } | Rpc::SandboxLifecycle { target, .. } | Rpc::ReadFile { target, .. } =>
                                matches!(target, Target::Sandbox { sandbox_id } if pot_sandbox::Manager::is_shared(sandbox_id)),
                            Rpc::LaunchTask { launch, .. } => matches!(&launch.execution_target.target,
                                Target::Sandbox { sandbox_id } if pot_sandbox::Manager::is_shared(sandbox_id)),
                            Rpc::ConfigureSandboxResources { .. } => true,
                            _ => false,
                        };
                        let host = resources.execution_host(shared).await?;
                        let result = authority
                            .dispatch_with_hub(
                                &host,
                                &control,
                                command.payload.body,
                                resources.assistant_hub().await,
                            )
                            .await;
                        reply = Some(authority.sign(match result {
                            Ok(value) => json!({"request_digest":key,"result":value}),
                            Err(error) => json!({"request_digest":key,"error":error.to_string()}),
                        })?);
                    }
                    Ok::<(), anyhow::Error>(())
                }
                .await;
                if result.is_err() {
                    tokio::time::sleep(std::time::Duration::from_millis(250)).await;
                }
            }
        }));
    }
}
async fn identity(State(resources): State<Arc<Resources>>, headers: HeaderMap) -> Response {
    if headers.contains_key("origin") {
        return StatusCode::FORBIDDEN.into_response();
    }
    Json(json!({"schema_version":VERSION,"identity":resources.control.identity,"role":"control"}))
        .into_response()
}
async fn register(
    State(resources): State<Arc<Resources>>,
    headers: HeaderMap,
    Json(request): Json<Signed<JoinRequest>>,
) -> Response {
    if headers.contains_key("origin") {
        return StatusCode::FORBIDDEN.into_response();
    }
    match resources.control.register(request) {
        Ok(reply) => Json(reply).into_response(),
        Err(_) => StatusCode::UNAUTHORIZED.into_response(),
    }
}
async fn poll(
    State(resources): State<Arc<Resources>>,
    headers: HeaderMap,
    Json(request): Json<Signed<Poll>>,
) -> Response {
    if headers.contains_key("origin") {
        return StatusCode::FORBIDDEN.into_response();
    }
    let token = headers
        .get("authorization")
        .and_then(|h| h.to_str().ok())
        .and_then(|h| h.strip_prefix("Bearer "))
        .unwrap_or("");
    match resources.control.next_command(token, request).await {
        Ok(reply) => Json(reply).into_response(),
        Err(_) => StatusCode::UNAUTHORIZED.into_response(),
    }
}
async fn require_target(host: &pot_control::host::Host, target: &Target) -> Result<()> {
    host.ensure_shared_target(target)?;
    let inventory = host.execution_target_inventory().await;
    ensure!(
        inventory["items"].as_array().is_some_and(|items| items
            .iter()
            .any(|item| item["execution_target"]["target"] == json!(target))),
        "Execution target resource is unavailable"
    );
    Ok(())
}
fn workspace_scope(lease: &Lease) -> Result<String> {
    digest(&(&lease.control_id, &lease.owner, &lease.target))
}
fn scope(lease: &Lease) -> Result<String> {
    digest(&(
        &lease.control_id,
        &lease.owner,
        &lease.run_id,
        lease.generation,
        &lease.target,
        &lease.id,
    ))
}
impl Authority {
    #[cfg(test)]
    async fn dispatch(
        self: &Arc<Self>,
        host: &pot_control::host::Host,
        control: &str,
        body: Rpc,
    ) -> Result<Value> {
        self.dispatch_with_hub(host, control, body, None).await
    }
    async fn dispatch_with_hub(
        self: &Arc<Self>,
        host: &pot_control::host::Host,
        control: &str,
        body: Rpc,
        hub: Option<Arc<pot_control::control::assistants::Hub>>,
    ) -> Result<Value> {
        let boot = host.describe()["boot_id"].as_str().unwrap().to_owned();
        match body {
            Rpc::LaunchTask { lease_id, launch } => {
                self.launch_task(host, control, lease_id, *launch).await
            }
            Rpc::InspectTask { task_id, run_id } => {
                self.inspect_task(host, control, &task_id, &run_id).await
            }
            Rpc::RecoverTaskLease { lease_id } => {
                self.recover_task_lease(host, control, &lease_id).await
            }
            Rpc::SandboxLifecycle {
                operation_id,
                accept_before_ms,
                target,
                args,
            } => {
                self.accept_management(
                    host,
                    control,
                    management::Request {
                        operation: operation_id,
                        accept_before_ms,
                        target,
                        args,
                    },
                    hub,
                )
                .await
            }
            Rpc::InspectManagement { operation_id } => {
                self.inspect_management(control, &operation_id, &boot)
            }
            Rpc::Resources {
                lease_id,
                resources,
            } => {
                let lease = self.lease(control, &lease_id, &boot, true)?;
                require_target(host, &lease.target).await?;
                let revision = resources.revision()?;
                self.update(|book| {
                    let accepted = book
                        .leases
                        .get_mut(&lease.id)
                        .ok_or_else(|| anyhow::anyhow!("Lease missing"))?;
                    ensure!(
                        accepted
                            .resources_revision
                            .as_ref()
                            .is_none_or(|old| old == &revision),
                        "Control resource configuration changed for an accepted run"
                    );
                    accepted.resources_revision = Some(revision);
                    Ok(())
                })?;
                host.install_controlled_resources(
                    &lease.target,
                    &workspace_scope(&lease)?,
                    resources,
                )
                .await
            }
            Rpc::Context { lease_id } => {
                let lease = self.lease(control, &lease_id, &boot, true)?;
                require_target(host, &lease.target).await?;
                host.controlled_context(&lease.target, &workspace_scope(&lease)?)
                    .await
            }
            Rpc::Prepare {
                lease_id,
                name,
                args,
            } => {
                let lease = self.lease(control, &lease_id, &boot, true)?;
                let authority = self.clone();
                let control = control.to_owned();
                let check_boot = boot.clone();
                let cancelled = Arc::new(move || {
                    authority
                        .lease(&control, &lease_id, &check_boot, true)
                        .is_err()
                });
                host.prepare_controlled(
                    pot_control::host::ControlOperation {
                        resources_revision: lease.resources_revision.clone(),
                        target: ExecutionTarget {
                            runtime_id: self.identity.runtime_id.clone(),
                            target: lease.target.clone(),
                        },
                        scope: scope(&lease)?,
                        workspace_scope: workspace_scope(&lease)?,
                        run_id: lease.run_id,
                        generation: lease.generation,
                        call_id: String::new(),
                        name,
                        args,
                    },
                    cancelled,
                )
                .await
            }
            Rpc::ReadFile {
                owner,
                target,
                query,
            } => {
                uuid::Uuid::parse_str(&owner)?;
                require_target(host, &target).await?;
                let workspace = digest(&(control, &owner, &target))?;
                Ok(serde_json::to_value(
                    host.controlled_file(&target, &workspace, query).await?,
                )?)
            }
            Rpc::Inventory => Ok(host.execution_target_inventory().await),
            Rpc::ConfigureSandboxResources { budget } => {
                host.configure_sandbox_resources(&budget).await?;
                Ok(json!({"ok":true}))
            }
            Rpc::Acquire {
                owner,
                run_id,
                generation,
                target,
            } => {
                let _guard = self.operations.lock().await;
                self.management_busy(&target)?;
                require_target(host, &target).await?;
                self.acquire(control, &owner, &run_id, generation, &target, &boot)
            }
            Rpc::Renew { lease_id } => self.renew(control, &lease_id, &boot),
            Rpc::Cancel { lease_id } => {
                let lease = self.cancel(control, &lease_id)?;
                host.cancel_controlled(&scope(&lease)?, &lease.target)
                    .await?;
                Ok(json!({"cancelled":true}))
            }
            Rpc::Inspect { lease_id, call_id } => {
                let lease = self.lease(control, &lease_id, &boot, false)?;
                let path = self.receipt_path(&lease, &call_id)?;
                if !path.exists() {
                    return Ok(json!({"state":"not_accepted"}));
                }
                let receipt: Receipt = serde_json::from_slice(&std::fs::read(&path)?)?;
                ensure!(
                    receipt.schema_version == VERSION,
                    "Unsupported operation receipt"
                );
                Ok(self.operation_result(&receipt, &path, &boot))
            }
            Rpc::Execute {
                lease_id,
                call_id,
                name,
                args,
            } => {
                let _guard = self.operations.lock().await;
                let lease = self.lease(control, &lease_id, &boot, false)?;
                let path = self.receipt_path(&lease, &call_id)?;
                let request =
                    json!({"lease_id":lease_id,"call_id":call_id,"name":name,"args":args});
                if path.exists() {
                    let receipt: Receipt = serde_json::from_slice(&std::fs::read(&path)?)?;
                    ensure!(
                        receipt.schema_version == VERSION && receipt.request == request,
                        "Operation identity conflicts with accepted request"
                    );
                    return Ok(self.operation_result(&receipt, &path, &boot));
                }
                self.lease(control, &lease_id, &boot, true)?;
                require_target(host, &lease.target).await?;
                // The destination validates named tools against the supplied Control snapshot.
                // Internal envelopes remain unavailable on this RPC.
                ensure!(
                    name != "plugin_call",
                    "Internal plugin envelopes cannot be called directly"
                );
                let execution_scope = scope(&lease)?;
                std::fs::create_dir_all(path.parent().unwrap())?;
                let receipt = Receipt {
                    schema_version: VERSION,
                    request,
                    boot_id: boot.clone(),
                    result: None,
                };
                private_json(&path, &receipt)?;
                let authority = self.clone();
                let host = host.clone();
                self.active.lock().unwrap().insert(path.clone());
                let active = ActiveOperation {
                    authority: self.clone(),
                    path: path.clone(),
                };
                let result = self.operation_result(&receipt, &path, &boot);
                let task = tokio::spawn(async move {
                    let _active = active;
                    let check = authority.clone();
                    let control = lease.control_id.clone();
                    let lease_id = lease.id.clone();
                    let execution_boot = boot.clone();
                    let cancelled: Arc<dyn Fn() -> bool + Send + Sync> = Arc::new(move || {
                        check
                            .lease(&control, &lease_id, &execution_boot, true)
                            .is_err()
                    });
                    // The monitor also drains yielded sessions after expiry, independently of new RPCs.
                    let monitor_host = host.clone();
                    let monitor_check = cancelled.clone();
                    let monitor_scope = execution_scope.clone();
                    let monitor_target = lease.target.clone();
                    let monitor = tokio::spawn(async move {
                        while !monitor_check() {
                            tokio::time::sleep(std::time::Duration::from_millis(100)).await;
                        }
                        if let Err(error) = monitor_host
                            .cancel_controlled(&monitor_scope, &monitor_target)
                            .await
                        {
                            eprintln!("Control scope cleanup failed: {error}");
                        }
                    });
                    authority.track(monitor);
                    let workspace = workspace_scope(&lease).unwrap();
                    let outcome = host
                        .execute_controlled(
                            pot_control::host::ControlOperation {
                                resources_revision: lease.resources_revision.clone(),
                                scope: execution_scope,
                                workspace_scope: workspace,
                                run_id: lease.run_id,
                                generation: lease.generation,
                                target: ExecutionTarget {
                                    runtime_id: authority.identity.runtime_id.clone(),
                                    target: lease.target,
                                },
                                call_id,
                                name,
                                args,
                            },
                            cancelled,
                        )
                        .await;
                    let mut receipt = receipt;
                    receipt.result = Some(match outcome {
                        Ok(value) => json!({"ok":true,"value":value}),
                        Err(error) => json!({"ok":false,"error":error.to_string()}),
                    });
                    if let Err(error) = private_json(&path, &receipt) {
                        eprintln!(
                            "Control receipt persistence failed; outcome remains uncertain: {error}"
                        );
                    }
                });
                self.track(task);
                Ok(result)
            }
        }
    }
}
struct ActiveOperation {
    authority: Arc<Authority>,
    path: PathBuf,
}
impl Drop for ActiveOperation {
    fn drop(&mut self) {
        self.authority.active.lock().unwrap().remove(&self.path);
    }
}
impl Authority {
    fn operation_result(&self, receipt: &Receipt, path: &Path, boot: &str) -> Value {
        json!({"state":if receipt.result.is_some(){"completed"}else if receipt.boot_id==boot && self.active.lock().unwrap().contains(path){"accepted"}else{"uncertain"},"result":receipt.result})
    }
}

impl Authority {
    pub(super) async fn restore_listener(
        self: &Arc<Self>,
        resources: Arc<Resources>,
    ) -> Result<()> {
        let path = self.data.join("control-listener.json");
        if path.exists() {
            let config: Option<Listen> = serde_json::from_slice(&std::fs::read(&path)?)?;
            if let Some(config) = config {
                self.start_listener(resources.clone(), config).await?;
            }
        }
        self.start_member(resources).await;
        Ok(())
    }
    async fn start_listener(
        self: &Arc<Self>,
        resources: Arc<Resources>,
        config: Listen,
    ) -> Result<()> {
        ensure!(
            self.control_allowed && self.book.lock().unwrap().control.is_none(),
            "Only standalone, unjoined Runtime may enable Control"
        );
        let mut guard = self.listener.lock().await;
        ensure!(
            guard.is_none(),
            "Control listener is already active; stop it before reconfiguration"
        );
        ensure!(
            config.certificate.is_absolute() && config.private_key.is_absolute(),
            "TLS files must use absolute paths"
        );
        let _ = rustls::crypto::ring::default_provider().install_default();
        let tls = axum_server::tls_rustls::RustlsConfig::from_pem_file(
            &config.certificate,
            &config.private_key,
        )
        .await?;
        let listener = std::net::TcpListener::bind(config.address)?;
        listener.set_nonblocking(true)?;
        let address = listener.local_addr()?;
        // Save the actual port, including when an operator requested port zero.
        private_json(
            &self.data.join("control-listener.json"),
            &Some(Listen { address, ..config }),
        )?;
        let client_token = self.client_token()?;
        let native_token = resources.host().await.endpoint(0).token;
        let client_routes = control_client_router(resources.router(), client_token, native_token);
        let handle = axum_server::Handle::new();
        let serving = handle.clone();
        let router = Router::new()
            .route("/control/v1/identity", get(identity))
            .route("/control/v1/join", axum::routing::post(register))
            .route("/control/v1/poll", axum::routing::post(poll))
            .route(
                "/control/v1/task-service",
                axum::routing::post(task_services::service)
                    .layer(axum::extract::DefaultBodyLimit::max(18 * 1024 * 1024)),
            )
            .layer(axum::extract::DefaultBodyLimit::max(1024 * 1024))
            .with_state(resources.clone())
            .merge(client_routes);
        let server = axum_server::from_tcp_rustls(listener, tls)?;
        let task = tokio::spawn(async move {
            if let Err(error) = server
                .handle(serving)
                .serve(router.into_make_service())
                .await
            {
                eprintln!("Control listener failed: {error}");
            }
        });
        *guard = Some((handle, task));
        Ok(())
    }
    pub(super) async fn stop_listener(&self, persist: bool) -> Result<()> {
        let mut guard = self.listener.lock().await;
        if persist {
            private_json(
                &self.data.join("control-listener.json"),
                &Option::<Listen>::None,
            )?;
        }
        if let Some((handle, mut task)) = guard.take() {
            handle.shutdown();
            if tokio::time::timeout(std::time::Duration::from_secs(2), &mut task)
                .await
                .is_err()
            {
                task.abort();
                let _ = task.await;
            }
        }
        Ok(())
    }
}

impl Authority {
    /// Failed refresh keeps only previously confirmed inventory; it never grants new authority.
    pub(super) async fn machines(&self) -> Vec<Value> {
        let control = self
            .book
            .lock()
            .unwrap()
            .members
            .values()
            .cloned()
            .collect::<Vec<_>>();
        futures_util::future::join_all(control.into_iter().map(|control|async move {
            let online=control.enabled && matches!(tokio::time::timeout(std::time::Duration::from_secs(2),self.call(&control.identity.runtime_id,Rpc::Inventory)).await,Ok(Ok(_)));
            let book=self.book.lock().unwrap();
            let current=book.members.get(&control.identity.runtime_id).unwrap_or(&control);
            json!({"runtime_id":current.identity.runtime_id,"identity":current.identity,"local":false,
                "connection":if online && current.enabled{"online"}else{"offline"},"enabled":current.enabled,"last_seen_ms":current.last_seen_ms,
                "tags":current.tags,"execution_targets":current.inventory["items"],"default":null})
        })).await
    }
}

impl Authority {
    fn track(&self, task: tokio::task::JoinHandle<()>) {
        let mut tasks = self.tasks.lock().unwrap();
        tasks.retain(|task| !task.is_finished());
        tasks.push(task);
    }
    pub(super) async fn stop_operations(&self) {
        self.stopping
            .store(true, std::sync::atomic::Ordering::SeqCst);
        if let Some(task) = self.member_worker.lock().await.take() {
            task.abort();
            let _ = task.await;
        }
        self.deliveries.lock().unwrap().clear();
        self.changed.notify_waiters();
        loop {
            let tasks = std::mem::take(&mut *self.tasks.lock().unwrap());
            if tasks.is_empty() {
                break;
            }
            for mut task in tasks {
                if tokio::time::timeout(std::time::Duration::from_secs(2), &mut task)
                    .await
                    .is_err()
                {
                    task.abort();
                    let _ = task.await;
                    eprintln!(
                        "Control operation shutdown timed out; inspect its durable receipt before any new action"
                    );
                }
            }
        }
    }
}
#[cfg(test)]
mod tests {
    use super::*;
    use tower::ServiceExt;
    #[tokio::test]
    async fn control_client_credential_survives_restart_without_local_authority() {
        let authority = authority();
        let token = authority.client_token().unwrap();
        let reopened = Authority::open(&authority.data, &authority.identity.runtime_id).unwrap();
        assert_eq!(token, reopened.client_token().unwrap());
        let local = axum::Router::new().route(
            "/api/v1/runtime",
            get(|headers: HeaderMap| async move {
                if headers.get("authorization").and_then(|h| h.to_str().ok())
                    == Some("Bearer second-boot")
                    && !headers.contains_key("origin")
                {
                    StatusCode::OK
                } else {
                    StatusCode::UNAUTHORIZED
                }
            }),
        );
        let remote = control_client_router(local.clone(), token.clone(), "second-boot".into());
        for (routes, credential, origin, status) in [
            (remote.clone(), token.as_str(), false, StatusCode::OK),
            (local, token.as_str(), false, StatusCode::UNAUTHORIZED),
            (
                remote.clone(),
                "first-boot",
                false,
                StatusCode::UNAUTHORIZED,
            ),
            (remote, token.as_str(), true, StatusCode::UNAUTHORIZED),
        ] {
            let mut req = axum::http::Request::builder()
                .uri("/api/v1/runtime")
                .header("authorization", format!("Bearer {credential}"));
            if origin {
                req = req.header("origin", "https://browser.example");
            }
            assert_eq!(
                routes
                    .oneshot(req.body(axum::body::Body::empty()).unwrap())
                    .await
                    .unwrap()
                    .status(),
                status
            );
        }
    }
    fn authority() -> Arc<Authority> {
        let path = std::env::temp_dir().join(format!("pot-control-{}", uuid::Uuid::new_v4()));
        std::fs::create_dir_all(&path).unwrap();
        Authority::open(&path, &uuid::Uuid::new_v4().to_string()).unwrap()
    }
    pub(super) fn enroll(control: &Authority, node: &Authority) -> String {
        let token = secret().unwrap();
        node.update(|b| {
            b.control = Some(Binding {
                identity: control.identity.clone(),
                token: token.clone(),
                url: "https://localhost/".into(),
                ca_pem: None,
            });
            Ok(())
        })
        .unwrap();
        token
    }
    pub(super) async fn join_nodes(
        control: &Arc<Resources>,
        node: &Arc<Resources>,
    ) -> (String, String) {
        let (url, ca) = setup_tls(control).await;
        let request = Join {
            request_id: uuid::Uuid::new_v4().to_string(),
            url: url.clone(),
            ca_pem: Some(ca.clone()),
            invitation: control.control.create_invitation().unwrap()["invitation"]
                .as_str()
                .unwrap()
                .into(),
            expected_runtime_id: control.control.identity.runtime_id.clone(),
            expected_fingerprint: control.control.identity.fingerprint.clone(),
        };
        node.control.join(request.clone()).await.unwrap();
        node.control.join(request).await.unwrap();
        node.control.start_member(node.clone()).await;
        (url, ca)
    }
    #[test]
    fn invitations_are_single_use_private_durable_and_never_restore_removed_members() {
        let control = authority();
        let node = authority();
        let stranger = authority();
        private_json(
            &control.data.join("control-listener.json"),
            &Some(Listen {
                address: "127.0.0.1:0".parse().unwrap(),
                certificate: control.data.join("cert.pem"),
                private_key: control.data.join("key.pem"),
            }),
        )
        .unwrap();
        let invitation = control.create_invitation().unwrap()["invitation"]
            .as_str()
            .unwrap()
            .to_owned();
        let request = JoinRequest {
            schema_version: VERSION,
            os: std::env::consts::OS.into(),
            request_id: uuid::Uuid::new_v4().to_string(),
            caller: node.identity.clone(),
            invitation: invitation.clone(),
        };
        let reply = control
            .register(node.sign(request.clone()).unwrap())
            .unwrap();
        assert_eq!(
            control.book.lock().unwrap().members[&node.identity.runtime_id].tags,
            vec![std::env::consts::OS]
        );
        assert!(control.local_tags().contains(&"task-runner".into()));
        control
            .set_tags(&control.identity.runtime_id, vec![])
            .unwrap();
        control
            .set_tags(&node.identity.runtime_id, vec!["desktop".into()])
            .unwrap();
        let restored = Authority::open(&control.data, &control.identity.runtime_id).unwrap();
        assert_eq!(
            restored
                .register(node.sign(request.clone()).unwrap())
                .unwrap()
                .payload,
            reply.payload
        );
        assert!(restored.local_tags().is_empty());
        assert_eq!(
            restored.book.lock().unwrap().members[&node.identity.runtime_id].tags,
            ["desktop"]
        );
        assert!(
            restored
                .set_tags(&node.identity.runtime_id, vec!["Bad Label".into()])
                .is_err()
        );
        assert!(!restored.metadata().to_string().contains(&invitation));
        let mut conflict = request.clone();
        conflict.caller = stranger.identity.clone();
        assert!(restored.register(stranger.sign(conflict).unwrap()).is_err());
        restored.remove_member(&node.identity.runtime_id).unwrap();
        assert!(restored.register(node.sign(request).unwrap()).is_err());
        let path = restored.data.join("runtime-control.json");
        std::fs::remove_file(&path).unwrap();
        std::fs::create_dir(&path).unwrap();
        let count = restored.book.lock().unwrap().codes.len();
        assert!(restored.create_invitation().is_err());
        assert_eq!(count, restored.book.lock().unwrap().codes.len());
        for r in [control, node, stranger] {
            std::fs::remove_dir_all(&r.data).unwrap();
        }
    }
    #[test]
    fn commands_are_bound_to_control_identity_credentials_recipient_and_time() {
        let control = authority();
        let node = authority();
        let other = authority();
        let token = enroll(&control, &node);
        let request = Envelope {
            schema_version: VERSION,
            caller_id: control.identity.runtime_id.clone(),
            recipient_id: node.identity.runtime_id.clone(),
            issued_at_ms: now(),
            command_id: secret().unwrap(),
            body: Rpc::Inventory,
        };
        assert!(
            node.authorize(&token, &control.sign(request.clone()).unwrap())
                .is_ok()
        );
        assert!(
            node.authorize("native-token", &control.sign(request.clone()).unwrap())
                .is_err()
        );
        assert!(
            node.authorize(&token, &other.sign(request.clone()).unwrap())
                .is_err()
        );
        let mut altered = control.sign(request.clone()).unwrap();
        altered.payload.body = Rpc::Cancel {
            lease_id: "other".into(),
        };
        assert!(node.authorize(&token, &altered).is_err());
        let mut expired = request.clone();
        expired.issued_at_ms -= 60_000;
        assert!(
            node.authorize(&token, &control.sign(expired).unwrap())
                .is_err()
        );
        let mut wrong = request;
        wrong.recipient_id = control.identity.runtime_id.clone();
        assert!(
            node.authorize(&token, &control.sign(wrong).unwrap())
                .is_err()
        );
        assert!(!node.metadata().to_string().contains(&token));
        for a in [control, node, other] {
            std::fs::remove_dir_all(&a.data).unwrap();
        }
    }
    #[tokio::test]
    async fn outward_global_resources_are_control_selected_and_lease_scoped() {
        use base64::Engine;
        use pot_control::remote_execution::{RemoteBinding, RemoteExecution};
        use pot_runner::resources::{GlobalResources, ResourceFile};
        let control = resources().await;
        let node = resources().await;
        native(
            &node,
            json!({"request_id":"global-host","resource":{"kind":"host"}}),
        )
        .await;
        join_nodes(&control, &node).await;
        control
            .control
            .call(&node.control.identity.runtime_id, Rpc::Inventory)
            .await
            .unwrap();
        let binding = RemoteBinding {
            owner: uuid::Uuid::new_v4().to_string(),
            run_id: uuid::Uuid::new_v4().to_string(),
            generation: 1,
            execution_target: ExecutionTarget::host(&node.control.identity.runtime_id),
        };
        let context = control.control.open(binding.clone()).await.unwrap();
        let workspace = PathBuf::from(context["workspace"].as_str().unwrap());
        std::fs::create_dir_all(workspace.join(".agents/skills/project-only")).unwrap();
        std::fs::write(
            workspace.join(".agents/skills/project-only/SKILL.md"),
            "---\nname: project-only\ndescription: project skill\n---\n",
        )
        .unwrap();
        std::fs::write(workspace.join("AGENTS.md"), "PROJECT_ONLY_RULE").unwrap();
        let mut globals = GlobalResources::default();
        globals.files.insert(
            "skills/user/control-only/SKILL.md".into(),
            ResourceFile {
                data: base64::engine::general_purpose::STANDARD
                    .encode("---\nname: control-only\ndescription: control skill\n---\n"),
                executable: false,
            },
        );
        let installed = control
            .control
            .install_resources(binding.clone(), globals.clone())
            .await
            .unwrap();
        assert!(
            installed["instructions"]
                .as_str()
                .unwrap()
                .contains("PROJECT_ONLY_RULE")
        );
        let skills = installed["skills"].as_array().unwrap();
        assert!(skills.iter().any(|s| s["name"] == "control-only"));
        assert!(skills.iter().any(|s| s["name"] == "project-only"));
        assert_eq!(
            installed["global_resources_revision"],
            globals.revision().unwrap()
        );
        assert_eq!(
            installed,
            control
                .control
                .install_resources(binding.clone(), globals.clone())
                .await
                .unwrap()
        );
        RemoteExecution::cancel(&*control.control, binding.clone())
            .await
            .unwrap();
        assert!(
            control
                .control
                .install_resources(binding, globals)
                .await
                .is_err()
        );
        for n in [control, node] {
            n.control.stop_listener(false).await.unwrap();
            std::fs::remove_dir_all(n.control.data.parent().unwrap()).unwrap();
        }
    }
    #[tokio::test]
    async fn outward_plugins_use_control_bundle_and_scoped_sessions() {
        use pot_control::remote_execution::{RemoteBinding, RemoteExecution};
        use pot_runner::resources::GlobalResources;
        let control = resources().await;
        let node = resources().await;
        native(
            &node,
            json!({"request_id":"plugin-host","resource":{"kind":"host"}}),
        )
        .await;
        join_nodes(&control, &node).await;
        control
            .control
            .call(&node.control.identity.runtime_id, Rpc::Inventory)
            .await
            .unwrap();
        let binding = RemoteBinding {
            owner: uuid::Uuid::new_v4().to_string(),
            run_id: uuid::Uuid::new_v4().to_string(),
            generation: 1,
            execution_target: ExecutionTarget::host(&node.control.identity.runtime_id),
        };
        let context = control.control.open(binding.clone()).await.unwrap();
        let mut bundle = pot_core::plugins::PluginBundle::builtin("computer-use").unwrap();
        bundle.manifest.requirements = vec!["node".into()];
        bundle.files.insert("index.mjs".into(), "export function createPlugin({emit,mode}) { let n=0; return {async execute(){emit({type:'text',text:mode+':'+String(++n)});},async dispose(){}}; }".into());
        bundle.digest = bundle.hash().unwrap();
        let mut globals = GlobalResources {
            vision: true,
            ..Default::default()
        };
        globals.shell.default_timeout_ms = 4321;
        globals.plugins.insert(
            "computer-use".into(),
            pot_core::plugins::PluginConfig {
                enabled: true,
                source: None,
                bundle: Some(bundle.clone()),
            },
        );
        control
            .control
            .install_resources(binding.clone(), globals.clone())
            .await
            .unwrap();
        let command = control
            .control
            .prepare(
                binding.clone(),
                "exec_command".into(),
                json!({"cmd":"true"}),
            )
            .await
            .unwrap();
        assert_eq!(command["timeout_ms"], 4321);
        let args = json!({"code":"ignored"});
        assert_eq!(
            control
                .control
                .prepare(binding.clone(), "cua_repl".into(), args.clone())
                .await
                .unwrap(),
            args
        );
        let first = control
            .control
            .execute(
                binding.clone(),
                "plugin-first".into(),
                "cua_repl".into(),
                args.clone(),
            )
            .await
            .unwrap();
        assert_eq!(first["result"]["content"][0]["text"], "mixed:1");
        assert_eq!(
            first,
            control
                .control
                .execute(
                    binding.clone(),
                    "plugin-first".into(),
                    "cua_repl".into(),
                    args
                )
                .await
                .unwrap()
        );
        let continuation = json!({"session_id":first["result"]["session_id"],"code":"ignored"});
        let next = control
            .control
            .execute(
                binding.clone(),
                "plugin-next".into(),
                "cua_repl".into(),
                continuation.clone(),
            )
            .await
            .unwrap();
        assert_eq!(next["result"]["content"][0]["text"], "mixed:2");
        let mut other = binding.clone();
        other.owner = uuid::Uuid::new_v4().to_string();
        other.run_id = uuid::Uuid::new_v4().to_string();
        control.control.open(other.clone()).await.unwrap();
        control
            .control
            .install_resources(other.clone(), globals.clone())
            .await
            .unwrap();
        let foreign = control
            .control
            .execute(
                other.clone(),
                "foreign-session".into(),
                "cua_repl".into(),
                continuation,
            )
            .await
            .unwrap_err();
        assert!(
            foreign.to_string().contains("session expired"),
            "{foreign:#}"
        );
        assert!(
            control
                .control
                .prepare(binding.clone(), "plugin_call".into(), json!({}))
                .await
                .unwrap_err()
                .to_string()
                .contains("Internal plugin")
        );
        // An accepted run cannot adopt a new Control configuration.
        let mut changed = globals.clone();
        changed.vision = false;
        assert!(
            control
                .control
                .install_resources(binding.clone(), changed)
                .await
                .unwrap_err()
                .to_string()
                .contains("configuration changed")
        );
        // Even a different internally valid cache is not the Control-bound version.
        let mut forged = globals.clone();
        forged.vision = false;
        pot_control::control_resources::install(
            &PathBuf::from(context["workspace"].as_str().unwrap()),
            &forged,
        )
        .unwrap();
        assert!(
            control
                .control
                .prepare(
                    binding.clone(),
                    "cua_repl".into(),
                    json!({"code":"ignored"})
                )
                .await
                .unwrap_err()
                .to_string()
                .contains("snapshot binding changed")
        );
        control
            .control
            .install_resources(binding.clone(), globals.clone())
            .await
            .unwrap();
        RemoteExecution::cancel(&*control.control, other.clone())
            .await
            .unwrap();
        other.owner = uuid::Uuid::new_v4().to_string();
        other.run_id = uuid::Uuid::new_v4().to_string();
        control.control.open(other.clone()).await.unwrap();
        // No node builtin fallback when Control has disabled this package.
        globals.plugins.get_mut("computer-use").unwrap().enabled = false;
        control
            .control
            .install_resources(other.clone(), globals.clone())
            .await
            .unwrap();
        assert!(
            control
                .control
                .prepare(other.clone(), "cua_repl".into(), json!({"code":"ignored"}))
                .await
                .unwrap_err()
                .to_string()
                .contains("Unknown tool")
        );
        RemoteExecution::cancel(&*control.control, other.clone())
            .await
            .unwrap();
        other.owner = uuid::Uuid::new_v4().to_string();
        other.run_id = uuid::Uuid::new_v4().to_string();
        control.control.open(other.clone()).await.unwrap();
        globals.plugins.get_mut("computer-use").unwrap().enabled = true;
        bundle
            .manifest
            .requirements
            .push("unsupported-dependency".into());
        bundle.digest = bundle.hash().unwrap();
        globals.plugins.get_mut("computer-use").unwrap().bundle = Some(bundle.clone());
        control
            .control
            .install_resources(other.clone(), globals.clone())
            .await
            .unwrap();
        assert!(
            control
                .control
                .prepare(other.clone(), "cua_repl".into(), json!({"code":"ignored"}))
                .await
                .unwrap_err()
                .to_string()
                .contains("Unsupported Host plugin dependency")
        );
        RemoteExecution::cancel(&*control.control, other.clone())
            .await
            .unwrap();
        other.owner = uuid::Uuid::new_v4().to_string();
        other.run_id = uuid::Uuid::new_v4().to_string();
        control.control.open(other.clone()).await.unwrap();
        bundle.manifest.requirements = vec!["node".into()];
        bundle.manifest.execution = "sandbox".into();
        bundle.digest = bundle.hash().unwrap();
        globals.plugins.get_mut("computer-use").unwrap().bundle = Some(bundle);
        control
            .control
            .install_resources(other.clone(), globals)
            .await
            .unwrap();
        assert!(
            control
                .control
                .prepare(other.clone(), "cua_repl".into(), json!({"code":"ignored"}))
                .await
                .unwrap_err()
                .to_string()
                .contains("Sandbox execution only")
        );
        RemoteExecution::cancel(&*control.control, binding.clone())
            .await
            .unwrap();
        assert!(
            control
                .control
                .execute(
                    binding,
                    "revoked".into(),
                    "cua_repl".into(),
                    json!({"code":"ignored"})
                )
                .await
                .is_err()
        );
        RemoteExecution::cancel(&*control.control, other)
            .await
            .unwrap();
        for n in [control, node] {
            n.control.stop_listener(false).await.unwrap();
            std::fs::remove_dir_all(n.control.data.parent().unwrap()).unwrap();
        }
    }
    #[tokio::test]
    async fn outward_file_download_survives_finished_leases_and_is_scoped() {
        use pot_control::remote_execution::RemoteExecution;
        let control = resources().await;
        let node = resources().await;
        native(
            &node,
            json!({"request_id":"files-host","resource":{"kind":"host"}}),
        )
        .await;
        join_nodes(&control, &node).await;
        control
            .control
            .call(&node.control.identity.runtime_id, Rpc::Inventory)
            .await
            .unwrap();
        let owner = uuid::Uuid::new_v4().to_string();
        let target = Target::Host {};
        let execution_target = ExecutionTarget {
            runtime_id: node.control.identity.runtime_id.clone(),
            target: target.clone(),
        };
        let workspace = node
            .control
            .data
            .join("execution-workspaces")
            .join(digest(&(&control.control.identity.runtime_id, &owner, &target)).unwrap());
        std::fs::create_dir_all(&workspace).unwrap();
        let bytes: Vec<_> = (0..pot_control::files::CHUNK_BYTES + 29)
            .map(|n| n as u8)
            .collect();
        std::fs::write(workspace.join("report.bin"), &bytes).unwrap();
        // No active execution lease is required to fetch a completed task's files.
        let mut query = pot_protocol::FileQuery {
            path: "report.bin".into(),
            ..Default::default()
        };
        let first = control
            .control
            .read_file(execution_target.clone(), owner.clone(), query.clone())
            .await
            .unwrap();
        let mut actual = base64::engine::general_purpose::STANDARD
            .decode(&first.data)
            .unwrap();
        query.offset = actual.len() as u64;
        query.revision = first.revision;
        let last = control
            .control
            .read_file(execution_target.clone(), owner.clone(), query.clone())
            .await
            .unwrap();
        actual.extend(
            base64::engine::general_purpose::STANDARD
                .decode(last.data)
                .unwrap(),
        );
        assert_eq!(actual, bytes);
        query.offset = 0;
        assert!(
            control
                .control
                .read_file(
                    execution_target.clone(),
                    uuid::Uuid::new_v4().to_string(),
                    query.clone()
                )
                .await
                .is_err()
        );
        query.path = "../report.bin".into();
        assert!(
            control
                .control
                .read_file(execution_target.clone(), owner.clone(), query.clone())
                .await
                .is_err()
        );
        query.path = "report.bin".into();
        std::fs::write(workspace.join("report.bin"), "changed").unwrap();
        assert!(
            control
                .control
                .read_file(execution_target.clone(), owner.clone(), query.clone())
                .await
                .is_err()
        );
        control
            .control
            .remove_member(&execution_target.runtime_id)
            .unwrap();
        assert!(
            control
                .control
                .read_file(execution_target, owner, query)
                .await
                .is_err()
        );
        for r in [control, node] {
            r.shutdown().await;
            std::fs::remove_dir_all(r.control.data.parent().unwrap()).unwrap();
        }
    }

    #[tokio::test]
    async fn outward_membership_single_control_full_inventory_and_removal() {
        let control = resources().await;
        let node = resources().await;
        let other = resources().await;
        native(
            &node,
            json!({"request_id":"host","resource":{"kind":"host"}}),
        )
        .await;
        let (url, ca) = join_nodes(&control, &node).await;
        let member = &node.control.identity.runtime_id;
        assert_eq!(
            control.control.call(member, Rpc::Inventory).await.unwrap()["items"]
                .as_array()
                .unwrap()
                .len(),
            1
        );
        control.control.set_tags(member, vec![]).unwrap();
        control.control.call(member, Rpc::Inventory).await.unwrap();
        assert_eq!(control.control.machines().await[0]["tags"], json!([]));
        let reopened =
            Authority::open(&control.control.data, &control.control.identity.runtime_id).unwrap();
        assert!(
            reopened.book.lock().unwrap().members[member]
                .tags
                .is_empty()
        );
        assert!(
            node.control.listener.lock().await.is_none(),
            "Execution node must not open an inbound command listener"
        );
        assert!(!control.control.metadata().to_string().contains("grant"));
        assert_eq!(
            node.control.metadata()["control"]["identity"],
            json!(control.control.identity)
        );
        let (url2, ca2) = setup_tls(&other).await;
        let request = Join {
            request_id: uuid::Uuid::new_v4().to_string(),
            url: url2,
            ca_pem: Some(ca2),
            invitation: other.control.create_invitation().unwrap()["invitation"]
                .as_str()
                .unwrap()
                .into(),
            expected_runtime_id: other.control.identity.runtime_id.clone(),
            expected_fingerprint: other.control.identity.fingerprint.clone(),
        };
        assert!(node.control.join(request).await.is_err());
        let lease = control
            .control
            .call(
                member,
                Rpc::Acquire {
                    owner: "a".into(),
                    run_id: uuid::Uuid::new_v4().to_string(),
                    generation: 1,
                    target: Target::Host {},
                },
            )
            .await
            .unwrap();
        let id = lease["id"].as_str().unwrap();
        let boot = lease["boot_id"].as_str().unwrap();
        assert!(
            node.control
                .lease(&control.control.identity.runtime_id, id, "next-boot", true)
                .is_err()
        );
        node.control.leave().await.unwrap();
        assert!(
            node.control
                .lease(&control.control.identity.runtime_id, id, boot, true)
                .is_err()
        );
        assert!(
            node.control
                .lease(&control.control.identity.runtime_id, id, boot, false)
                .is_ok()
        );
        control.control.remove_member(member).unwrap();
        assert!(control.control.call(member, Rpc::Inventory).await.is_err());
        let client = client(&url, Some(&ca)).unwrap().0;
        assert_eq!(
            client
                .post(format!("{url}/peer/v1/rpc"))
                .send()
                .await
                .unwrap()
                .status(),
            reqwest::StatusCode::NOT_FOUND
        );
        for r in [control, node, other] {
            r.shutdown().await;
            std::fs::remove_dir_all(r.control.data.parent().unwrap()).unwrap();
        }
    }
    #[tokio::test]
    async fn embedded_control_is_rejected_before_listener_restore() {
        let control = resources().await;
        setup_tls(&control).await;
        let blocked = Authority::open_policy(
            &control.control.data,
            &control.control.identity.runtime_id,
            false,
        )
        .unwrap();
        assert!(blocked.restore_listener(control.clone()).await.is_err());
        assert!(blocked.listener.lock().await.is_none());
        control.shutdown().await;
        std::fs::remove_dir_all(control.control.data.parent().unwrap()).unwrap();
    }
    #[test]
    fn signatures_and_digests_ignore_nested_json_object_order() {
        let root = std::env::temp_dir().join(format!("pot-canonical-{}", uuid::Uuid::new_v4()));
        std::fs::create_dir_all(&root).unwrap();
        let authority = Authority::open(&root, &uuid::Uuid::new_v4().to_string()).unwrap();
        let first: Value =
            serde_json::from_str(r#"{"z":{"b":2,"a":1},"a":[{"y":4,"x":3}]}"#).unwrap();
        let second: Value =
            serde_json::from_str(r#"{"a":[{"x":3,"y":4}],"z":{"a":1,"b":2}}"#).unwrap();
        assert_eq!(
            canonical_bytes(&first).unwrap(),
            br#"{"a":[{"x":3,"y":4}],"z":{"a":1,"b":2}}"#
        );
        assert_eq!(
            protocol_digest(&first).unwrap(),
            protocol_digest(&second).unwrap()
        );
        let signed = authority.sign(first).unwrap();
        authority
            .identity
            .verify(&second, &signed.signature)
            .unwrap();
        assert!(
            authority
                .identity
                .verify(&json!({"a":[],"z":{}}), &signed.signature)
                .is_err()
        );
        std::fs::remove_dir_all(root).unwrap();
    }
    #[tokio::test]
    async fn runner_membership_and_assistant_ownership_are_mutually_exclusive() {
        let center = resources().await;
        let runner = resources().await;
        enroll(&center.control, &runner.control);
        let reply = native(&runner, json!({"request_id":"forbidden-assistant","resource":{"kind":"assistant","name":"Wrong owner"}})).await;
        assert_eq!(reply.status(), StatusCode::CONFLICT);
        assert!(runner.assistant_hub().await.is_none());
        assert!(
            !runner
                .host()
                .await
                .data_dir()
                .join("control.sqlite")
                .exists()
        );
        assert!(
            !runner
                .host()
                .await
                .data_dir()
                .join("pot-v2.sqlite")
                .exists()
        );
        let local = resources().await;
        assert_eq!(native(&local, json!({"request_id":"local-assistant","resource":{"kind":"assistant","name":"Local"}})).await.status(), StatusCode::OK);
        let result = local
            .join_control(Join {
                request_id: uuid::Uuid::new_v4().to_string(),
                url: "https://127.0.0.1:1/".into(),
                invitation: "unused".into(),
                expected_runtime_id: center.control.identity.runtime_id.clone(),
                expected_fingerprint: center.control.identity.fingerprint.clone(),
                ca_pem: None,
            })
            .await;
        assert!(result.unwrap_err().to_string().contains("cannot join"));
        assert!(!local.control.is_member());
        local.shutdown().await;
        runner.shutdown().await;
        center.shutdown().await;
    }
    pub(super) async fn resources() -> Arc<Resources> {
        let root =
            std::env::temp_dir().join(format!("pot-control-runtime-{}", uuid::Uuid::new_v4()));
        let config = pot_runtime::RuntimeConfig {
            data: root.join("data"),
            workspace: root.join("workspace"),
        };
        let backend = pot_runtime::initialize(config.clone()).await.unwrap();
        Resources::open(backend, config).await.unwrap()
    }
    pub(super) async fn native(
        resources: &Arc<Resources>,
        body: Value,
    ) -> axum::response::Response {
        let token = resources.host().await.endpoint(0).token;
        resources
            .router()
            .oneshot(
                axum::http::Request::builder()
                    .method("POST")
                    .uri("/api/v1/runtime/resources")
                    .header("authorization", format!("Bearer {token}"))
                    .header("content-type", "application/json")
                    .body(axum::body::Body::from(body.to_string()))
                    .unwrap(),
            )
            .await
            .unwrap()
    }
    pub(super) async fn setup_tls(resources: &Arc<Resources>) -> (String, String) {
        let rcgen::CertifiedKey { cert, signing_key } =
            rcgen::generate_simple_self_signed(vec!["localhost".into()]).unwrap();
        let certificate = resources.control.data.join("cert.pem");
        let private_key = resources.control.data.join("key.pem");
        std::fs::write(&certificate, cert.pem()).unwrap();
        std::fs::write(&private_key, signing_key.serialize_pem()).unwrap();
        resources
            .control
            .start_listener(
                resources.clone(),
                Listen {
                    address: "127.0.0.1:0".parse().unwrap(),
                    certificate,
                    private_key,
                },
            )
            .await
            .unwrap();
        let config: Option<Listen> = serde_json::from_slice(
            &std::fs::read(resources.control.data.join("control-listener.json")).unwrap(),
        )
        .unwrap();
        (
            format!("https://localhost:{}", config.unwrap().address.port()),
            cert.pem(),
        )
    }
}
#[cfg(test)]
mod recovery_tests {
    use super::tests::{enroll, native, resources};
    use super::*;
    async fn completed(
        authority: &Arc<Authority>,
        host: &pot_control::host::Host,
        control: &str,
        lease: &str,
        call: &str,
    ) -> Value {
        tokio::time::timeout(std::time::Duration::from_secs(10), async {
            loop {
                let value = authority
                    .dispatch(
                        host,
                        control,
                        Rpc::Inspect {
                            lease_id: lease.into(),
                            call_id: call.into(),
                        },
                    )
                    .await
                    .unwrap();
                if value["state"] == "completed" {
                    break value;
                }
                tokio::time::sleep(std::time::Duration::from_millis(20)).await;
            }
        })
        .await
        .unwrap()
    }
    #[tokio::test]
    async fn restart_quarantines_unsettled_receipt_without_reexecuting() {
        let caller = resources().await;
        let target = resources().await;
        native(
            &target,
            json!({"request_id":"host","resource":{"kind":"host"}}),
        )
        .await;
        enroll(&caller.control, &target.control);
        let control = &caller.control.identity.runtime_id;
        let host = target.host().await;
        let boot = host.describe()["boot_id"].as_str().unwrap().to_owned();
        let value = target
            .control
            .acquire(
                control,
                "a",
                &uuid::Uuid::new_v4().to_string(),
                1,
                &Target::Host {},
                &boot,
            )
            .unwrap();
        let id = value["id"].as_str().unwrap();
        let lease = target.control.lease(control, id, &boot, false).unwrap();
        let args = json!({"cmd":"touch replay-forbidden"});
        let call = "uncertain";
        let path = target.control.receipt_path(&lease, call).unwrap();
        std::fs::create_dir_all(path.parent().unwrap()).unwrap();
        private_json(
            &path,
            &Receipt {
                schema_version: VERSION,
                request: json!({"lease_id":id,"call_id":call,"name":"exec_command","args":args}),
                boot_id: boot,
                result: None,
            },
        )
        .unwrap();
        let config = pot_runtime::RuntimeConfig {
            data: target.control.data.clone(),
            workspace: target.control.data.parent().unwrap().join("workspace"),
        };
        target.shutdown().await;
        drop(host);
        drop(target);
        let backend = pot_runtime::initialize(config.clone()).await.unwrap();
        let restarted = Resources::open(backend, config).await.unwrap();
        let host = restarted.host().await;
        let result = restarted
            .control
            .dispatch(
                &host,
                control,
                Rpc::Execute {
                    lease_id: id.into(),
                    call_id: call.into(),
                    name: "exec_command".into(),
                    args,
                },
            )
            .await
            .unwrap();
        assert_eq!(result["state"], "uncertain");
        assert!(
            restarted
                .control
                .dispatch(
                    &host,
                    control,
                    Rpc::Renew {
                        lease_id: id.into()
                    }
                )
                .await
                .is_err()
        );
        assert!(
            !restarted
                .control
                .data
                .join("execution-workspaces")
                .join(workspace_scope(&lease).unwrap())
                .exists()
        );
        restarted.shutdown().await;
        caller.shutdown().await;
    }
    #[tokio::test]
    async fn expiry_drains_yielded_session_and_leaves_other_scope_alive() {
        let caller = resources().await;
        let target = resources().await;
        native(
            &target,
            json!({"request_id":"host","resource":{"kind":"host"}}),
        )
        .await;
        enroll(&caller.control, &target.control);
        let control = &caller.control.identity.runtime_id;
        let host = target.host().await;
        let boot = host.describe()["boot_id"].as_str().unwrap().to_owned();
        let mut leases = vec![];
        let mut results = vec![];
        for owner in ["expire", "alive"] {
            let lease = target
                .control
                .acquire(
                    control,
                    owner,
                    &uuid::Uuid::new_v4().to_string(),
                    1,
                    &Target::Host {},
                    &boot,
                )
                .unwrap();
            let id = lease["id"].as_str().unwrap().to_owned();
            target
                .control
                .dispatch(
                    &host,
                    control,
                    Rpc::Execute {
                        lease_id: id.clone(),
                        call_id: "start".into(),
                        name: "exec_command".into(),
                        args: json!({"cmd":"echo $$ > pid; exec sleep 60","yield_time_ms":1}),
                    },
                )
                .await
                .unwrap();
            let result = completed(&target.control, &host, control, &id, "start").await;
            assert_eq!(result["result"]["ok"], true, "{result}");
            results.push(result);
            leases.push(id);
        }
        let expired = target
            .control
            .lease(control, &leases[0], &boot, false)
            .unwrap();
        let pid: i32 = std::fs::read_to_string(
            target
                .control
                .data
                .join("execution-workspaces")
                .join(workspace_scope(&expired).unwrap())
                .join("pid"),
        )
        .unwrap()
        .trim()
        .parse()
        .unwrap();
        assert_eq!(unsafe { libc::kill(pid, 0) }, 0);
        target
            .control
            .update(|book| {
                book.leases.get_mut(&leases[0]).unwrap().expires_at_ms = now() - 1;
                Ok(())
            })
            .unwrap();
        tokio::time::timeout(std::time::Duration::from_secs(5), async {
            while unsafe { libc::kill(pid, 0) } == 0 {
                tokio::time::sleep(std::time::Duration::from_millis(20)).await;
            }
        })
        .await
        .unwrap();
        let session = results[1]["result"]["value"]["result"]["session_id"].clone();
        assert!(session.is_string(), "{}", results[1]);
        target
            .control
            .dispatch(
                &host,
                control,
                Rpc::Execute {
                    lease_id: leases[1].clone(),
                    call_id: "poll".into(),
                    name: "write_stdin".into(),
                    args: json!({"session_id":session,"yield_time_ms":1}),
                },
            )
            .await
            .unwrap();
        let result = completed(&target.control, &host, control, &leases[1], "poll").await;
        assert_eq!(result["result"]["ok"], true, "{result}");
        let foreign = results[0]["result"]["value"]["result"]["session_id"].clone();
        target
            .control
            .dispatch(
                &host,
                control,
                Rpc::Execute {
                    lease_id: leases[1].clone(),
                    call_id: "foreign".into(),
                    name: "write_stdin".into(),
                    args: json!({"session_id":foreign,"yield_time_ms":1}),
                },
            )
            .await
            .unwrap();
        let result = completed(&target.control, &host, control, &leases[1], "foreign").await;
        assert_eq!(result["result"]["ok"], false);
        target
            .control
            .dispatch(
                &host,
                control,
                Rpc::Cancel {
                    lease_id: leases[1].clone(),
                },
            )
            .await
            .unwrap();
        target.shutdown().await;
        caller.shutdown().await;
    }
}

struct PendingDelivery<'a> {
    authority: &'a Authority,
    key: String,
}
impl Drop for PendingDelivery<'_> {
    fn drop(&mut self) {
        self.authority.deliveries.lock().unwrap().remove(&self.key);
        self.authority
            .queue
            .lock()
            .unwrap()
            .retain(|k| k != &self.key);
    }
}
