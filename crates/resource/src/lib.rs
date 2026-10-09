//! Verified resources. Authorization belongs to the calling product service.
use anyhow::{Result, ensure};
use futures::StreamExt;
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use std::{
    collections::BTreeMap,
    io::Read,
    path::{Path, PathBuf},
    sync::{Arc, Mutex},
    time::Instant,
};
use tokio::io::AsyncWriteExt;
#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct Resource {
    pub release_tag: String,
    pub filename: String,
    pub sha256: String,
    pub size_bytes: u64,
    pub unpacked_bytes: u64,
    pub format: Format,
}
#[derive(Clone, Copy, Debug, Deserialize, Serialize)]
#[serde(rename_all = "kebab-case")]
pub enum Format {
    Raw,
    OciTarGzip,
}
impl Resource {
    pub fn validate(&self) -> Result<()> {
        for s in [&self.release_tag, &self.filename] {
            ensure!(
                !s.is_empty()
                    && s.len() < 200
                    && !s.starts_with('.')
                    && s.bytes()
                        .all(|b| b.is_ascii_alphanumeric() || b"-_.".contains(&b)),
                "Invalid resource name"
            );
        }
        ensure!(
            self.sha256.len() == 64
                && self
                    .sha256
                    .bytes()
                    .all(|b| b.is_ascii_digit() || (b'a'..=b'f').contains(&b)),
            "Invalid SHA256"
        );
        ensure!(
            self.size_bytes > 0 && self.unpacked_bytes > 0,
            "Invalid resource size"
        );
        if matches!(self.format, Format::Raw) {
            ensure!(self.size_bytes == self.unpacked_bytes, "Invalid raw size");
        }
        Ok(())
    }
}
#[derive(Clone, Debug, Serialize, Default)]
pub struct Progress {
    pub phase: String,
    pub downloaded_bytes: u64,
    pub total_bytes: u64,
    pub bytes_per_second: u64,
    pub remaining_seconds: Option<u64>,
}
#[derive(Clone)]
pub struct Verified {
    path: PathBuf,
    digest: String,
}
impl Verified {
    pub fn path(&self) -> &Path {
        &self.path
    }
    pub fn digest(&self) -> &str {
        &self.digest
    }
}
#[derive(Clone)]
pub struct Store {
    root: PathBuf,
    client: reqwest::Client,
    locks: Arc<Mutex<BTreeMap<String, Arc<tokio::sync::Mutex<()>>>>>,
    jobs: Arc<Mutex<BTreeMap<String, Arc<Job>>>>,
}
impl Store {
    pub fn new(root: PathBuf) -> Result<Self> {
        std::fs::create_dir_all(&root)?;
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            std::fs::set_permissions(&root, std::fs::Permissions::from_mode(0o700))?;
        }
        Ok(Self {
            root,
            client: reqwest::Client::builder()
                .https_only(true)
                .redirect(reqwest::redirect::Policy::limited(5))
                .build()?,
            locks: Default::default(),
            jobs: Default::default(),
        })
    }
    pub fn root(&self) -> &Path {
        &self.root
    }
    pub fn cached(&self, r: &Resource) -> Result<Option<Verified>> {
        r.validate()?;
        let dir = self.root.join(&r.sha256);
        let blob = dir.join("blob");
        if !blob.is_file() || !dir.join("complete.json").is_file() {
            return Ok(None);
        }
        ensure!(
            std::fs::symlink_metadata(&dir)?.is_dir(),
            "Cache directory link forbidden"
        );
        ensure!(
            std::fs::symlink_metadata(&blob)?.is_file(),
            "Cache blob link forbidden"
        );
        verify(&blob, r)?;
        // The archive, whose digest is embedded in the client, is the authority.
        // An editable completion marker cannot authorize altered extracted bytes.
        let files = expected_files(&blob, r)?;
        check_tree(&dir.join("content"), &dir.join("content"), &files)?;
        for (name, digest) in &files {
            ensure!(
                hash(&dir.join("content").join(name))? == *digest,
                "Cached content changed"
            );
        }
        Ok(Some(Verified {
            path: dir.join("content"),
            digest: r.sha256.clone(),
        }))
    }
    /// One transfer per digest, with per-caller cancellation and shared progress.
    pub async fn prepare(
        &self,
        r: &Resource,
        repo: &str,
        cancelled: &(dyn Fn() -> bool + Send + Sync),
        emit: &(dyn Fn(Progress) + Send + Sync),
    ) -> Result<Verified> {
        r.validate()?;
        ensure!(!cancelled(), "Preparation cancelled");
        let (job, first) = {
            let mut jobs = self.jobs.lock().unwrap();
            if let Some(job) = jobs.get(&r.sha256) {
                job.waiters
                    .fetch_add(1, std::sync::atomic::Ordering::SeqCst);
                (job.clone(), false)
            } else {
                let (updates, _) = tokio::sync::watch::channel(JobState::Progress(Progress {
                    phase: "downloading".into(),
                    total_bytes: r.size_bytes,
                    ..Default::default()
                }));
                let job = Arc::new(Job {
                    updates,
                    waiters: std::sync::atomic::AtomicUsize::new(1),
                });
                jobs.insert(r.sha256.clone(), job.clone());
                (job, true)
            }
        };
        let _waiter = Waiter(job.clone());
        let mut receiver = job.updates.subscribe();
        if first {
            let store = self.clone();
            let resource = r.clone();
            let repo = repo.to_owned();
            let job = job.clone();
            tokio::spawn(async move {
                let result = store
                    .transfer(
                        &resource,
                        &repo,
                        &|| job.waiters.load(std::sync::atomic::Ordering::SeqCst) == 0,
                        &|p| {
                            job.updates.send_replace(JobState::Progress(p));
                        },
                    )
                    .await
                    .map_err(|e| format!("{e:#}"));
                let mut jobs = store.jobs.lock().unwrap();
                job.updates.send_replace(JobState::Complete(result));
                jobs.remove(&resource.sha256);
            });
        }
        loop {
            ensure!(!cancelled(), "Preparation cancelled");
            let state = receiver.borrow_and_update().clone();
            match state {
                JobState::Progress(p) => emit(p),
                JobState::Complete(result) => return result.map_err(anyhow::Error::msg),
            };
            tokio::select! {_=receiver.changed()=>{},_=tokio::time::sleep(std::time::Duration::from_millis(200))=>{}}
        }
    }
    async fn transfer(
        &self,
        r: &Resource,
        repo: &str,
        cancelled: &(dyn Fn() -> bool + Send + Sync),
        emit: &(dyn Fn(Progress) + Send + Sync),
    ) -> Result<Verified> {
        r.validate()?;
        let lock = self
            .locks
            .lock()
            .unwrap()
            .entry(r.sha256.clone())
            .or_default()
            .clone();
        let _lock = loop {
            ensure!(!cancelled(), "Preparation cancelled");
            tokio::select! {guard=lock.lock()=>break guard, _=tokio::time::sleep(std::time::Duration::from_millis(200))=>{}}
        };
        ensure!(!cancelled(), "Preparation cancelled");
        let store = self.clone();
        let resource = r.clone();
        if let Some(v) = tokio::task::spawn_blocking(move || store.cached(&resource)).await?? {
            return Ok(v);
        }
        ensure!(repo.starts_with("https://"), "HTTPS source required");
        #[cfg(unix)]
        check_space(
            &self.root,
            r.size_bytes
                .checked_add(r.unpacked_bytes)
                .ok_or_else(|| anyhow::anyhow!("Resource size overflow"))?,
        )?;
        let url = format!(
            "{}/releases/download/{}/{}",
            repo.trim_end_matches('/'),
            r.release_tag,
            r.filename
        );
        let tmp = self
            .root
            .join(format!(".download-{}", uuid::Uuid::new_v4()));
        std::fs::create_dir(&tmp)?;
        let outcome=async {
            let request=self.client.get(url).timeout(std::time::Duration::from_secs(3600)).send(); tokio::pin!(request); let response=loop {ensure!(!cancelled(),"Preparation cancelled"); tokio::select!{r=&mut request=>break r?.error_for_status()?,_=tokio::time::sleep(std::time::Duration::from_millis(200))=>{}}};
            if let Some(size)=response.content_length() {ensure!(size==r.size_bytes,"Download size mismatch");}
            let mut stream=response.bytes_stream(); let mut file=tokio::fs::File::create(tmp.join("blob")).await?; let mut count=0; let mut digest=Sha256::new(); let start=Instant::now();
            loop {
                ensure!(!cancelled(),"Preparation cancelled");
                let next=tokio::select! { value=stream.next()=>value, _=tokio::time::sleep(std::time::Duration::from_millis(200))=>continue };
                let Some(chunk)=next else {break;}; let chunk=chunk?; count+=chunk.len() as u64; ensure!(count<=r.size_bytes,"Download exceeds expected size"); digest.update(&chunk); file.write_all(&chunk).await?;
                let speed=(count as f64/start.elapsed().as_secs_f64().max(0.001)) as u64;
                emit(Progress{phase:"downloading".into(),downloaded_bytes:count,total_bytes:r.size_bytes,bytes_per_second:speed,remaining_seconds:(speed>0).then(||(r.size_bytes-count)/speed)});
            }
            file.sync_all().await?; drop(file);
            emit(Progress{phase:"verifying".into(),downloaded_bytes:count,total_bytes:r.size_bytes,..Default::default()});
            ensure!(count==r.size_bytes && hex(&digest.finalize())==r.sha256,"Resource SHA256 or size mismatch"); ensure!(!cancelled(),"Preparation cancelled");
            emit(Progress{phase:"extracting".into(),downloaded_bytes:count,total_bytes:r.size_bytes,..Default::default()});
            let resource=r.clone(); let target=tmp.clone(); tokio::task::spawn_blocking(move ||unpack(&target,&resource)).await??;
            ensure!(!cancelled(),"Preparation cancelled");
            let dest=self.root.join(&r.sha256); if dest.exists(){anyhow::bail!("Existing resource cache requires repair");} std::fs::rename(&tmp,&dest)?;
            self.cached(r)?.ok_or_else(||anyhow::anyhow!("Resource cache unavailable"))
        }.await;
        if tmp.exists() {
            let _ = std::fs::remove_dir_all(tmp);
        }
        outcome
    }
    /// Offline owner-provided bytes undergo identical verification and extraction.
    pub fn import(&self, r: &Resource, source: &Path) -> Result<Verified> {
        if let Some(v) = self.cached(r)? {
            return Ok(v);
        }
        let lock = self
            .locks
            .lock()
            .unwrap()
            .entry(r.sha256.clone())
            .or_default()
            .clone();
        let _guard = lock
            .try_lock()
            .map_err(|_| anyhow::anyhow!("Resource is being prepared"))?;
        verify(source, r)?;
        let tmp = self.root.join(format!(".import-{}", uuid::Uuid::new_v4()));
        std::fs::create_dir(&tmp)?;
        let result = (|| {
            std::fs::copy(source, tmp.join("blob"))?;
            unpack(&tmp, r)?;
            let dest = self.root.join(&r.sha256);
            if dest.exists() {
                anyhow::bail!("Existing resource cache requires repair");
            }
            std::fs::rename(&tmp, dest)?;
            self.cached(r)?
                .ok_or_else(|| anyhow::anyhow!("Import failed"))
        })();
        if tmp.exists() {
            let _ = std::fs::remove_dir_all(tmp);
        }
        result
    }
}
fn hash(path: &Path) -> Result<String> {
    let mut file = std::fs::File::open(path)?;
    let mut digest = Sha256::new();
    let mut buf = [0; 65536];
    loop {
        let n = file.read(&mut buf)?;
        if n == 0 {
            break;
        }
        digest.update(&buf[..n]);
    }
    Ok(hex(&digest.finalize()))
}
fn verify(path: &Path, r: &Resource) -> Result<()> {
    r.validate()?;
    ensure!(
        std::fs::metadata(path)?.len() == r.size_bytes && hash(path)? == r.sha256,
        "Resource SHA256 or size mismatch"
    );
    Ok(())
}
fn unpack(dir: &Path, r: &Resource) -> Result<()> {
    let content = dir.join("content");
    std::fs::create_dir(&content)?;
    let mut files = BTreeMap::new();
    match r.format {
        Format::Raw => {
            std::fs::copy(dir.join("blob"), content.join("payload"))?;
            files.insert("payload".into(), r.sha256.clone());
        }
        Format::OciTarGzip => {
            let mut archive = tar::Archive::new(flate2::read::GzDecoder::new(std::fs::File::open(
                dir.join("blob"),
            )?));
            let mut total = 0u64;
            let mut entries = 0;
            for entry in archive.entries()? {
                let mut entry = entry?;
                entries += 1;
                ensure!(entries <= 100000, "Archive entry limit");
                let path = entry.path()?.into_owned();
                ensure!(
                    !path.as_os_str().is_empty()
                        && path
                            .components()
                            .all(|c| matches!(c, std::path::Component::Normal(_))),
                    "Unsafe archive path"
                );
                let kind = entry.header().entry_type();
                ensure!(
                    kind.is_file() || kind.is_dir(),
                    "Archive links and special entries forbidden"
                );
                if kind.is_file() {
                    total = total
                        .checked_add(entry.size())
                        .ok_or_else(|| anyhow::anyhow!("Archive size overflow"))?;
                    ensure!(total <= r.unpacked_bytes, "Archive exceeds unpacked size");
                    let name = path.to_string_lossy().into_owned();
                    ensure!(!files.contains_key(&name), "Duplicate archive file");
                    entry.unpack_in(&content)?;
                    files.insert(name, hash(&content.join(path))?);
                } else {
                    entry.unpack_in(&content)?;
                }
            }
            ensure!(
                total == r.unpacked_bytes
                    && content.join("oci-layout").is_file()
                    && content.join("index.json").is_file(),
                "Invalid OCI layout"
            );
        }
    }
    std::fs::write(dir.join("complete.json"), serde_json::to_vec(&files)?)?;
    Ok(())
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn rejects_tampering_and_revalidates_cache() {
        let dir = tempfile::tempdir().unwrap();
        let source = dir.path().join("source");
        std::fs::write(&source, b"kernel").unwrap();
        let r = Resource {
            release_tag: "v1".into(),
            filename: "kernel".into(),
            sha256: hash(&source).unwrap(),
            size_bytes: 6,
            unpacked_bytes: 6,
            format: Format::Raw,
        };
        let store = Store::new(dir.path().join("cache")).unwrap();
        let v = store.import(&r, &source).unwrap();
        assert!(store.cached(&r).unwrap().is_some());
        std::fs::write(v.path().join("payload"), b"evil").unwrap();
        assert!(store.cached(&r).is_err());
        std::fs::write(&source, b"evil").unwrap();
        assert!(store.import(&r, &source).is_err());
    }
}

