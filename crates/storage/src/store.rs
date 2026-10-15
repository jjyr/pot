use anyhow::{Result, bail};
use pot_protocol::*;
use rusqlite::{Connection, OptionalExtension, params};
use serde_json::json;
use std::path::Path;
use uuid::Uuid;

pub struct Store(pub Connection);
pub fn id() -> String {
    Uuid::new_v4().to_string()
}
pub fn now() -> i64 {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .unwrap()
        .as_millis() as i64
}
impl Store {
    pub fn open(path: &Path) -> Result<Self> {
        let conn = Connection::open(path)?;
        conn.execute_batch("PRAGMA journal_mode=DELETE; PRAGMA synchronous=EXTRA;")?;
        let version: i64 = conn.query_row("PRAGMA user_version", [], |r| r.get(0))?;
        if version > 1 {
            bail!("Database schema is newer than this runtime");
        }
        if version == 0 {
            conn.execute_batch("BEGIN; CREATE TABLE messages(id TEXT PRIMARY KEY, request_id TEXT UNIQUE, body TEXT NOT NULL); CREATE TABLE events(cursor INTEGER PRIMARY KEY AUTOINCREMENT, body TEXT NOT NULL); CREATE TABLE metadata(key TEXT PRIMARY KEY, value TEXT NOT NULL); PRAGMA user_version=1; COMMIT;")?;
        }
        conn.execute_batch("CREATE TABLE IF NOT EXISTS main_message_outbox(sequence INTEGER PRIMARY KEY AUTOINCREMENT, message_id TEXT UNIQUE NOT NULL, body TEXT NOT NULL);")?;
        let mut store = Self(conn);
        // Interrupted turns are never silently replayed; preserve partial output.
        for mut message in store.messages()? {
            if matches!(
                message.status,
                MessageStatus::Accepted | MessageStatus::Streaming
            ) {
                message.status = MessageStatus::Failed;
                store.save(message)?;
            }
        }
        Ok(store)
    }
    pub fn get(&self, key: &str) -> Result<Option<String>> {
        Ok(self
            .0
            .query_row("SELECT value FROM metadata WHERE key=?", [key], |r| {
                r.get(0)
            })
            .optional()?)
    }
    pub fn set(&self, key: &str, value: &str) -> Result<()> {
        self.0.execute(
            "INSERT INTO metadata VALUES (?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value",
            [key, value],
        )?;
        Ok(())
    }
    pub fn messages(&self) -> Result<Vec<Message>> {
        let mut stmt = self.0.prepare("SELECT body FROM messages ORDER BY rowid")?;
        let rows = stmt.query_map([], |r| r.get::<_, String>(0))?;
        rows.map(|r| Ok(serde_json::from_str(&r?)?)).collect()
    }
    /// Read one settled reply for navigation, without loading a conversation transcript.
    pub fn last_completed_reply(&self) -> Result<Option<Message>> {
        let body: Option<String> = self.0.query_row(
            "SELECT body FROM messages WHERE json_extract(body,'$.role')='assistant' AND json_extract(body,'$.status')='complete' AND trim(json_extract(body,'$.text'))<>'' ORDER BY rowid DESC LIMIT 1",
            [], |row| row.get(0),
        ).optional()?;
        body.map(|body| Ok(serde_json::from_str(&body)?))
            .transpose()
    }
    pub fn message(&self, message_id: &str) -> Result<Option<Message>> {
        let body: Option<String> = self
            .0
            .query_row("SELECT body FROM messages WHERE id=?", [message_id], |r| {
                r.get(0)
            })
            .optional()?;
        body.map(|s| Ok(serde_json::from_str(&s)?)).transpose()
    }
    pub fn task_reports(&self) -> Result<Vec<Message>> {
        let mut stmt = self
            .0
            .prepare("SELECT body FROM messages WHERE id LIKE 'task-report-%' ORDER BY rowid")?;
        let rows = stmt.query_map([], |r| r.get::<_, String>(0))?;
        rows.map(|r| Ok(serde_json::from_str(&r?)?)).collect()
    }
    pub fn duplicate(&self, request: &str) -> Result<Option<Message>> {
        let body: Option<String> = self
            .0
            .query_row(
                "SELECT body FROM messages WHERE request_id=?",
                [request],
                |r| r.get(0),
            )
            .optional()?;
        body.map(|s| Ok(serde_json::from_str(&s)?)).transpose()
    }
    pub fn cursor(&self) -> Result<i64> {
        Ok(self
            .0
            .query_row("SELECT COALESCE(MAX(cursor),0) FROM events", [], |r| {
                r.get(0)
            })?)
    }
    pub fn events(&self, after: i64) -> Result<Vec<Event>> {
        let mut stmt = self
            .0
            .prepare("SELECT cursor,body FROM events WHERE cursor>? ORDER BY cursor")?;
        let rows = stmt.query_map([after], |r| {
            Ok((r.get::<_, i64>(0)?, r.get::<_, String>(1)?))
        })?;
        rows.map(|r| {
            let (cursor, body) = r?;
            let mut e: Event = serde_json::from_str(&body)?;
            e.cursor = Some(cursor);
            Ok(e)
        })
        .collect()
    }
    pub fn notification_count(&self) -> Result<u64> {
        Ok(self
            .0
            .query_row("SELECT COUNT(*) FROM main_message_outbox", [], |row| {
                row.get(0)
            })?)
    }
    pub fn notification_sequence(&self) -> Result<i64> {
        Ok(self.0.query_row(
            "SELECT COALESCE(MAX(sequence),0) FROM main_message_outbox",
            [],
            |r| r.get(0),
        )?)
    }
    pub fn notifications(&self, after: i64) -> Result<Vec<(i64, Message)>> {
        let mut stmt = self.0.prepare("SELECT sequence,body FROM main_message_outbox WHERE sequence>? ORDER BY sequence LIMIT 32")?;
        let rows = stmt.query_map([after], |r| {
            Ok((r.get::<_, i64>(0)?, r.get::<_, String>(1)?))
        })?;
        rows.map(|r| {
            let (seq, body) = r?;
            Ok((seq, serde_json::from_str(&body)?))
        })
        .collect()
    }
    pub fn save(&mut self, message: Message) -> Result<Event> {
        self.save_with_metadata(message, None)
    }
    pub fn save_with_metadata(
        &mut self,
        message: Message,
        metadata: Option<(&str, &str)>,
    ) -> Result<Event> {
        self.save_inner(message, metadata, true)
    }
    /// Persist a synthetic display wrapper without treating it as an Agent message.
    pub fn save_non_notifying(&mut self, message: Message) -> Result<Event> {
        self.save_inner(message, None, false)
    }
    fn save_inner(
        &mut self,
        message: Message,
        metadata: Option<(&str, &str)>,
        notify: bool,
    ) -> Result<Event> {
        let tx = self.0.transaction()?;
        tx.execute(
            "INSERT INTO messages VALUES (?,?,?) ON CONFLICT(id) DO UPDATE SET body=excluded.body",
            params![
                message.id,
                message.request_id,
                serde_json::to_string(&message)?
            ],
        )?;
        if let Some((key, value)) = metadata {
            tx.execute("INSERT INTO metadata VALUES (?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value",params![key,value])?;
        }
        // Canonical message and notification eligibility commit together. Updates/replays
        // cannot enqueue a second notification; worker databases are never dispatched.
        if notify
            && message.role == Role::Assistant
            && message.status == MessageStatus::Complete
            && !message.text.trim().is_empty()
            && !message.id.starts_with("task-report-")
        {
            tx.execute(
                "INSERT OR IGNORE INTO main_message_outbox(message_id,body) VALUES (?,?)",
                params![message.id, serde_json::to_string(&message)?],
            )?;
        }
        let mut event = Event {
            event_id: id(),
            cursor: None,
            kind: EventKind::Message { message },
        };
        tx.execute(
            "INSERT INTO events(body) VALUES (?)",
            [json!(event).to_string()],
        )?;
        event.cursor = Some(tx.last_insert_rowid());
        tx.commit()?;
        Ok(event)
    }
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn only_finalized_assistant_messages_enter_durable_outbox_once() {
        let path = std::env::temp_dir().join(format!("pot-push-{}.sqlite", id()));
        let mut s = Store::open(&path).unwrap();
        let mut m = Message {
            id: id(),
            conversation_id: "main".into(),
            role: Role::Assistant,
            text: "actual reply".into(),
            status: MessageStatus::Streaming,
            created_at_ms: now(),
            request_id: None,
        };
        s.save(m.clone()).unwrap();
        assert_eq!(s.notification_sequence().unwrap(), 0);
        m.status = MessageStatus::Complete;
        s.save(m.clone()).unwrap();
        s.save(m.clone()).unwrap();
        assert_eq!(s.notifications(0).unwrap().len(), 1);
        for (role, status, conversation, prefix) in [
            (
                Role::Assistant,
                MessageStatus::Complete,
                "main",
                "task-report-",
            ),
            (Role::Assistant, MessageStatus::Failed, "main", ""),
            (Role::Tool, MessageStatus::Complete, "main", ""),
        ] {
            m.id = format!("{prefix}{}", id());
            m.role = role;
            m.status = status;
            m.conversation_id = conversation.into();
            s.save(m.clone()).unwrap();
        }
        assert_eq!(s.notifications(0).unwrap().len(), 1);
        drop(s);
        let restarted = Store::open(&path).unwrap();
        assert_eq!(restarted.notifications(0).unwrap().len(), 1);
        assert_eq!(restarted.notification_sequence().unwrap(), 1);
        drop(restarted);
        std::fs::remove_file(path).unwrap();
    }
    #[test]
    fn restart_preserves_metadata_and_marks_interrupted_turn() {
        let path = std::env::temp_dir().join(format!("pot-{}.sqlite", id()));
        {
            let mut s = Store::open(&path).unwrap();
            s.set("thread-label", "main").unwrap();
            s.save(Message {
                id: id(),
                conversation_id: "main".into(),
                role: Role::Assistant,
                text: "partial".into(),
                status: MessageStatus::Streaming,
                created_at_ms: now(),
                request_id: Some("r1".into()),
            })
            .unwrap();
            assert_eq!(s.cursor().unwrap(), 1);
        }
        {
            let s = Store::open(&path).unwrap();
            assert_eq!(s.get("thread-label").unwrap().as_deref(), Some("main"));
            let m = s.duplicate("r1").unwrap().unwrap();
            assert_eq!(m.text, "partial");
            assert_eq!(m.status, MessageStatus::Failed);
            assert_eq!(s.events(1).unwrap().len(), 1);
        }
        std::fs::remove_file(path).unwrap();
    }
}
