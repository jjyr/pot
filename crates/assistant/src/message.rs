//! Durable assistant mailboxes. Transport and Agent execution are injected by callers.
use anyhow::{Result, anyhow, ensure};
use pot_protocol::message::*;
use rusqlite::{Connection, OptionalExtension, params};
use std::{path::Path, sync::Mutex};

pub struct Mailbox {
    db: Mutex<Connection>,
    control_id: String,
}
impl Mailbox {
    pub fn open(path: &Path, control_id: &str) -> Result<Self> {
        ensure!(identifier(control_id), "Invalid Control identity");
        let db = Connection::open(path)?;
        db.execute_batch("PRAGMA journal_mode=WAL; PRAGMA synchronous=FULL;
            CREATE TABLE IF NOT EXISTS mail_identity (id TEXT PRIMARY KEY);
            CREATE TABLE IF NOT EXISTS mail (
                sequence INTEGER PRIMARY KEY AUTOINCREMENT, id TEXT NOT NULL UNIQUE,
                envelope TEXT NOT NULL, direction TEXT NOT NULL, state TEXT NOT NULL,
                conversation TEXT NOT NULL, recipient TEXT NOT NULL, detail TEXT);
            CREATE TABLE IF NOT EXISTS contacts (control TEXT PRIMARY KEY, directory TEXT NOT NULL);")?;
        let existing: Option<String> = db
            .query_row("SELECT id FROM mail_identity", [], |r| r.get(0))
            .optional()?;
        ensure!(
            existing.as_deref().is_none_or(|id| id == control_id),
            "Mailbox belongs to another Control"
        );
        db.execute(
            "INSERT OR IGNORE INTO mail_identity VALUES (?)",
            [control_id],
        )?;
        ensure!(
            db.query_row("SELECT count(*) FROM mail_identity", [], |r| r
                .get::<_, i64>(0))?
                == 1,
            "Mailbox belongs to another Control"
        );
        // Never replay a model/tool attempt whose completion was not durably recorded.
        db.execute("UPDATE mail SET state='interrupted', detail='Execution outcome requires attention after restart' WHERE state='processing'", [])?;
        Ok(Self {
            db: Mutex::new(db),
            control_id: control_id.into(),
        })
    }
    pub fn register(&self, directory: Directory) -> Result<()> {
        ensure!(
            identifier(&directory.control_id) && directory.control_id != self.control_id,
            "Invalid peer Control"
        );
        ensure!(directory.assistants.len() <= 1024, "Directory too large");
        let mut seen = std::collections::BTreeSet::new();
        for a in &directory.assistants {
            crate::registry::validate_name(&a.name)?;
            ensure!(
                a.address.valid()
                    && a.address.control_id == directory.control_id
                    && seen.insert(&a.address),
                "Invalid assistant directory"
            );
        }
        self.db.lock().unwrap().execute("INSERT INTO contacts VALUES (?,?) ON CONFLICT(control) DO UPDATE SET directory=excluded.directory WHERE contacts.directory != excluded.directory",
            params![directory.control_id, serde_json::to_string(&directory)?])?;
        Ok(())
    }
    pub fn contacts(&self) -> Result<Vec<Assistant>> {
        let db = self.db.lock().unwrap();
        let mut q = db.prepare("SELECT directory FROM contacts ORDER BY control")?;
        let mut out = Vec::new();
        for value in q.query_map([], |r| r.get::<_, String>(0))? {
            out.extend(serde_json::from_str::<Directory>(&value?)?.assistants);
        }
        Ok(out)
    }
    pub fn send(
        &self,
        id: &str,
        from: AssistantRef,
        send: Send,
        parent: Option<&Envelope>,
    ) -> Result<Envelope> {
        ensure!(
            from.control_id == self.control_id && from != send.to,
            "Invalid sender or self-message"
        );
        let mut db = self.db.lock().unwrap();
        let tx = db.transaction()?;
        if let Some(old) = find(&tx, id)? {
            ensure!(
                old.from == from
                    && old.to == send.to
                    && old.text == send.text
                    && old.reply_to == send.reply_to,
                "Message ID reused with different content"
            );
            return Ok(old);
        }
        let reply = send
            .reply_to
            .as_deref()
            .map(|id| find(&tx, id)?.ok_or_else(|| anyhow!("Reply message missing")))
            .transpose()?;
        if let Some(reply) = &reply {
            ensure!(
                reply.to == from && reply.from == send.to,
                "Reply participants do not match"
            );
        }
        // A received message cannot escape the automatic exchange bound by starting a new conversation.
        let hops = parent
            .map(|m| m.hops + 1)
            .unwrap_or(0)
            .max(reply.as_ref().map(|m| m.hops + 1).unwrap_or(0));
        let message = Envelope {
            id: id.into(),
            from,
            to: send.to,
            conversation_id: reply
                .as_ref()
                .map(|m| m.conversation_id.clone())
                .unwrap_or_else(pot_storage::id),
            reply_to: send.reply_to,
            text: send.text,
            hops,
        };
        ensure!(
            message.valid(),
            "Invalid message or automatic exchange limit reached"
        );
        let local = message.to.control_id == self.control_id;
        insert(&tx, &message, if local { "in" } else { "out" })?;
        tx.commit()?;
        Ok(message)
    }
    pub fn deliver(&self, message: &Envelope) -> Result<Receipt> {
        ensure!(
            message.valid()
                && message.to.control_id == self.control_id
                && message.from.control_id != self.control_id,
            "Invalid delivery"
        );
        let mut db = self.db.lock().unwrap();
        let tx = db.transaction()?;
        if let Some(old) = find(&tx, &message.id)? {
            ensure!(old == *message, "Conflicting delivery");
        } else {
            if let Some(reply) = &message.reply_to {
                let parent = find(&tx, reply)?.ok_or_else(|| anyhow!("Reply message missing"))?;
                ensure!(
                    parent.from == message.to
                        && parent.to == message.from
                        && parent.conversation_id == message.conversation_id
                        && message.hops > parent.hops,
                    "Invalid reply binding"
                );
            } else {
                ensure!(
                    !tx.query_row(
                        "SELECT EXISTS(SELECT 1 FROM mail WHERE conversation=?)",
                        [&message.conversation_id],
                        |r| r.get::<_, bool>(0)
                    )?,
                    "Conversation already exists"
                );
            }
            insert(&tx, message, "in")?;
        }
        tx.commit()?;
        Ok(Receipt {
            message: message.clone(),
        })
    }
    pub fn pending(&self, target: &str) -> Result<Vec<Envelope>> {
        let db = self.db.lock().unwrap();
        let mut q = db.prepare("SELECT envelope FROM mail WHERE direction='out' AND state='queued' AND json_extract(envelope, '$.to.control_id')=? ORDER BY sequence LIMIT ?")?;
        q.query_map(params![target, PAGE_SIZE as i64], |r| r.get::<_, String>(0))?
            .map(|r| Ok(serde_json::from_str(&r?)?))
            .collect()
    }
    pub fn acknowledge(&self, receipt: &Receipt) -> Result<()> {
        let db = self.db.lock().unwrap();
        ensure!(
            find(&db, &receipt.message.id)?.as_ref() == Some(&receipt.message),
            "Receipt does not match message"
        );
        ensure!(
            receipt.message.from.control_id == self.control_id
                && receipt.message.to.control_id != self.control_id,
            "Not an outbound message"
        );
        db.execute(
            "UPDATE mail SET state='delivered' WHERE id=? AND direction='out'",
            [&receipt.message.id],
        )?;
        Ok(())
    }
    pub fn next(&self, recipient: &str) -> Result<Option<Envelope>> {
        let db = self.db.lock().unwrap();
        // An uncertain attempt fences its whole local conversation until explicitly investigated.
        let raw: Option<String> = db.query_row("SELECT envelope FROM mail m WHERE direction='in' AND state='queued' AND recipient=?
            AND NOT EXISTS(SELECT 1 FROM mail b WHERE b.conversation=m.conversation AND b.recipient=m.recipient AND b.state IN ('processing','interrupted','failed'))
            ORDER BY sequence LIMIT 1", [recipient], |r| r.get(0)).optional()?;
        raw.map(|s| Ok(serde_json::from_str(&s)?)).transpose()
    }
    pub fn conversation(&self, assistant: &str, conversation: &str) -> Result<Vec<Envelope>> {
        let db = self.db.lock().unwrap();
        let mut q = db.prepare("SELECT envelope FROM mail WHERE conversation=? AND
            ((json_extract(envelope,'$.from.control_id')=? AND json_extract(envelope,'$.from.assistant_id')=?)
            OR (json_extract(envelope,'$.to.control_id')=? AND recipient=?)) ORDER BY sequence LIMIT 32")?;
        q.query_map(
            params![
                conversation,
                self.control_id,
                assistant,
                self.control_id,
                assistant
            ],
            |r| r.get::<_, String>(0),
        )?
        .map(|r| Ok(serde_json::from_str(&r?)?))
        .collect()
    }
    pub fn history(&self, assistant: &str, after: i64) -> Result<Vec<Record>> {
        let db = self.db.lock().unwrap();
        let mut q = db.prepare("SELECT sequence,envelope,state,detail FROM mail WHERE sequence>? AND
            ((json_extract(envelope,'$.from.control_id')=? AND json_extract(envelope,'$.from.assistant_id')=?)
            OR (json_extract(envelope,'$.to.control_id')=? AND recipient=?)) ORDER BY sequence LIMIT ?")?;
        let rows = q.query_map(
            params![
                after,
                self.control_id,
                assistant,
                self.control_id,
                assistant,
                PAGE_SIZE as i64
            ],
            |r| {
                Ok((
                    r.get::<_, i64>(0)?,
                    r.get::<_, String>(1)?,
                    r.get::<_, String>(2)?,
                    r.get::<_, Option<String>>(3)?,
                ))
            },
        )?;
        rows.map(|r| {
            let (sequence, raw, state, detail) = r?;
            Ok(Record {
                sequence,
                message: serde_json::from_str(&raw)?,
                state,
                detail,
            })
        })
        .collect()
    }
    pub fn claim(&self, id: &str) -> Result<()> {
        ensure!(self.db.lock().unwrap().execute("UPDATE mail SET state='processing' WHERE id=? AND direction='in' AND state='queued'", [id])? == 1, "Message already claimed");
        Ok(())
    }
    pub fn finish(&self, id: &str, success: bool, detail: &str) -> Result<()> {
        self.db.lock().unwrap().execute(
            "UPDATE mail SET state=?, detail=? WHERE id=? AND state='processing'",
            params![if success { "processed" } else { "failed" }, detail, id],
        )?;
        Ok(())
    }
}
fn find(db: &Connection, id: &str) -> Result<Option<Envelope>> {
    let raw: Option<String> = db
        .query_row("SELECT envelope FROM mail WHERE id=?", [id], |r| r.get(0))
        .optional()?;
    raw.map(|s| Ok(serde_json::from_str(&s)?)).transpose()
}
fn insert(db: &Connection, message: &Envelope, direction: &str) -> Result<()> {
    db.execute("INSERT INTO mail(id,envelope,direction,state,conversation,recipient) VALUES (?,?,?,'queued',?,?)",
        params![message.id, serde_json::to_string(message)?, direction, message.conversation_id, message.to.assistant_id])?;
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    fn address(control: &str, assistant: &str) -> AssistantRef {
        AssistantRef {
            control_id: control.into(),
            assistant_id: assistant.into(),
        }
    }
    fn input(to: AssistantRef) -> Send {
        Send {
            to,
            text: "Please investigate".into(),
            reply_to: None,
        }
    }
    #[test]
    fn durable_local_delivery_conflicts_and_restart_fence() {
        let path = std::env::temp_dir().join(format!("mail-{}.sqlite", pot_storage::id()));
        let mailbox = Mailbox::open(&path, "local").unwrap();
        let sent = mailbox
            .send(
                "one",
                address("local", "a"),
                input(address("local", "b")),
                None,
            )
            .unwrap();
        assert_eq!(mailbox.next("b").unwrap(), Some(sent.clone()));
        assert_eq!(
            mailbox
                .send(
                    "one",
                    address("local", "a"),
                    input(address("local", "b")),
                    None
                )
                .unwrap(),
            sent
        );
        let mut conflicting = input(address("local", "b"));
        conflicting.text = "different".into();
        assert!(
            mailbox
                .send("one", address("local", "a"), conflicting, None)
                .is_err()
        );
        mailbox.claim("one").unwrap();
        drop(mailbox);
        assert!(Mailbox::open(&path, "wrong").is_err());
        let mailbox = Mailbox::open(&path, "local").unwrap();
        assert!(mailbox.next("b").unwrap().is_none());
        assert_eq!(mailbox.history("b", 0).unwrap()[0].state, "interrupted");
        assert!(mailbox.history("unrelated", 0).unwrap().is_empty());
    }
    #[test]
    fn remote_receipts_reply_binding_and_exchange_bound() {
        let dir = std::env::temp_dir().join(pot_storage::id());
        std::fs::create_dir_all(&dir).unwrap();
        let a = Mailbox::open(&dir.join("a.sqlite"), "a").unwrap();
        let b = Mailbox::open(&dir.join("b.sqlite"), "b").unwrap();
        let sent = a
            .send(
                "one",
                address("a", "same"),
                input(address("b", "same")),
                None,
            )
            .unwrap();
        drop(a);
        let a = Mailbox::open(&dir.join("a.sqlite"), "a").unwrap();
        assert_eq!(a.pending("b").unwrap(), vec![sent.clone()]);
        let receipt = b.deliver(&sent).unwrap();
        assert_eq!(b.deliver(&sent).unwrap(), receipt);
        let mut altered = receipt.clone();
        altered.message.text.push('!');
        assert!(a.acknowledge(&altered).is_err());
        assert!(b.deliver(&altered.message).is_err());
        a.acknowledge(&receipt).unwrap();
        a.acknowledge(&receipt).unwrap();
        assert!(a.pending("b").unwrap().is_empty());
        let reply = b
            .send(
                "two",
                address("b", "same"),
                Send {
                    to: sent.from.clone(),
                    text: "done".into(),
                    reply_to: Some(sent.id.clone()),
                },
                Some(&sent),
            )
            .unwrap();
        assert_eq!(reply.conversation_id, sent.conversation_id);
        a.deliver(&reply).unwrap();
        let mut spoof = reply.clone();
        spoof.id = "spoof".into();
        spoof.from.assistant_id = "other".into();
        assert!(a.deliver(&spoof).is_err());
        let mut parent = reply;
        parent.hops = MAX_HOPS;
        assert!(
            a.send(
                "limit",
                address("a", "same"),
                input(address("b", "same")),
                Some(&parent)
            )
            .is_err()
        );
    }
    #[test]
    fn failed_conversation_does_not_block_other_conversations() {
        let path = std::env::temp_dir().join(format!("mail-{}.sqlite", pot_storage::id()));
        let box_ = Mailbox::open(&path, "local").unwrap();
        box_.send(
            "one",
            address("local", "a"),
            input(address("local", "b")),
            None,
        )
        .unwrap();
        box_.claim("one").unwrap();
        box_.finish("one", false, "unknown").unwrap();
        let next = box_
            .send(
                "two",
                address("local", "a"),
                input(address("local", "b")),
                None,
            )
            .unwrap();
        assert_eq!(box_.next("b").unwrap(), Some(next));
    }
}
