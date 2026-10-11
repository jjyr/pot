//! Encrypted SQLite secrets. Master keys are supplied independently of the database.
use anyhow::{Result, anyhow, ensure};
use openssl::{
    rand::rand_bytes,
    symm::{Cipher, decrypt_aead, encrypt_aead},
};
use rusqlite::{Connection, OptionalExtension, params};
use std::{
    path::Path,
    sync::{Arc, Mutex},
};
use zeroize::Zeroizing;

pub trait KeySource: Send + Sync {
    fn key(&self, allow_create: bool) -> Result<Zeroizing<Vec<u8>>>;
}
/// Explicit process-owned key, useful for embedders and deterministic tests.
pub struct MemoryKey(Zeroizing<Vec<u8>>);
impl MemoryKey {
    pub fn new(key: Vec<u8>) -> Result<Self> {
        ensure!(key.len() == 32, "Secret master key must contain 32 bytes");
        Ok(Self(Zeroizing::new(key)))
    }
}
impl KeySource for MemoryKey {
    fn key(&self, _allow_create: bool) -> Result<Zeroizing<Vec<u8>>> {
        Ok(self.0.clone())
    }
}
/// macOS uses Keychain; other platforms use ~/.pot/secrets/master.key.
pub struct SystemKey {
    account: String,
}
impl SystemKey {
    pub fn new(account: String) -> Self {
        Self { account }
    }
}
impl KeySource for SystemKey {
    fn key(&self, allow_create: bool) -> Result<Zeroizing<Vec<u8>>> {
        if let Some(path) = std::env::var_os("POT_SECRET_KEY_FILE") {
            return read_key_file(Path::new(&path));
        }
        #[cfg(target_os = "macos")]
        {
            static LOCK: Mutex<()> = Mutex::new(());
            let _guard = LOCK.lock().unwrap();
            use security_framework::passwords::{get_generic_password, set_generic_password};
            let service = "pot.control.secrets";
            let key = match get_generic_password(service, &self.account) {
                Ok(value) => value,
                Err(error) if error.code() == -25300 && allow_create => {
                    let mut key = Zeroizing::new(vec![0; 32]);
                    rand_bytes(&mut key)?;
                    set_generic_password(service, &self.account, &key)?;
                    return Ok(key);
                }
                Err(_) => return Err(anyhow!("Control secret key unavailable in Keychain")),
            };
            ensure!(key.len() == 32, "Invalid Control master key");
            Ok(Zeroizing::new(key))
        }
        #[cfg(not(target_os = "macos"))]
        {
            let _ = &self.account;
            let home = std::env::var_os("HOME")
                .filter(|v| !v.is_empty())
                .ok_or_else(|| anyhow!("HOME is unavailable; configure POT_SECRET_KEY_FILE"))?;
            default_file_key(
                &Path::new(&home).join(".pot/secrets/master.key"),
                allow_create,
            )
        }
    }
}

fn read_key_file(path: &Path) -> Result<Zeroizing<Vec<u8>>> {
    use std::{io::Read, os::unix::fs::PermissionsExt};
    let file = std::fs::File::open(path)?;
    let metadata = file.metadata()?;
    ensure!(
        metadata.is_file() && metadata.permissions().mode() & 0o077 == 0,
        "Secret key file must be a regular owner-only file"
    );
    let mut key = Zeroizing::new(Vec::new());
    file.take(33).read_to_end(&mut key)?;
    ensure!(
        key.len() == 32,
        "Secret key file must contain exactly 32 raw bytes"
    );
    Ok(key)
}