fn check_tree(root: &Path, dir: &Path, files: &BTreeMap<String, String>) -> Result<()> {
    ensure!(
        std::fs::symlink_metadata(dir)?.is_dir(),
        "Invalid cache directory"
    );
    for item in std::fs::read_dir(dir)? {
        let item = item?;
        let kind = item.file_type()?;
        ensure!(!kind.is_symlink(), "Cache links forbidden");
        if kind.is_dir() {
            check_tree(root, &item.path(), files)?;
        } else {
            ensure!(
                kind.is_file()
                    && files.contains_key(
                        &item
                            .path()
                            .strip_prefix(root)?
                            .to_string_lossy()
                            .into_owned()
                    ),
                "Unexpected cache entry"
            );
        }
    }
    Ok(())
}
fn expected_files(blob: &Path, r: &Resource) -> Result<BTreeMap<String, String>> {
    if matches!(r.format, Format::Raw) {
        return Ok(BTreeMap::from([("payload".into(), r.sha256.clone())]));
    }
    let mut archive = tar::Archive::new(flate2::read::GzDecoder::new(std::fs::File::open(blob)?));
    let mut files = BTreeMap::new();
    let mut total = 0u64;
    for entry in archive.entries()? {
        let mut entry = entry?;
        let path = entry.path()?.into_owned();
        ensure!(
            path.components()
                .all(|c| matches!(c, std::path::Component::Normal(_))),
            "Unsafe archive path"
        );
        let kind = entry.header().entry_type();
        ensure!(kind.is_file() || kind.is_dir(), "Unsafe archive entry");
        if kind.is_file() {
            total += entry.size();
            ensure!(total <= r.unpacked_bytes, "Archive too large");
            let mut digest = Sha256::new();
            let mut buf = [0; 65536];
            loop {
                let n = entry.read(&mut buf)?;
                if n == 0 {
                    break;
                }
                digest.update(&buf[..n]);
            }
            ensure!(
                files
                    .insert(path.to_string_lossy().into_owned(), hex(&digest.finalize()))
                    .is_none(),
                "Duplicate file"
            );
        }
    }
    ensure!(
        total == r.unpacked_bytes
            && files.contains_key("index.json")
            && files.contains_key("oci-layout"),
        "Invalid OCI archive"
    );
    Ok(files)
}

