//! Owner approval transport. Only current permanent grants are persisted;
//! pending/one-time decisions are memory-only. Policy lives in pot-agent.
use super::*;
use pot_agent::permissions::{Config, Grant, Request, Scope};
use serde::Deserialize;
use std::{
    io::Write,
    os::{fd::AsRawFd, unix::fs::OpenOptionsExt},
};

#[derive(Clone)]
struct Pending {
    id: String,
    request: Request,
    phase: String,
    decision: String,
}
#[derive(Clone, Default)]
struct Book {
    requests: Vec<Pending>,
    grants: Vec<Grant>,
    disk: Option<Vec<u8>>,
}
pub(crate) struct Hub {
    book: Mutex<Book>,
    path: Option<PathBuf>,
    changed: tokio::sync::Notify,
}
fn read(path: &std::path::Path) -> Result<Option<Vec<u8>>> {
    match std::fs::read(path) {
        Ok(bytes) => Ok(Some(bytes)),
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => Ok(None),
        Err(e) => Err(e.into()),
    }
}
impl Hub {
    #[cfg(test)]
    pub fn memory() -> Self {
        Self {
            book: Mutex::new(Book::default()),
            path: None,
            changed: Default::default(),
        }
    }
    pub fn open(path: &std::path::Path) -> Result<Self> {
        let disk = read(path)?;
        let config: Config = disk
            .as_ref()
            .map(|bytes| serde_json::from_slice(bytes))
            .transpose()?
            .unwrap_or_default();
        let grants = config.into_grants()?;
        let hub = Self {
            book: Mutex::new(Book {
                grants,
                disk,
                ..Book::default()
            }),
            path: Some(path.into()),
            changed: Default::default(),
        };
        // Absence means no grants. Do not create or rewrite user configuration on startup.
        Ok(hub)
    }
    pub fn open_with_legacy(path: &std::path::Path, legacy: &std::path::Path) -> Result<Self> {
        // A manually supplied JSON file is authoritative. Import only when absent.
        if read(path)?.is_some() || !legacy.exists() {
            return Self::open(path);
        }
        use rusqlite::OptionalExtension;
        let connection = rusqlite::Connection::open_with_flags(
            legacy,
            rusqlite::OpenFlags::SQLITE_OPEN_READ_ONLY,
        )?;
        let value: Option<String> = connection
            .query_row(
                "SELECT value FROM metadata WHERE key='permissions.v1'",
                [],
                |r| r.get(0),
            )
            .optional()?;
        let mut grants: Vec<Grant> = vec![];
        if let Some(value) = value {
            let book: Value = serde_json::from_str(&value)?;
            let entries = book["grants"]
                .as_array()
                .ok_or_else(|| anyhow!("Invalid legacy permissions"))?;
            for entry in entries {
                let mut entry = entry.clone();
                entry
                    .as_object_mut()
                    .ok_or_else(|| anyhow!("Invalid legacy grant"))?
                    .remove("epoch");
                let grant: Grant = serde_json::from_value(entry)?;
                grants.retain(|g| g.owner != grant.owner || g.scope != grant.scope);
                grants.push(grant);
            }
        }
        drop(connection);
        let grants = Config::from_grants(&grants).into_grants()?;
        let hub = Self {
            book: Mutex::new(Book::default()),
            path: Some(path.into()),
            changed: Default::default(),
        };
        {
            let mut book = hub.book.lock().unwrap();
            book.disk = Some(hub.persist(&grants, None)?);
            book.grants = grants;
        }
        // Successful migration discards the old request/history store.
        std::fs::remove_file(legacy)?;
        Ok(hub)
    }
    fn persist(&self, grants: &[Grant], expected: Option<&[u8]>) -> Result<Vec<u8>> {
        let path = self
            .path
            .as_ref()
            .ok_or_else(|| anyhow!("Permission file unavailable"))?;
        let parent = path
            .parent()
            .ok_or_else(|| anyhow!("Permission directory unavailable"))?;
        std::fs::create_dir_all(parent)?;
        // Serialize writers sharing the global Agent home, then detect manual/stale edits.
        let lock = std::fs::OpenOptions::new()
            .create(true)
            .truncate(false)
            .read(true)
            .write(true)
            .mode(0o600)
            .open(parent.join(".permissions.lock"))?;
        anyhow::ensure!(
            unsafe { libc::flock(lock.as_raw_fd(), libc::LOCK_EX) } == 0,
            "Permission file lock unavailable"
        );
        anyhow::ensure!(
            read(path)?.as_deref() == expected,
            "permissions.json changed externally; restart Server before editing permissions in the UI"
        );
        let mut bytes = serde_json::to_vec_pretty(&Config::from_grants(grants))?;
        bytes.push(b'\n');
        let temporary = parent.join(format!(".permissions-{}.tmp", id()));
        let result = (|| -> Result<()> {
            let mut file = std::fs::OpenOptions::new()
                .create_new(true)
                .write(true)
                .mode(0o600)
                .open(&temporary)?;
            file.write_all(&bytes)?;
            file.sync_all()?;
            std::fs::rename(&temporary, path)?;
            std::fs::File::open(parent)?.sync_all()?;
            Ok(())
        })();
        if result.is_err() {
            let _ = std::fs::remove_file(&temporary);
        }
        result?;
        Ok(bytes)
    }
    fn save(&self, mut next: Book, book: &mut Book) -> Result<()> {
        if next.grants != book.grants && self.path.is_some() {
            next.disk = Some(self.persist(&next.grants, book.disk.as_deref())?);
        }
        *book = next;
        self.changed.notify_waiters();
        Ok(())
    }
    pub fn grants(&self, owner: &str) -> Value {
        json!(
            self.book
                .lock()
                .unwrap()
                .grants
                .iter()
                .filter(|g| g.owner == owner)
                .collect::<Vec<_>>()
        )
    }
    fn enqueue(&self, request: Request) -> Result<String> {
        let mut book = self.book.lock().unwrap();
        if let Some(old) = book.requests.iter().find(|p| {
            p.request.owner == request.owner
                && p.request.epoch == request.epoch
                && p.request.turn == request.turn
                && p.request.call_id == request.call_id
        }) {
            anyhow::ensure!(old.request == request, "Conflicting permission request ID");
            return Ok(old.id.clone());
        }
        anyhow::ensure!(
            book.requests.len() < 1000,
            "Permission request limit reached"
        );
        let key = id();
        book.requests.push(Pending {
            id: key.clone(),
            request,
            phase: "pending".into(),
            decision: String::new(),
        });
        self.changed.notify_waiters();
        Ok(key)
    }
    fn cancel(&self, key: &str) -> Result<()> {
        let mut book = self.book.lock().unwrap();
        book.requests.retain(|p| p.id != key);
        self.changed.notify_waiters();
        Ok(())
    }
    fn consume(&self, key: &str) -> Result<Option<bool>> {
        let mut book = self.book.lock().unwrap();
        let Some(index) = book.requests.iter().position(|p| p.id == key) else {
            return Ok(Some(false));
        };
        if book.requests[index].phase == "pending" {
            return Ok(None);
        }
        let approved = book.requests.remove(index).phase == "approved";
        self.changed.notify_waiters();
        Ok(Some(approved))
    }
    fn decide(&self, key: &str, action: &str, scope: Option<Scope>) -> Result<()> {
        let mut book = self.book.lock().unwrap();
        let mut next = book.clone();
        if action == "revoke" {
            next.grants.retain(|g| g.id != key);
        } else {
            anyhow::ensure!(
                matches!(action, "allow_once" | "allow_persistent" | "deny"),
                "Unknown permission decision"
            );
            let index = next
                .requests
                .iter()
                .position(|p| p.id == key)
                .ok_or_else(|| anyhow!("Permission request is no longer pending"))?;
            let p = &mut next.requests[index];
            if p.decision == action && p.phase == "approved" {
                if action == "allow_persistent" {
                    let expected = scope.as_ref().unwrap_or(&p.request.suggested_scope);
                    anyhow::ensure!(
                        next.grants
                            .iter()
                            .any(|g| g.id == key && &g.scope == expected),
                        "Conflicting repeated grant scope"
                    );
                }
                return Ok(());
            }
            anyhow::ensure!(
                p.phase == "pending",
                "Permission request is no longer pending"
            );
            if action == "deny" {
                next.requests.remove(index);
            } else {
                p.decision = action.into();
                p.phase = "approved".into();
                if action == "allow_persistent" {
                    let scope = scope.unwrap_or_else(|| p.request.suggested_scope.clone());
                    anyhow::ensure!(
                        scope.covers(&p.request.suggested_scope),
                        "Grant must cover the reviewed operation"
                    );
                    let grant = Grant {
                        id: p.id.clone(),
                        owner: p.request.owner.clone(),
                        scope,
                    };
                    let checked = Config::from_grants(std::slice::from_ref(&grant))
                        .into_grants()?
                        .remove(0);
                    anyhow::ensure!(checked == grant, "Permission scope changed after review");
                    // Replace the same scope and remove narrower grants now covered by it.
                    next.grants
                        .retain(|g| g.owner != grant.owner || !grant.scope.covers(&g.scope));
                    anyhow::ensure!(next.grants.len() < 1000, "Persistent grant limit reached");
                    next.grants.push(grant);
                }
            }
        }
        self.save(next, &mut book)
    }
}
fn live(s: &Service, r: &Request) -> bool {
    if s.stopping() {
        return false;
    }
    let inner = s.inner.lock().unwrap();
    agent::current(&inner.store)
        .ok()
        .flatten()
        .is_some_and(|b| {
            b.spec.engine == agent::Engine::Pot && b.epoch == r.epoch && b.pot_owner == r.owner
        })
        && inner
            .turn
            .as_ref()
            .filter(|t| !t.cancelling)
            .and_then(|t| inner.store.message(&t.user_message_id).ok().flatten())
            .is_some_and(|m| m.request_id.as_deref() == Some(&r.turn))
}
struct WaitingRequest<'a> {
    hub: &'a Hub,
    key: String,
}
impl Drop for WaitingRequest<'_> {
    fn drop(&mut self) {
        let _ = self.hub.cancel(&self.key);
    }
}
pub(crate) async fn request(s: &Service, epoch: u64, turn: &str, value: Value) -> Result<Value> {
    let request: Request = serde_json::from_value(value)?;
    anyhow::ensure!(
        request.owner == s.usage_scope
            && request.epoch == epoch
            && request.turn == turn
            && live(s, &request),
        "Permission identity changed"
    );
    anyhow::ensure!(
        request.call_id.len() <= 256
            && !request.call_id.is_empty()
            && serde_json::to_vec(&request)?.len() <= 300000,
        "Invalid permission request"
    );
    anyhow::ensure!(
        request.suggested_scope == pot_agent::permissions::scope(&request.tool, &request.args)?,
        "Permission scope does not match operation"
    );
    let key = s.permissions.enqueue(request.clone())?;
    let _waiting = WaitingRequest {
        hub: &s.permissions,
        key: key.clone(),
    };
    loop {
        let notification = s.permissions.changed.notified();
        if !live(s, &request) {
            s.permissions.cancel(&key)?;
            anyhow::bail!("Permission request cancelled");
        }
        if let Some(approved) = s.permissions.consume(&key)? {
            return Ok(json!({"request":request,"approved":approved}));
        }
        tokio::select! {
            _ = notification => {},
            _ = s.cancel_notify.notified() => {},
            _ = s.stop_requested() => {},
            _ = tokio::time::sleep(std::time::Duration::from_millis(200)) => {},
        }
    }
}
fn lane(root: &Service, owner: &str) -> Option<Service> {
    if owner == "main" {
        Some(root.clone())
    } else {
        tasks::permission_lane(root, owner)
    }
}
fn refresh(root: &Service) -> Result<()> {
    let records = root.permissions.book.lock().unwrap().requests.clone();
    for p in records
        .into_iter()
        .filter(|p| matches!(p.phase.as_str(), "pending" | "approved"))
    {
        if lane(root, &p.request.owner).is_none_or(|s| !live(&s, &p.request)) {
            root.permissions.cancel(&p.id)?;
        }
    }
    Ok(())
}
pub async fn inspect(State(root): State<Service>, h: HeaderMap) -> ApiResult<Json<Value>> {
    root.authorized(&h)?;
    if root.is_worker {
        return Err(error(
            StatusCode::FORBIDDEN,
            "owner_only",
            "Owner permission administration required",
        ));
    }
    refresh(&root).map_err(internal)?;
    let book = root.permissions.book.lock().unwrap();
    Ok(Json(
        json!({"requests":book.requests.iter().filter(|p| p.phase == "pending").map(|p| json!({"id":p.id,"request":p.request})).collect::<Vec<_>>(),"grants":book.grants}),
    ))
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Decision {
    id: String,
    action: String,
    #[serde(default)]
    scope: Option<Scope>,
}
pub async fn decide(
    State(root): State<Service>,
    h: HeaderMap,
    Json(body): Json<Decision>,
) -> ApiResult<Json<Value>> {
    root.authorized(&h)?;
    if root.is_worker {
        return Err(error(
            StatusCode::FORBIDDEN,
            "owner_only",
            "Owner permission administration required",
        ));
    }
    refresh(&root).map_err(internal)?;
    root.permissions
        .decide(&body.id, &body.action, body.scope)
        .map_err(|e| error(StatusCode::CONFLICT, "permission_decision", &e.to_string()))?;
    Ok(Json(json!({"ok":true})))
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::os::unix::fs::PermissionsExt;
    fn operation() -> Request {
        Request {
            owner: "main".into(),
            epoch: 1,
            turn: "turn".into(),
            call_id: "call".into(),
            tool: "host_exec_command".into(),
            args: json!({"cmd":"true","workdir":"/","timeout_ms":60000}),
            suggested_scope: Scope::Exact {
                tool: "host_exec_command".into(),
                args: json!({"cmd":"true","workdir":"/","timeout_ms":60000}),
            },
        }
    }
    fn directory() -> PathBuf {
        let path = std::env::temp_dir().join(format!("pot-permissions-{}", id()));
        std::fs::create_dir(&path).unwrap();
        path
    }
    #[test]
    fn once_is_consumed_once_and_only_current_grants_survive_restart() {
        let root = directory();
        let path = root.join("permissions.json");
        let hub = Hub::open(&path).unwrap();
        let initial = read(&path).unwrap();
        let r = operation();
        let key = hub.enqueue(r.clone()).unwrap();
        assert_eq!(hub.enqueue(r.clone()).unwrap(), key);
        let mut conflict = r.clone();
        conflict.args["cmd"] = json!("false");
        assert!(hub.enqueue(conflict).is_err());
        assert!(hub.consume(&key).unwrap().is_none());
        hub.decide(&key, "allow_once", None).unwrap();
        assert_eq!(hub.consume(&key).unwrap(), Some(true));
        assert_eq!(hub.consume(&key).unwrap(), Some(false));
        assert!(hub.book.lock().unwrap().requests.is_empty());
        assert_eq!(read(&path).unwrap(), initial);
        let mut next = r;
        next.call_id = "saved".into();
        let saved = hub.enqueue(next.clone()).unwrap();
        hub.decide(&saved, "allow_persistent", None).unwrap();
        hub.decide(&saved, "allow_persistent", None).unwrap();
        // The same Agent/scope replaces its previous state rather than appending history.
        next.call_id = "replacement".into();
        next.epoch = 99;
        let replacement = hub.enqueue(next.clone()).unwrap();
        hub.decide(&replacement, "allow_persistent", None).unwrap();
        assert_eq!(hub.grants("main").as_array().unwrap().len(), 1);
        next.call_id = "pending".into();
        let pending = hub.enqueue(next).unwrap();
        drop(hub);
        let hub = Hub::open(&path).unwrap();
        assert_eq!(hub.grants("main").as_array().unwrap().len(), 1);
        assert!(hub.grants("other").as_array().unwrap().is_empty());
        assert_eq!(hub.consume(&pending).unwrap(), Some(false));
        assert_eq!(hub.consume(&saved).unwrap(), Some(false));
        assert_eq!(hub.consume(&replacement).unwrap(), Some(false));
        let file: Value = serde_json::from_slice(&std::fs::read(&path).unwrap()).unwrap();
        assert_eq!(file["version"], 1);
        assert_eq!(file["grants"][0]["agent"], "main");
        assert_eq!(file["grants"][0]["permission"]["type"], "operation");
        assert!(file.get("requests").is_none());
        assert!(file["grants"][0].get("epoch").is_none());
        assert_eq!(
            std::fs::metadata(&path).unwrap().permissions().mode() & 0o777,
            0o600
        );
        hub.decide(&replacement, "revoke", None).unwrap();
        assert!(hub.grants("main").as_array().unwrap().is_empty());
        drop(hub);
        assert!(
            Hub::open(&path)
                .unwrap()
                .grants("main")
                .as_array()
                .unwrap()
                .is_empty()
        );
        std::fs::remove_dir_all(root).unwrap();
    }
    #[test]
    fn settled_requests_do_not_accumulate_or_touch_the_file() {
        let root = directory();
        let path = root.join("permissions.json");
        let hub = Hub::open(&path).unwrap();
        let bytes = read(&path).unwrap();
        for n in 0..1100 {
            let mut request = operation();
            request.call_id = n.to_string();
            let key = hub.enqueue(request).unwrap();
            if n % 2 == 0 {
                hub.decide(&key, "deny", None).unwrap();
            } else {
                hub.cancel(&key).unwrap();
            }
            assert_eq!(hub.consume(&key).unwrap(), Some(false));
            assert!(hub.book.lock().unwrap().requests.is_empty());
        }
        let key = hub.enqueue(operation()).unwrap();
        drop(WaitingRequest {
            hub: &hub,
            key: key.clone(),
        });
        assert_eq!(hub.consume(&key).unwrap(), Some(false));
        assert!(hub.book.lock().unwrap().requests.is_empty());
        assert_eq!(read(&path).unwrap(), bytes);
        std::fs::remove_dir_all(root).unwrap();
    }
    #[test]
    fn manual_edits_take_effect_on_restart_and_are_not_overwritten_by_stale_writers() {
        let root = directory();
        let path = root.join("permissions.json");
        let hub = Hub::open(&path).unwrap();
        let stale = Hub::open(&path).unwrap();
        let key = hub.enqueue(operation()).unwrap();
        hub.decide(&key, "allow_persistent", None).unwrap();
        let edited = std::fs::read(&path).unwrap();
        let key = stale.enqueue(operation()).unwrap();
        assert!(stale.decide(&key, "allow_persistent", None).is_err());
        assert!(stale.grants("main").as_array().unwrap().is_empty());
        assert_eq!(stale.consume(&key).unwrap(), None);
        assert_eq!(std::fs::read(&path).unwrap(), edited);
        std::fs::write(&path, "{\"version\":1,\"grants\":[]}").unwrap();
        assert_eq!(hub.grants("main").as_array().unwrap().len(), 1);
        assert!(
            Hub::open(&path)
                .unwrap()
                .grants("main")
                .as_array()
                .unwrap()
                .is_empty()
        );
        std::fs::write(&path, "{bad").unwrap();
        assert!(Hub::open(&path).is_err());
        assert_eq!(std::fs::read_to_string(&path).unwrap(), "{bad");
        std::fs::remove_dir_all(root).unwrap();
    }
    #[test]
    fn legacy_migration_keeps_latest_permanent_grants_and_discards_history() {
        let root = directory();
        let legacy = root.join("permissions-v1.sqlite");
        let path = root.join("permissions.json");
        let store = Store::open(&legacy).unwrap();
        let r = operation();
        store
            .set(
                "permissions.v1",
                &json!({"requests":[{"request":r}], "grants":[
                    {"id":"old","owner":"main","epoch":1,"scope":r.suggested_scope},
                    {"id":"latest","owner":"main","epoch":2,"scope":r.suggested_scope}
                ]})
                .to_string(),
            )
            .unwrap();
        drop(store);
        let hub = Hub::open_with_legacy(&path, &legacy).unwrap();
        assert!(!legacy.exists());
        assert!(hub.book.lock().unwrap().requests.is_empty());
        assert_eq!(hub.grants("main")[0]["id"], "latest");
        assert_eq!(hub.grants("main").as_array().unwrap().len(), 1);
        std::fs::remove_dir_all(root).unwrap();
    }
    #[tokio::test]
    async fn owner_only_decisions_and_cancelled_turns_cannot_approve() {
        let mut s = tasks::tests::fixture();
        s.is_worker = true;
        assert_eq!(
            inspect(State(s.clone()), tasks::tests::headers())
                .await
                .unwrap_err()
                .0,
            StatusCode::FORBIDDEN
        );
        s.is_worker = false;
        let mut r = operation();
        r.epoch = agent::binding(&s).unwrap().epoch;
        let key = s.permissions.enqueue(r).unwrap();
        let body = serde_json::from_value(json!({"id":key,"action":"allow_once"})).unwrap();
        assert_eq!(
            decide(State(s.clone()), tasks::tests::headers(), Json(body))
                .await
                .unwrap_err()
                .0,
            StatusCode::CONFLICT
        );
        assert_eq!(s.permissions.consume(&key).unwrap(), Some(false));
        assert!(s.permissions.book.lock().unwrap().requests.is_empty());
        std::fs::remove_dir_all(s.data.parent().unwrap()).unwrap();
    }
}
