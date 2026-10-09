//! Runtime-loaded libtailscale. Go owns the userspace node and TLS certificate renewal.
use anyhow::{Context, Result, ensure};
use serde_json::{Value, json};
use std::{
    ffi::{CStr, CString, c_char, c_int, c_void},
    path::PathBuf,
    sync::{Arc, Mutex},
};
struct Library {
    handle: usize,
}
impl Library {
    fn load() -> Result<Self> {
        let name = if cfg!(target_os = "macos") {
            "libpot_tailscale.dylib"
        } else {
            "libpot_tailscale.so"
        };
        let exe = std::env::current_exe()?;
        let paths = [
            exe.parent().unwrap().join(name),
            exe.parent().unwrap().join("../lib/pot").join(name),
            PathBuf::from(env!("CARGO_MANIFEST_DIR"))
                .join("../../.local/tailscale")
                .join(name),
        ];
        let path = paths
            .into_iter()
            .find(|p| p.is_file())
            .context("Embedded Tailscale library missing; run mise run build:tailscale")?;
        let path = CString::new(path.to_string_lossy().as_bytes())?;
        let handle = unsafe { libc::dlopen(path.as_ptr(), libc::RTLD_NOW | libc::RTLD_LOCAL) };
        ensure!(!handle.is_null(), "Could not load embedded Tailscale");
        Ok(Self {
            handle: handle as usize,
        })
    }
    unsafe fn symbol(&self, name: &CStr) -> Result<*mut c_void> {
        let p = unsafe { libc::dlsym(self.handle as *mut c_void, name.as_ptr()) };
        ensure!(!p.is_null(), "Missing libtailscale adapter symbol");
        Ok(p)
    }
    unsafe fn result(&self, value: *mut c_char) -> Result<Value> {
        ensure!(!value.is_null(), "Empty Tailscale response");
        let bytes = unsafe { CStr::from_ptr(value) }.to_bytes().to_vec();
        let free: unsafe extern "C" fn(*mut c_char) =
            unsafe { std::mem::transmute(self.symbol(c"PotTsFree")?) };
        unsafe { free(value) };
        let value: Value = serde_json::from_slice(&bytes)?;
        if let Some(error) = value["error"].as_str() {
            anyhow::bail!("{error}");
        }
        Ok(value)
    }
}
// Do not dlclose a Go runtime: goroutines can outlive the last node.
struct Node {
    library: Library,
    id: c_int,
}
pub struct Tailscale {
    node: Mutex<Option<Node>>,
    pub data: PathBuf,
    hostname: String,
    port: u16,
    error: Mutex<String>,
}
impl Tailscale {
    pub fn new(data: PathBuf, hostname: String, port: u16) -> Arc<Self> {
        Arc::new(Self {
            node: Mutex::new(None),
            data,
            hostname,
            port,
            error: Mutex::new(String::new()),
        })
    }
    pub fn configured(&self) -> bool {
        self.data.join("tailscale-enabled").exists()
    }
    pub async fn connect(self: &Arc<Self>) -> Result<()> {
        let this = self.clone();
        tokio::task::spawn_blocking(move || {
            let mut node = this.node.lock().unwrap();
            if node.is_some() {
                return Ok(());
            }
            std::fs::create_dir_all(this.data.join("tailscale"))?;
            use std::os::unix::fs::PermissionsExt;
            std::fs::set_permissions(
                this.data.join("tailscale"),
                std::fs::Permissions::from_mode(0o700),
            )?;
            let library = Library::load()?;
            let dir = CString::new(this.data.join("tailscale").to_string_lossy().as_bytes())?;
            let hostname = CString::new(this.hostname.clone())?;
            let value = unsafe {
                let f: unsafe extern "C" fn(*const c_char, *const c_char) -> *mut c_char =
                    std::mem::transmute(library.symbol(c"PotTsConnect")?);
                library.result(f(dir.as_ptr(), hostname.as_ptr()))?
            };
            *node = Some(Node {
                library,
                id: value["id"].as_i64().context("Missing Tailscale node")? as c_int,
            });
            std::fs::write(this.data.join("tailscale-enabled"), b"enabled")?;
            Ok(())
        })
        .await?
    }
    pub async fn status(self: &Arc<Self>) -> Value {
        let this = self.clone();
        tokio::task::spawn_blocking(move||{
            let node=this.node.lock().unwrap();let Some(node)=node.as_ref() else{return json!({"available":true,"state":"Disconnected","error":this.error.lock().unwrap().clone()});};
            let result=(||unsafe{
                let f:unsafe extern "C" fn(c_int)->*mut c_char=std::mem::transmute(node.library.symbol(c"PotTsStatus")?);
                node.library.result(f(node.id))
            })();
            match result {Ok(mut v)=>{v["available"]=json!(true);v["error"]=json!(this.error.lock().unwrap().clone());v},Err(e)=>json!({"available":true,"state":"Error","error":e.to_string()})}
        }).await.unwrap_or_else(|e|json!({"state":"Error","error":e.to_string()}))
    }
    pub async fn enable(self: &Arc<Self>) -> Result<String> {
        self.connect().await?;
        self.set_web(true, true).await
    }
    pub async fn set_web(self: &Arc<Self>, enabled: bool, https: bool) -> Result<String> {
        let this = self.clone();
        let result = tokio::task::spawn_blocking(move || {
            let node = this.node.lock().unwrap();
            let Some(node) = node.as_ref() else {
                anyhow::ensure!(!enabled, "Connect Tailscale first");
                return Ok("http://localhost".to_owned());
            };
            let value = unsafe {
                let f: unsafe extern "C" fn(c_int, c_int, c_int, c_int) -> *mut c_char =
                    std::mem::transmute(node.library.symbol(c"PotTsSetWeb")?);
                node.library
                    .result(f(node.id, this.port.into(), enabled.into(), https.into()))?
            };
            Ok::<_, anyhow::Error>(
                value["origin"]
                    .as_str()
                    .context("Missing Web address")?
                    .to_owned(),
            )
        })
        .await?;
        *self.error.lock().unwrap() = result
            .as_ref()
            .err()
            .map(ToString::to_string)
            .unwrap_or_default();
        result
    }
    pub async fn disconnect(self: &Arc<Self>) -> Result<()> {
        self.stop().await?;
        if self.configured() {
            std::fs::remove_file(self.data.join("tailscale-enabled"))?;
        }
        Ok(())
    }
    pub async fn stop(self: &Arc<Self>) -> Result<()> {
        let this = self.clone();
        tokio::task::spawn_blocking(move || {
            let mut slot = this.node.lock().unwrap();
            if let Some(node) = slot.take() {
                unsafe {
                    let f: unsafe extern "C" fn(c_int) -> *mut c_char =
                        std::mem::transmute(node.library.symbol(c"PotTsDisconnect")?);
                    node.library.result(f(node.id))?;
                }
            }
            Ok(())
        })
        .await?
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    #[ignore = "requires mise run build:tailscale; performs no network enrollment"]
    fn libtailscale_ffi_loads_and_returns_owned_errors() {
        let library = Library::load().expect("built libtailscale adapter");
        unsafe {
            for name in [
                c"PotTsConnect",
                c"PotTsEnable",
                c"PotTsSetWeb",
                c"PotTsDisconnect",
                c"PotTsFree",
            ] {
                library.symbol(name).unwrap();
            }
            let status: unsafe extern "C" fn(c_int) -> *mut c_char =
                std::mem::transmute(library.symbol(c"PotTsStatus").unwrap());
            let error = library.result(status(-1)).unwrap_err();
            assert!(error.to_string().contains("disconnected"));
        }
    }
}