fn hex(bytes: &[u8]) -> String {
    bytes.iter().map(|byte| format!("{byte:02x}")).collect()
}

#[derive(Clone)]
enum JobState {
    Progress(Progress),
    Complete(std::result::Result<Verified, String>),
}
struct Job {
    updates: tokio::sync::watch::Sender<JobState>,
    waiters: std::sync::atomic::AtomicUsize,
}
struct Waiter(Arc<Job>);
impl Drop for Waiter {
    fn drop(&mut self) {
        self.0
            .waiters
            .fetch_sub(1, std::sync::atomic::Ordering::SeqCst);
    }
}

#[cfg(test)]
mod delivery_tests {
    use super::*;
    use std::{
        io::BufRead,
        process::{Command, Stdio},
        sync::atomic::{AtomicBool, Ordering},
    };
    struct Tls {
        child: std::process::Child,
        dir: tempfile::TempDir,
        repo: String,
        client: reqwest::Client,
    }
    impl Drop for Tls {
        fn drop(&mut self) {
            let _ = self.child.kill();
            let _ = self.child.wait();
        }
    }
    fn tls() -> Tls {
        let dir = tempfile::tempdir().unwrap();
        let cert = dir.path().join("cert.pem");
        let key = dir.path().join("key.pem");
        let config = dir.path().join("openssl.cnf");
        std::fs::write(&config,"[req]\ndistinguished_name=dn\nx509_extensions=ext\nprompt=no\n[dn]\nCN=127.0.0.1\n[ext]\nsubjectAltName=IP:127.0.0.1\nbasicConstraints=critical,CA:FALSE\nextendedKeyUsage=serverAuth\nkeyUsage=digitalSignature,keyEncipherment\n").unwrap();
        assert!(
            Command::new("openssl")
                .args([
                    "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "1", "-config"
                ])
                .arg(config)
                .arg("-keyout")
                .arg(&key)
                .arg("-out")
                .arg(&cert)
                .stdout(Stdio::null())
                .stderr(Stdio::null())
                .status()
                .unwrap()
                .success()
        );
        let script = r#"import ssl,socket,sys,time,pathlib
ctx=ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER);ctx.load_cert_chain(sys.argv[1],sys.argv[2])
sock=socket.socket();sock.bind(('127.0.0.1',0));sock.listen();print(sock.getsockname()[1],flush=True)
while True:
 c,_=sock.accept()
 try:
  c=ctx.wrap_socket(c,server_side=True);head=b''
  while not head.endswith(b'\r\n\r\n'): head+=c.recv(1)
  with open(sys.argv[3],'a') as f: f.write('request\n')
  data=b'k'*131072;c.sendall(b'HTTP/1.1 200 OK\r\nContent-Length: 131072\r\nConnection: close\r\n\r\n')
  for i in range(0,len(data),4096): c.sendall(data[i:i+4096]);time.sleep(.01)
 except (OSError,ssl.SSLError): pass
 finally: c.close()
"#;
        let mut child = Command::new("python3")
            .arg("-u")
            .arg("-c")
            .arg(script)
            .arg(&cert)
            .arg(&key)
            .arg(dir.path().join("requests"))
            .stdout(Stdio::piped())
            .stderr(Stdio::null())
            .spawn()
            .unwrap();
        let mut port = String::new();
        std::io::BufReader::new(child.stdout.take().unwrap())
            .read_line(&mut port)
            .unwrap();
        let client = reqwest::Client::builder()
            .https_only(true)
            .no_proxy()
            .add_root_certificate(
                reqwest::Certificate::from_pem(&std::fs::read(&cert).unwrap()).unwrap(),
            )
            .build()
            .unwrap();
        Tls {
            child,
            dir,
            repo: format!("https://127.0.0.1:{}", port.trim()),
            client,
        }
    }
    fn resource() -> Resource {
        Resource {
            release_tag: "v1".into(),
            filename: "kernel".into(),
            sha256: hex(&Sha256::digest(vec![b'k'; 131072])),
            size_bytes: 131072,
            unpacked_bytes: 131072,
            format: Format::Raw,
        }
    }
    #[tokio::test]
    async fn tls_download_shares_progress_and_cancelling_one_waiter_preserves_transfer() {
        let tls = tls();
        let mut store = Store::new(tls.dir.path().join("cache")).unwrap();
        store.client = tls.client.clone();
        let r = resource();
        let cancel = AtomicBool::new(false);
        let updates = Mutex::new(Vec::new());
        let cancelled = || cancel.load(Ordering::SeqCst);
        let stop = |p: Progress| {
            if p.downloaded_bytes > 0 {
                cancel.store(true, Ordering::SeqCst);
            }
        };
        let progress = |p: Progress| updates.lock().unwrap().push(p.phase);
        let first = store.prepare(&r, &tls.repo, &cancelled, &stop);
        let second = store.prepare(&r, &tls.repo, &|| false, &progress);
        let (a, b) = tokio::join!(first, second);
        assert!(a.is_err());
        let verified = b.unwrap();
        assert!(verified.path().join("payload").is_file());
        assert_eq!(
            std::fs::read_to_string(tls.dir.path().join("requests"))
                .unwrap()
                .lines()
                .count(),
            1
        );
        assert!(
            updates
                .lock()
                .unwrap()
                .iter()
                .any(|phase| phase == "downloading")
        );
        // Network download rejects authentic TLS bytes when the embedded digest differs.
        let mut wrong = r.clone();
        wrong.sha256 = "0".repeat(64);
        assert!(
            store
                .prepare(&wrong, &tls.repo, &|| false, &|_| {})
                .await
                .is_err()
        );
        assert!(!store.root().join(wrong.sha256).exists());
    }
    #[tokio::test]
    async fn last_waiter_cancellation_removes_partial_download() {
        let tls = tls();
        let mut store = Store::new(tls.dir.path().join("cache")).unwrap();
        store.client = tls.client.clone();
        let r = resource();
        let cancel = AtomicBool::new(false);
        assert!(
            store
                .prepare(&r, &tls.repo, &|| cancel.load(Ordering::SeqCst), &|p| {
                    if p.downloaded_bytes > 0 {
                        cancel.store(true, Ordering::SeqCst);
                    }
                })
                .await
                .is_err()
        );
        assert!(
            cancel.load(Ordering::SeqCst),
            "Cancellation must occur during a successful TLS transfer"
        );
        tokio::time::timeout(std::time::Duration::from_secs(3), async {
            while !store.jobs.lock().unwrap().is_empty() {
                tokio::time::sleep(std::time::Duration::from_millis(20)).await;
            }
        })
        .await
        .unwrap();
        assert_eq!(std::fs::read_dir(store.root()).unwrap().count(), 0);
    }
    #[test]
    fn cache_marker_cannot_authorize_modified_bytes_or_links() {
        let dir = tempfile::tempdir().unwrap();
        let source = dir.path().join("source");
        std::fs::write(&source, b"kernel").unwrap();
        let r = Resource {
            release_tag: "v1".into(),
            filename: "kernel".into(),
            sha256: hash(&source).unwrap(),
            size_bytes: 6,
            unpacked_bytes: 6,
            format: Format::Raw,
        };
        let store = Store::new(dir.path().join("cache")).unwrap();
        let v = store.import(&r, &source).unwrap();
        std::fs::write(store.root().join(&r.sha256).join("complete.json"), b"{}").unwrap();
        std::fs::write(v.path().join("payload"), b"evil!!").unwrap();
        assert!(store.cached(&r).is_err());
        std::fs::remove_file(v.path().join("payload")).unwrap();
        #[cfg(unix)]
        std::os::unix::fs::symlink(source, v.path().join("payload")).unwrap();
        assert!(store.cached(&r).is_err());
    }
    #[test]
    fn archive_rejects_links_and_expansion_before_publication() {
        let dir = tempfile::tempdir().unwrap();
        let source = dir.path().join("archive");
        let zipped = flate2::write::GzEncoder::new(
            std::fs::File::create(&source).unwrap(),
            flate2::Compression::default(),
        );
        let mut archive = tar::Builder::new(zipped);
        let mut header = tar::Header::new_gnu();
        header.set_entry_type(tar::EntryType::Symlink);
        header.set_size(0);
        header.set_mode(0o644);
        header.set_link_name("/etc/passwd").unwrap();
        header.set_cksum();
        archive
            .append_data(&mut header, "index.json", std::io::empty())
            .unwrap();
        archive.into_inner().unwrap().finish().unwrap();
        let r = Resource {
            release_tag: "v1".into(),
            filename: "image.gz".into(),
            sha256: hash(&source).unwrap(),
            size_bytes: std::fs::metadata(&source).unwrap().len(),
            unpacked_bytes: 1,
            format: Format::OciTarGzip,
        };
        let store = Store::new(dir.path().join("cache")).unwrap();
        assert!(store.import(&r, &source).is_err());
        assert_eq!(std::fs::read_dir(store.root()).unwrap().count(), 0);
    }
}

#[cfg(unix)]
fn check_space(root: &Path, required: u64) -> Result<()> {
    use std::os::unix::ffi::OsStrExt;
    let path = std::ffi::CString::new(root.as_os_str().as_bytes())?;
    let mut stats = std::mem::MaybeUninit::<libc::statvfs>::uninit();
    // statvfs initializes the output on success; CString is nul terminated.
    ensure!(
        unsafe { libc::statvfs(path.as_ptr(), stats.as_mut_ptr()) } == 0,
        "Cannot inspect resource cache free space"
    );
    let stats = unsafe { stats.assume_init() };
    let available = (stats.f_bavail as u128) * (stats.f_frsize as u128);
    ensure!(
        available >= u128::from(required) + 64 * 1024 * 1024,
        "Not enough free disk space to download and import resources"
    );
    Ok(())
}