#[cfg(any(not(target_os = "macos"), test))]
fn default_file_key(path: &Path, allow_create: bool) -> Result<Zeroizing<Vec<u8>>> {
    use std::{
        io::{ErrorKind, Write},
        os::unix::fs::{DirBuilderExt, PermissionsExt},
    };
    match std::fs::symlink_metadata(path) {
        Ok(_) => return read_key_file(path),
        Err(error) if error.kind() == ErrorKind::NotFound => {}
        Err(error) => return Err(error.into()),
    }
    ensure!(
        allow_create,
        "Secret master key is missing; restore the original key"
    );
    let parent = path
        .parent()
        .ok_or_else(|| anyhow!("Secret key directory missing"))?;
    std::fs::DirBuilder::new()
        .recursive(true)
        .mode(0o700)
        .create(parent)?;
    ensure!(
        std::fs::metadata(parent)?.permissions().mode() & 0o077 == 0,
        "Secret key directory must be owner-only"
    );
    let mut key = Zeroizing::new(vec![0; 32]);
    rand_bytes(&mut key)?;
    let mut temporary = tempfile::NamedTempFile::new_in(parent)?;
    temporary.write_all(&key)?;
    temporary.as_file().sync_all()?;
    // Publish only a complete key, without replacing another process's key.
    match temporary.persist_noclobber(path) {
        Ok(_) => std::fs::File::open(parent)?.sync_all()?,
        Err(error) if error.error.kind() == ErrorKind::AlreadyExists => {}
        Err(error) => return Err(error.error.into()),
    }
    read_key_file(path)
}

