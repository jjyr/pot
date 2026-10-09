//! RFC 8291 message encryption; push services receive ciphertext only.
use super::state::*;
use anyhow::{Result, ensure};
use axum::{
    Json,
    extract::State,
    http::{HeaderMap, StatusCode},
    response::Response,
};
use base64::{Engine, engine::general_purpose::URL_SAFE_NO_PAD};
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use std::time::{Duration, Instant};
use web_push::{ContentEncoding, SubscriptionInfo, VapidSignatureBuilder, WebPushMessageBuilder};

const INSTALLATION: &str = "__Host-pot-installation";
#[derive(Clone, Serialize, Deserialize)]
pub struct Subscription {
    pub credential: String,
    pub info: SubscriptionInfo,
    pub locale: String,
    pub private_preview: bool,
    pub sequence: i64,
    pub failures: u32,
    pub retry_after_ms: i64,
}
pub struct Push {
    pem: Vec<u8>,
    pub public_key: String,
    contact: String,
    client: reqwest::Client,
}
impl Push {
    pub fn from_env() -> Result<Option<Self>> {
        let Some(path) = std::env::var_os("POT_ACCESS_VAPID_KEY") else {
            return Ok(None);
        };
        use std::os::unix::fs::PermissionsExt;
        ensure!(
            std::fs::metadata(&path)?.permissions().mode() & 0o077 == 0,
            "VAPID private key requires mode 0600"
        );
        let pem = std::fs::read(path)?;
        let public_key = URL_SAFE_NO_PAD
            .encode(VapidSignatureBuilder::from_pem_no_sub(pem.as_slice())?.get_public_key());
        let contact = std::env::var("POT_ACCESS_VAPID_CONTACT")?;
        ensure!(
            contact.starts_with("mailto:") || contact.starts_with("https://"),
            "VAPID contact must be mailto or HTTPS"
        );
        Ok(Some(Self {
            pem,
            public_key,
            contact,
            client: reqwest::Client::builder()
                .redirect(reqwest::redirect::Policy::none())
                .timeout(Duration::from_secs(15))
                .build()?,
        }))
    }
    fn message(&self, sub: &Subscription, content: &[u8]) -> Result<web_push::WebPushMessage> {
        validate(&sub.info)?;
        let mut vapid = VapidSignatureBuilder::from_pem(self.pem.as_slice(), &sub.info)?;
        vapid.add_claim("sub", self.contact.clone());
        let mut builder = WebPushMessageBuilder::new(&sub.info);
        builder.set_vapid_signature(vapid.build()?);
        builder.set_payload(ContentEncoding::Aes128Gcm, content);
        builder.set_ttl(86400);
        Ok(builder.build()?)
    }
    async fn send(&self, sub: &Subscription, content: &[u8]) -> Result<StatusCode> {
        let message = self.message(sub, content)?;
        let request = web_push::request_builder::build_request::<Vec<u8>>(message);
        let (parts, body) = request.into_parts();
        let mut req = self.client.post(parts.uri.to_string()).body(body);
        for (name, value) in &parts.headers {
            req = req.header(name.as_str(), value.to_str()?);
        }
        let r = req.send().await?;
        Ok(StatusCode::from_u16(r.status().as_u16())?)
    }
}
fn validate(info: &SubscriptionInfo) -> Result<()> {
    let url = url::Url::parse(&info.endpoint)?;
    ensure!(
        url.scheme() == "https"
            && url.username().is_empty()
            && url.password().is_none()
            && url.port_or_known_default() == Some(443)
            && url.fragment().is_none(),
        "Invalid push endpoint"
    );
    // Browser vendor endpoints only. No client-controlled arbitrary HTTP destination.
    ensure!(
        matches!(
            url.domain(),
            Some("fcm.googleapis.com" | "updates.push.services.mozilla.com" | "web.push.apple.com")
        ),
        "Unsupported push provider"
    );
    ensure!(info.endpoint.len() <= 2048, "Endpoint too long");
    let key = URL_SAFE_NO_PAD.decode(&info.keys.p256dh)?;
    ensure!(
        key.len() == 65 && key[0] == 4 && URL_SAFE_NO_PAD.decode(&info.keys.auth)?.len() == 16,
        "Invalid push keys"
    );
    Ok(())
}
fn authorized(a: &Access, h: &HeaderMap, touch: bool) -> Result<String, super::routes::Failure> {
    let _ = touch;
    a.authorization(h)
        .map(|_| a.generation())
        .ok_or_else(|| super::routes::fail(StatusCode::UNAUTHORIZED, "invalid_token"))
}
pub async fn subscribe(
    State(a): State<Access>,
    h: HeaderMap,
    Json(v): Json<Value>,
) -> Result<Response, super::routes::Failure> {
    let credential = authorized(&a, &h, true)?;
    let source = a
        .notifications
        .as_ref()
        .ok_or_else(|| super::routes::fail(StatusCode::SERVICE_UNAVAILABLE, "push_unavailable"))?;
    if a.push.is_none() {
        return Err(super::routes::fail(
            StatusCode::SERVICE_UNAVAILABLE,
            "push_unavailable",
        ));
    }
    let info: SubscriptionInfo =
        serde_json::from_value(v["subscription"].clone()).map_err(|_| super::routes::invalid())?;
    validate(&info).map_err(|_| super::routes::invalid())?;
    let sequence = source
        .notification_sequence()
        .map_err(|_| super::routes::invalid())?;
    let mut i = a.inner.lock().unwrap();
    if !i.enabled || Access::token_generation(&i.credentials.token) != credential {
        return Err(super::routes::fail(
            StatusCode::UNAUTHORIZED,
            "session_expired",
        ));
    }
    let id = cookie(&h, INSTALLATION)
        .filter(|id| i.credentials.subscriptions.contains_key(id))
        .unwrap_or_else(|| uuid::Uuid::new_v4().to_string());
    if i.credentials.subscriptions.len() >= 128 && !i.credentials.subscriptions.contains_key(&id) {
        return Err(super::routes::fail(
            StatusCode::TOO_MANY_REQUESTS,
            "subscription_limit",
        ));
    }
    let sequence = i
        .credentials
        .subscriptions
        .get(&id)
        .filter(|s| s.credential == credential && s.info.endpoint == info.endpoint)
        .map(|s| s.sequence)
        .unwrap_or(sequence);
    // An endpoint belongs to one browser installation. Re-consent does not duplicate it.
    let previous = i.credentials.subscriptions.clone();
    i.credentials
        .subscriptions
        .retain(|_, s| s.info.endpoint != info.endpoint);
    i.credentials.subscriptions.insert(
        id.clone(),
        Subscription {
            credential,
            info,
            locale: if v["locale"] == "zh-CN" {
                "zh-CN"
            } else {
                "en"
            }
            .into(),
            private_preview: v["private_preview"].as_bool().unwrap_or(false),
            sequence,
            failures: 0,
            retry_after_ms: 0,
        },
    );
    if a.persist(&i).is_err() {
        i.credentials.subscriptions = previous;
        return Err(super::routes::fail(
            StatusCode::INTERNAL_SERVER_ERROR,
            "subscription_save_failed",
        ));
    }
    Ok(super::routes::response(
        json!({"enabled":true}),
        Some((INSTALLATION, id, 25920000)),
    ))
}
pub async fn preferences(
    State(a): State<Access>,
    h: HeaderMap,
    Json(v): Json<Value>,
) -> Result<Json<Value>, super::routes::Failure> {
    let credential = authorized(&a, &h, true)?;
    let id = cookie(&h, INSTALLATION).ok_or_else(super::routes::invalid)?;
    let mut i = a.inner.lock().unwrap();
    let sub = i
        .credentials
        .subscriptions
        .get_mut(&id)
        .filter(|s| s.credential == credential)
        .ok_or_else(super::routes::invalid)?;
    let previous = sub.clone();
    sub.locale = if v["locale"] == "zh-CN" {
        "zh-CN"
    } else {
        "en"
    }
    .into();
    if a.persist(&i).is_err() {
        i.credentials.subscriptions.insert(id, previous);
        return Err(super::routes::fail(
            StatusCode::INTERNAL_SERVER_ERROR,
            "subscription_save_failed",
        ));
    }
    Ok(Json(json!({"ok":true})))
}
pub async fn heartbeat(
    State(a): State<Access>,
    h: HeaderMap,
    Json(v): Json<Value>,
) -> Result<Json<Value>, super::routes::Failure> {
    let credential = authorized(&a, &h, false)?;
    if let Some(id) = cookie(&h, INSTALLATION)
        && a.inner
            .lock()
            .unwrap()
            .credentials
            .subscriptions
            .get(&id)
            .is_some_and(|s| s.credential == credential)
    {
        let mut f = a.foreground.lock().unwrap();
        if v["visible"] == true {
            f.insert(id, Instant::now() + Duration::from_secs(25));
        } else {
            f.remove(&id);
        }
    }
    Ok(Json(json!({"ok":true})))
}
pub async fn unsubscribe(
    State(a): State<Access>,
    h: HeaderMap,
) -> Result<Response, super::routes::Failure> {
    authorized(&a, &h, true)?;
    remove(&a, &h).map_err(|_| {
        super::routes::fail(
            StatusCode::INTERNAL_SERVER_ERROR,
            "subscription_save_failed",
        )
    })?;
    Ok(super::routes::response(
        json!({"enabled":false}),
        Some((INSTALLATION, String::new(), 0)),
    ))
}
pub fn remove(a: &Access, h: &HeaderMap) -> Result<()> {
    if let Some(id) = cookie(h, INSTALLATION) {
        let mut i = a.inner.lock().unwrap();
        let old = i.credentials.subscriptions.remove(&id);
        if a.persist(&i).is_err() {
            if let Some(s) = old {
                i.credentials.subscriptions.insert(id, s);
            }
            anyhow::bail!("Unable to persist subscription removal");
        }
        a.foreground.lock().unwrap().remove(&id);
    }
    Ok(())
}
fn now() -> i64 {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .unwrap_or_default()
        .as_millis() as i64
}
fn payload(sub: &Subscription, msg: &pot_protocol::Message) -> Vec<u8> {
    // Bound UTF-8 bytes, not merely character count; retain original chat text.
    let mut end = msg.text.len().min(2000);
    while !msg.text.is_char_boundary(end) {
        end -= 1;
    }
    let body = if sub.private_preview {
        if sub.locale == "zh-CN" {
            "主 Agent 发来新消息"
        } else {
            "New message from the main Agent"
        }
    } else {
        &msg.text[..end]
    };
    let mut preview = body.to_owned();
    loop {
        let content=serde_json::to_vec(&json!({"title":"Pot","body":preview,"message_id":msg.id,"conversation":"main","url":format!("/?message={}",msg.id),"tag":format!("main:{}",msg.id)})).unwrap();
        if content.len() <= 3000 || preview.is_empty() {
            return content;
        }
        for _ in 0..50 {
            preview.pop();
        }
    }
}
pub async fn dispatch(a: Access) {
    let (Some(push), Some(source)) = (a.push.clone(), a.notifications.clone()) else {
        return;
    };
    loop {
        tokio::time::sleep(Duration::from_secs(2)).await;
        let subs = {
            let i = a.inner.lock().unwrap();
            if !i.enabled {
                continue;
            }
            i.credentials.subscriptions.clone()
        };
        for (id, sub) in subs {
            if sub.failures >= 7 || sub.retry_after_ms > now() {
                continue;
            }
            let Ok(messages) = source.notifications(sub.sequence) else {
                continue;
            };
            for (seq, msg) in messages {
                // Hold the policy lock only for checks/commits, never during network I/O.
                let eligible = {
                    let i = a.inner.lock().unwrap();
                    i.enabled
                        && i.credentials.subscriptions.get(&id).is_some_and(|s| {
                            s.sequence < seq && s.info.endpoint == sub.info.endpoint
                        })
                };
                if !eligible {
                    break;
                }
                let visible = a
                    .foreground
                    .lock()
                    .unwrap()
                    .get(&id)
                    .is_some_and(|until| *until > Instant::now());
                let outcome = if visible || msg.created_at_ms < now() - 86400000 {
                    Ok(StatusCode::NO_CONTENT)
                } else {
                    push.send(&sub, &payload(&sub, &msg)).await
                };
                let mut i = a.inner.lock().unwrap();
                let Some(current) = i.credentials.subscriptions.get_mut(&id) else {
                    break;
                };
                if current.info.endpoint != sub.info.endpoint
                    || current.credential != sub.credential
                {
                    break;
                }
                match outcome {
                    Ok(code) if code.is_success() => {
                        current.sequence = current.sequence.max(seq);
                        current.failures = 0;
                        current.retry_after_ms = 0;
                    }
                    Ok(StatusCode::GONE | StatusCode::NOT_FOUND) => {
                        i.credentials.subscriptions.remove(&id);
                    }
                    _ => {
                        current.failures += 1;
                        current.retry_after_ms = now() + 1000 * (1i64 << current.failures.min(10));
                    }
                }
                if a.persist(&i).is_err() {
                    // Fail closed until explicit re-consent; never retry a known delivery in this process.
                    i.credentials.subscriptions.remove(&id);
                    break;
                }
                if !i
                    .credentials
                    .subscriptions
                    .get(&id)
                    .is_some_and(|s| s.sequence >= seq)
                {
                    break;
                }
            }
        }
    }
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn endpoints_cannot_target_private_networks_or_arbitrary_hosts() {
        for url in [
            "http://fcm.googleapis.com/x",
            "https://127.0.0.1/x",
            "https://attacker.example/x",
            "https://fcm.googleapis.com.evil/x",
            "https://user@fcm.googleapis.com/x",
            "https://fcm.googleapis.com:444/x",
        ] {
            let s = SubscriptionInfo::new(url, "x", "x");
            assert!(validate(&s).is_err());
        }
    }
    #[test]
    fn encrypted_original_message_roundtrips_and_tampering_fails() {
        let (key, auth) = ece::generate_keypair_and_auth_secret().unwrap();
        let public = key.pub_as_raw().unwrap();
        let rcgen::CertifiedKey { signing_key, .. } =
            rcgen::generate_simple_self_signed(vec!["pot.example".into()]).unwrap();
        let push = Push {
            pem: signing_key.serialize_pem().into_bytes(),
            public_key: String::new(),
            contact: "mailto:test@example.com".into(),
            client: reqwest::Client::new(),
        };
        let sub = Subscription {
            credential: "key".into(),
            info: SubscriptionInfo::new(
                "https://fcm.googleapis.com/test".to_owned(),
                URL_SAFE_NO_PAD.encode(public),
                URL_SAFE_NO_PAD.encode(auth),
            ),
            locale: "en".into(),
            private_preview: false,
            sequence: 0,
            failures: 0,
            retry_after_ms: 0,
        };
        let mut msg = pot_protocol::Message {
            id: "message-id".into(),
            conversation_id: "main".into(),
            role: pot_protocol::Role::Assistant,
            text: "主 Agent 的原始消息 🚀".into(),
            status: pot_protocol::MessageStatus::Complete,
            created_at_ms: now(),
            request_id: None,
        };
        let content = payload(&sub, &msg);
        let mut encrypted = push
            .message(&sub, &content)
            .unwrap()
            .payload
            .unwrap()
            .content;
        assert!(
            !encrypted
                .windows(msg.text.len())
                .any(|w| w == msg.text.as_bytes())
        );
        assert_eq!(
            ece::decrypt(&key.raw_components().unwrap(), &auth, &encrypted).unwrap(),
            content
        );
        let last = encrypted.len() - 1;
        encrypted[last] ^= 1;
        assert!(ece::decrypt(&key.raw_components().unwrap(), &auth, &encrypted).is_err());
        msg.text = "\"\n😀".repeat(2000);
        let preview = payload(&sub, &msg);
        assert!(preview.len() <= 3000);
        let body: Value = serde_json::from_slice(&preview).unwrap();
        assert!(msg.text.starts_with(body["body"].as_str().unwrap()));
    }
}
