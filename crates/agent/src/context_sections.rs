//! Append-only, classified context. The retained messages are the durable
//! baseline, independent of provider prompt-cache or usage-calibration lifetime.
use anyhow::{Result, ensure};
use pot_core::ContextSection;
use pot_model_protocol::model::{Message, MessageKind};
use sha2::{Digest, Sha256};
use std::collections::BTreeMap;

pub(crate) fn is_user(message: &Message) -> bool {
    message.role == "user" && message.kind == MessageKind::Conversation
}

fn fingerprint(text: &str) -> String {
    Sha256::digest(text.as_bytes())
        .iter()
        .map(|byte| format!("{byte:02x}"))
        .collect()
}

fn render(key: &str, text: &str) -> Message {
    let active = !text.trim().is_empty();
    let category = if matches!(key, "instructions" | "plugins" | "skills") {
        "instructions"
    } else {
        "reference_data"
    };
    let body = if active {
        text.to_owned()
    } else {
        "This section is no longer available; its previous contents no longer apply.".into()
    };
    let mut message = Message::text(
        "user",
        format!(
            "<pot_context key=\"{key}\" category=\"{category}\">\nThis replaces all earlier contents of this section.\n{body}\n</pot_context>"
        ),
    );
    message.kind = MessageKind::Context {
        key: key.into(),
        fingerprint: fingerprint(text),
        active,
    };
    message
}

/// A stable key order keeps full replays deterministic across restart/cache expiry.
pub(crate) fn synchronize(messages: &mut Vec<Message>, sections: &[ContextSection]) -> Result<()> {
    let mut current = BTreeMap::new();
    for section in sections {
        ensure!(
            !section.key.is_empty()
                && section.key.len() <= 64
                && section
                    .key
                    .bytes()
                    .all(|b| b.is_ascii_lowercase() || b.is_ascii_digit() || b == b'-'),
            "Invalid context section key"
        );
        let normalized = section.text.replace("\r\n", "\n");
        ensure!(
            current
                .insert(section.key.as_str(), normalized.trim().to_owned())
                .is_none(),
            "Duplicate context section key"
        );
    }
    let mut previous = BTreeMap::new();
    for message in messages.iter() {
        if let MessageKind::Context {
            key,
            fingerprint,
            active,
        } = &message.kind
        {
            previous.insert(key.clone(), (fingerprint.clone(), *active));
        }
    }
    // Disappearing sections need an explicit revocation, not silent omission.
    for key in previous.keys() {
        current.entry(key.as_str()).or_default();
    }
    let mut updates = vec![];
    for (key, text) in current {
        if previous
            .get(key)
            .is_some_and(|(prior, _)| *prior == fingerprint(&text))
            || text.is_empty() && previous.get(key).is_none_or(|(_, active)| !*active)
        {
            continue;
        }
        updates.push(render(key, &text));
    }
    messages.extend(updates);
    Ok(())
}

pub(crate) fn full(sections: &[ContextSection]) -> Result<Vec<Message>> {
    let mut messages = vec![];
    synchronize(&mut messages, sections)?;
    Ok(messages)
}

/// Read-only result delivery does not discover resources or revoke the main
/// assistant's sections. If it compacts, retain the latest known active sections.
pub(crate) fn retained(messages: &[Message]) -> Vec<Message> {
    let mut latest = BTreeMap::new();
    for message in messages {
        if let MessageKind::Context { key, active, .. } = &message.kind {
            latest.insert(key, (*active, message));
        }
    }
    latest
        .into_values()
        .filter(|(active, _)| *active)
        .map(|(_, message)| message.clone())
        .collect()
}