pub struct Store {
    db: Mutex<Connection>,
    keys: Arc<dyn KeySource>,
}
impl Store {
    pub fn open(path: &Path, keys: Arc<dyn KeySource>) -> Result<Self> {
        use std::os::unix::fs::OpenOptionsExt;
        if let Some(parent) = path.parent() {
            std::fs::create_dir_all(parent)?;
        }
        std::fs::OpenOptions::new()
            .write(true)
            .create(true)
            .truncate(false)
            .mode(0o600)
            .open(path)?;
        let db = Connection::open(path)?;
        db.execute_batch("PRAGMA busy_timeout=5000; CREATE TABLE IF NOT EXISTS secrets (id TEXT PRIMARY KEY, nonce BLOB NOT NULL, ciphertext BLOB NOT NULL, tag BLOB NOT NULL);")?;
        Ok(Self {
            db: Mutex::new(db),
            keys,
        })
    }
    /// Immutable references make a task's selected secret version stable.
    pub fn put(&self, id: &str, plaintext: &[u8]) -> Result<()> {
        ensure!(
            !id.is_empty() && id.len() <= 256 && plaintext.len() <= 1024 * 1024,
            "Invalid secret size or reference"
        );
        let mut db = self.db.lock().unwrap();
        let tx = db.transaction_with_behavior(rusqlite::TransactionBehavior::Immediate)?;
        let populated: bool =
            tx.query_row("SELECT EXISTS(SELECT 1 FROM secrets)", [], |r| r.get(0))?;
        let key = self.keys.key(!populated)?;
        ensure!(key.len() == 32, "Invalid Control master key");
        if let Some(existing) = read(&tx, id, &key)? {
            ensure!(
                existing.len() == plaintext.len() && openssl::memcmp::eq(&existing, plaintext),
                "Secret reference already contains another value"
            );
            return Ok(());
        }
        let mut nonce = [0; 12];
        rand_bytes(&mut nonce)?;
        let mut tag = [0; 16];
        let ciphertext = encrypt_aead(
            Cipher::aes_256_gcm(),
            &key,
            Some(&nonce),
            id.as_bytes(),
            plaintext,
            &mut tag,
        )?;
        tx.execute(
            "INSERT INTO secrets VALUES (?,?,?,?)",
            params![id, nonce.as_slice(), ciphertext, tag.as_slice()],
        )?;
        tx.commit()?;
        Ok(())
    }
    pub fn get(&self, id: &str) -> Result<Zeroizing<Vec<u8>>> {
        let key = self.keys.key(false)?;
        read(&self.db.lock().unwrap(), id, &key)?
            .ok_or_else(|| anyhow!("Secret reference unavailable"))
    }
}
fn read(db: &Connection, id: &str, key: &[u8]) -> Result<Option<Zeroizing<Vec<u8>>>> {
    let row: Option<(Vec<u8>, Vec<u8>, Vec<u8>)> = db
        .query_row(
            "SELECT nonce,ciphertext,tag FROM secrets WHERE id=?",
            [id],
            |r| Ok((r.get(0)?, r.get(1)?, r.get(2)?)),
        )
        .optional()?;
    row.map(|(nonce, ciphertext, tag)| {
        ensure!(
            key.len() == 32 && nonce.len() == 12 && tag.len() == 16,
            "Invalid encrypted secret"
        );
        decrypt_aead(
            Cipher::aes_256_gcm(),
            key,
            Some(&nonce),
            id.as_bytes(),
            &ciphertext,
            &tag,
        )
        .map(Zeroizing::new)
        .map_err(|_| anyhow!("Secret authentication failed"))
    })
    .transpose()
}
#[cfg(test)]
mod tests {
    use super::*;
    struct FileKey(std::path::PathBuf);
    impl KeySource for FileKey {
        fn key(&self, allow_create: bool) -> Result<Zeroizing<Vec<u8>>> {
            default_file_key(&self.0, allow_create)
        }
    }
    #[test]
    fn default_key_is_private_reused_and_never_recreated_for_existing_secrets() {
        use std::os::unix::fs::PermissionsExt;
        let root = tempfile::tempdir().unwrap();
        let path = root.path().join(".pot/secrets/master.key");
        let keys = Arc::new(FileKey(path.clone()));
        let db = root.path().join("control.sqlite");
        let store = Store::open(&db, keys.clone()).unwrap();
        assert!(store.get("missing").is_err());
        assert!(!path.exists());
        store.put("one", b"private").unwrap();
        let original = read_key_file(&path).unwrap();
        assert_eq!(
            std::fs::metadata(&path).unwrap().permissions().mode() & 0o777,
            0o600
        );
        assert_eq!(
            std::fs::metadata(path.parent().unwrap())
                .unwrap()
                .permissions()
                .mode()
                & 0o777,
            0o700
        );
        drop(store);
        let store = Store::open(&db, keys).unwrap();
        assert_eq!(&**store.get("one").unwrap(), b"private");
        assert_eq!(*read_key_file(&path).unwrap(), *original);
        std::fs::remove_file(&path).unwrap();
        assert!(store.put("two", b"another").is_err());
        assert!(store.get("one").is_err());
        assert!(!path.exists());
    }
    #[test]
    fn concurrent_initializers_share_one_complete_key_and_reject_invalid_files() {
        use std::os::unix::fs::PermissionsExt;
        let root = tempfile::tempdir().unwrap();
        let path = root.path().join("secrets/master.key");
        let barrier = Arc::new(std::sync::Barrier::new(8));
        let handles: Vec<_> = (0..8)
            .map(|_| {
                let path = path.clone();
                let barrier = barrier.clone();
                std::thread::spawn(move || {
                    barrier.wait();
                    default_file_key(&path, true).unwrap()
                })
            })
            .collect();
        let keys: Vec<_> = handles.into_iter().map(|h| h.join().unwrap()).collect();
        assert!(keys.iter().all(|k| **k == *keys[0]));
        std::fs::write(&path, b"short").unwrap();
        assert!(default_file_key(&path, true).is_err());
        std::fs::write(&path, [7; 32]).unwrap();
        std::fs::set_permissions(&path, std::fs::Permissions::from_mode(0o644)).unwrap();
        assert!(default_file_key(&path, true).is_err());
    }
    #[test]
    fn ciphertext_survives_reopen_and_rejects_wrong_keys_and_tampering() {
        let mut suffix = [0; 8];
        rand_bytes(&mut suffix).unwrap();
        let path = std::env::temp_dir().join(format!(
            "pot-secrets-{:x}.sqlite",
            u64::from_le_bytes(suffix)
        ));
        let key = Arc::new(MemoryKey::new(vec![7; 32]).unwrap());
        let store = Store::open(&path, key.clone()).unwrap();
        store.put("deploy/v1", b"never-persist-this-token").unwrap();
        store.put("deploy/v1", b"never-persist-this-token").unwrap();
        assert!(store.put("deploy/v1", b"replacement").is_err());
        drop(store);
        assert!(
            !std::fs::read(&path)
                .unwrap()
                .windows(b"never-persist-this-token".len())
                .any(|w| w == b"never-persist-this-token")
        );
        let store = Store::open(&path, key).unwrap();
        assert_eq!(
            &**store.get("deploy/v1").unwrap(),
            b"never-persist-this-token"
        );
        assert!(
            Store::open(&path, Arc::new(MemoryKey::new(vec![8; 32]).unwrap()))
                .unwrap()
                .get("deploy/v1")
                .is_err()
        );
        store
            .db
            .lock()
            .unwrap()
            .execute("UPDATE secrets SET id='other'", [])
            .unwrap();
        assert!(store.get("other").is_err());
        drop(store);
        std::fs::remove_file(path).unwrap();
    }
}
