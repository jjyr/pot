//! Owner CLI: local OS permissions, no interactive authentication or root bearer handling.
use anyhow::{Context, Result, bail, ensure};
use serde_json::{Value, json};
use std::{
    path::{Path, PathBuf},
    time::Duration,
};
const HELP: &str = "Pot CLI\nUsage: pot [--root PATH] <command>\n  init                       Create an isolated instance\n  serve                      Run Server and Web UI in this process\n  sandbox enroll-macos ID BUNDLE    Enroll a stopped owner-prepared VM\n  status | doctor | open     Inspect/connect to the local instance\n  access status|token|regenerate-token|qr|enable|disable|connect|disconnect\n  chat TEXT                  Submit a main Agent message\n  tasks list|show ID|cancel ID\n  config get SECTION | set SECTION JSON_FILE\n  config server JSON_FILE    Persist listener/certificate settings (restart required)\n  api PATH [JSON_FILE]       Local API query or mutation\n  service install|start|stop|uninstall   macOS user service\nSections: providers, agent, routing, usage-policy\nRemote clients configure a persistent access Token; this CLI manages the local instance.";
fn main() -> Result<()> {
    let mut args = std::env::args().skip(1).collect::<Vec<_>>();
    if args.first().map(String::as_str) == Some("--version") {
        println!("pot {}", env!("CARGO_PKG_VERSION"));
        return Ok(());
    }
    if matches!(args.first().map(String::as_str), Some("--help" | "-h")) || args.is_empty() {
        println!("{HELP}");
        return Ok(());
    }
    let root = if args.first().map(String::as_str) == Some("--root") {
        ensure!(args.len() >= 3, "--root requires a path and command");
        let root = PathBuf::from(args.remove(1));
        args.remove(0);
        root
    } else {
        pot_server::default_root()
    };
    let root = std::path::absolute(root)?;
    // Configuration/environment is established before any async worker exists.
    unsafe {
        std::env::set_var("POT_LOCAL_ROOT", &root);
        std::env::set_var("POT_DATA_DIR", root.join("product"));
        std::env::set_var("POT_WORKSPACE", root.join("agent-workspace"));
    }
    unsafe {
        pot_server::apply_settings(&root, true)?;
    }
    if let Some(resources) = runtime_resources() {
        unsafe {
            std::env::set_var("POT_BUILTIN_SKILLS_DIR", resources.join("skills"));
            std::env::set_var("POT_MACOS_HELPER", resources.join("PotMacOSHelper"));
            std::env::set_var(
                "PATH",
                format!(
                    "{}:{}:{}",
                    resources.join("node/bin").display(),
                    resources.join("host-tools/node_modules/.bin").display(),
                    std::env::var("PATH").unwrap_or_default()
                ),
            );
        }
    }
    if std::env::var_os("POT_BUILTIN_SKILLS_DIR").is_none()
        && let Some(skills) = std::env::current_exe()?
            .parent()
            .and_then(|p| p.parent())
            .map(|p| p.join("lib/pot/skills"))
            .filter(|p| p.is_dir())
    {
        unsafe {
            std::env::set_var("POT_BUILTIN_SKILLS_DIR", skills);
        }
    }
    tokio::runtime::Runtime::new()?.block_on(run(&root, &args))
}
fn argument(args: &[String], index: usize) -> Result<&str> {
    args.get(index)
        .map(String::as_str)
        .context("Missing argument; use pot --help")
}
fn print(value: Value) -> Result<()> {
    println!("{}", serde_json::to_string_pretty(&value)?);
    Ok(())
}
fn json_file(path: &str) -> Result<Value> {
    Ok(serde_json::from_slice(&std::fs::read(path)?)?)
}
async fn api(root: &Path, path: &str, body: Option<Value>) -> Result<Value> {
    ensure!(
        path.starts_with("/api/"),
        "Only local API paths are allowed"
    );
    let client = reqwest::Client::builder()
        .unix_socket(root.join("product/control.sock"))
        .timeout(Duration::from_secs(120))
        .build()?;
    let url = format!("http://localhost{path}");
    let response = if let Some(body) = body {
        client.post(url).json(&body).send().await?
    } else {
        client.get(url).send().await?
    };
    let status = response.status();
    let value: Value = response.json().await?;
    ensure!(
        status.is_success(),
        "Local operation failed ({status}): {}",
        value["message"]
            .as_str()
            .or(value["code"].as_str())
            .unwrap_or("Request rejected")
    );
    Ok(value)
}
async fn run(root: &Path, args: &[String]) -> Result<()> {
    match argument(args, 0)? {
        "init" => {
            use std::os::unix::fs::PermissionsExt;
            for path in [
                root.to_owned(),
                root.join("product"),
                root.join("agent-workspace"),
            ] {
                std::fs::create_dir_all(&path)?;
                std::fs::set_permissions(path, std::fs::Permissions::from_mode(0o700))?;
            }
            println!(
                "Instance: {}\nStart with: pot --root '{}' serve",
                root.display(),
                root.display()
            );
            Ok(())
        }
        "sandbox" => {
            ensure!(
                argument(args, 1)? == "enroll-macos" && args.len() == 4,
                "Usage: pot sandbox enroll-macos ID BUNDLE"
            );
            let id = argument(args, 2)?;
            // Check the running instance and existing immutable definition before copying.
            let inventory = api(root, "/api/v1/sandboxes", None).await?;
            ensure!(
                !inventory["items"]
                    .as_array()
                    .unwrap_or(&vec![])
                    .iter()
                    .any(|i| i["id"] == id),
                "Sandbox ID already exists"
            );
            pot_sandbox_macos_apple_vm::enroll(root, id, Path::new(argument(args, 3)?))?;
            print(api(root,"/api/v1/sandboxes",Some(json!({"action":"create","sandbox_id":id,"provider":pot_sandbox_macos_apple_vm::PROVIDER_ID}))).await?)
        }
        "serve" => {
            let server = pot_server::Server::bind(pot_server::ServerConfig::from_env()).await?;
            println!(
                "Pot instance: {}\nWeb: {}\nAccess Token: pot --root '{}' access token",
                root.display(),
                if std::env::var_os("POT_TAILSCALE_HOSTNAME").is_some() {
                    "embedded Tailscale (pot access connect, then pot access enable)".into()
                } else {
                    std::env::var("POT_ACCESS_ORIGIN")?
                },
                root.display()
            );
            let _ownership = server.local_client();
            let mut helper = start_helper(root).await?;
            let result = server
                .serve_local(async {
                    let mut term =
                        tokio::signal::unix::signal(tokio::signal::unix::SignalKind::terminate())
                            .expect("signal handler");
                    tokio::select! {_=tokio::signal::ctrl_c()=>{},_=term.recv()=>{}}
                })
                .await;
            if let Some(ref mut child) = helper {
                if let Some(id) = child.id() {
                    unsafe {
                        libc::kill(id as i32, libc::SIGTERM);
                    }
                }
                if tokio::time::timeout(Duration::from_secs(240), child.wait())
                    .await
                    .is_err()
                {
                    child.kill().await?;
                }
            }
            result
        }
        "status" => print(api(root, "/api/v1/snapshot", None).await?),
        "doctor" => {
            let state = api(root, "/api/v1/snapshot", None).await;
            let assets = std::env::var_os("POT_ACCESS_ASSETS").map(PathBuf::from);
            print(
                json!({"instance":root,"server_running":state.is_ok(),"web_origin":std::env::var("POT_ACCESS_ORIGIN")?,"web_embedded":cfg!(feature="embedded-web"),"assets_directory":assets,"sandbox_helper_running":root.join("helper.sock").exists(),"note":"Sandbox execution requires a supported provider; no host execution fallback"}),
            )
        }
        "open" => {
            let state = api(root, "/api/v1/access", None).await?;
            ensure!(
                state["enabled"] == true,
                "Enable Web access before opening it"
            );
            let origin = state["origin"]
                .as_str()
                .context("Web access not configured")?;
            let token = state["token"].as_str().context("Missing access Token")?;
            let url = format!("{origin}/#access_token={token}");
            let command = if cfg!(target_os = "macos") {
                "open"
            } else {
                "xdg-open"
            };
            ensure!(
                std::process::Command::new(command)
                    .arg(url)
                    .status()?
                    .success(),
                "Browser could not be opened"
            );
            Ok(())
        }
        "access" => {
            let command = argument(args, 1)?;
            let mut value = match command {
                "status" | "qr" => api(root, "/api/v1/access", None).await?,
                "token" | "regenerate-token" | "enable" | "disable" | "connect" | "disconnect" => {
                    api(
                        root,
                        "/api/v1/access",
                        Some(json!({"action":command.replace('-',"_")})),
                    )
                    .await?
                }
                _ => bail!("Unknown access command"),
            };
            if command == "status" {
                value.as_object_mut().unwrap().remove("token");
            }
            if command == "qr" {
                let origin = value["origin"].as_str().context("Missing Web origin")?;
                let token = value["token"].as_str().context("Missing access Token")?;
                let url = format!("{origin}/#access_token={token}");
                println!("Connection QR contains access credentials. Do not share.");
                println!(
                    "{}\n{}",
                    url,
                    qrcode::QrCode::new(url.as_bytes())?
                        .render::<qrcode::render::unicode::Dense1x2>()
                        .quiet_zone(true)
                        .build()
                );
                Ok(())
            } else {
                print(value)
            }
        }
        "chat" => {
            let text = args[1..].join(" ");
            ensure!(!text.trim().is_empty(), "Message is required");
            print(api(root,"/api/v1/messages",Some(json!({"conversation_id":"main","request_id":uuid::Uuid::new_v4().to_string(),"text":text}))).await?)
        }
        "tasks" => match argument(args, 1)? {
            "list" => print(api(root, "/api/v1/tasks/query", None).await?),
            "show" => {
                let id = argument(args, 2)?;
                uuid::Uuid::parse_str(id)?;
                print(api(root, &format!("/api/v1/tasks/{id}"), None).await?)
            }
            "cancel" => {
                let id = argument(args, 2)?;
                uuid::Uuid::parse_str(id)?;
                let path = format!("/api/v1/tasks/{id}");
                let snapshot = api(root, &path, None).await?;
                let message = snapshot["turn"]["user_message_id"]
                    .as_str()
                    .context("No active task turn")?;
                print(api(root,&format!("{path}/cancel"),Some(json!({"request_id":uuid::Uuid::new_v4().to_string(),"user_message_id":message}))).await?)
            }
            _ => bail!("Unknown tasks command"),
        },
        "config" => {
            let command = argument(args, 1)?;
            if command == "server" {
                let value = json_file(argument(args, 2)?)?;
                let values = value
                    .as_object()
                    .context("Expected server settings object")?;
                for (key, value) in values {
                    ensure!(
                        matches!(
                            key.as_str(),
                            "POT_TAILSCALE_HOSTNAME"
                                | "POT_ACCESS_ORIGIN"
                                | "POT_ACCESS_BIND"
                                | "POT_ACCESS_CERT"
                                | "POT_ACCESS_KEY"
                                | "POT_ACCESS_ASSETS"
                                | "POT_ACCESS_VAPID_KEY"
                                | "POT_ACCESS_VAPID_CONTACT"
                        ) && value.is_string(),
                        "Unsupported server setting {key}"
                    );
                }
                std::fs::create_dir_all(root)?;
                private_write(
                    &root.join("server.json"),
                    &serde_json::to_vec_pretty(&value)?,
                )?;
                println!("Server settings saved; restart the instance to apply");
                return Ok(());
            }
            let section = argument(args, 2)?;
            let path = match section {
                "providers" => "/api/v1/providers",
                "agent" => "/api/v1/agent",
                "routing" => "/api/v1/agent/routing",
                "usage-policy" => {
                    if command == "get" {
                        "/api/v1/usage/summary"
                    } else {
                        "/api/v1/usage/policy"
                    }
                }
                _ => bail!("Unknown configuration section"),
            };
            let body = match command {
                "get" => None,
                "set" => Some(json_file(argument(args, 3)?)?),
                _ => bail!("Use config get or set"),
            };
            print(api(root, path, body).await?)
        }
        "api" => print(
            api(
                root,
                argument(args, 1)?,
                args.get(2).map(|p| json_file(p)).transpose()?,
            )
            .await?,
        ),
        "service" => service(root, argument(args, 1)?),
        _ => bail!("Unknown command; use pot --help"),
    }
}
fn private_write(path: &Path, bytes: &[u8]) -> Result<()> {
    use std::{io::Write, os::unix::fs::OpenOptionsExt};
    let temp = path.with_extension("tmp");
    let mut file = std::fs::OpenOptions::new()
        .create(true)
        .truncate(true)
        .write(true)
        .mode(0o600)
        .open(&temp)?;
    file.write_all(bytes)?;
    file.sync_all()?;
    std::fs::rename(temp, path)?;
    Ok(())
}
fn service(root: &Path, command: &str) -> Result<()> {
    ensure!(
        cfg!(target_os = "macos"),
        "Service installation currently supports macOS only"
    );
    let home = PathBuf::from(std::env::var_os("HOME").context("HOME unavailable")?);
    let file = home.join("Library/LaunchAgents/com.jjyr.pot.server.plist");
    // A per-user service owns one configured root. Do not overwrite an existing definition.
    match command {
        "install" => {
            ensure!(
                !file.exists(),
                "Pot service already installed; uninstall it before changing its root"
            );
            std::fs::create_dir_all(file.parent().unwrap())?;
            std::fs::create_dir_all(root)?;
            let escape = |s: String| {
                s.replace('&', "&amp;")
                    .replace('<', "&lt;")
                    .replace('>', "&gt;")
                    .replace('"', "&quot;")
            };
            let log = root.join("server.log");
            if !log.exists() {
                private_write(&log, b"")?;
            }
            let exe = escape(std::env::current_exe()?.display().to_string());
            let directory = escape(root.display().to_string());
            private_write(&file,format!(r#"<?xml version="1.0" encoding="UTF-8"?><!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd"><plist version="1.0"><dict><key>Label</key><string>com.jjyr.pot.server</string><key>ProgramArguments</key><array><string>{exe}</string><string>--root</string><string>{directory}</string><string>serve</string></array><key>RunAtLoad</key><true/><key>KeepAlive</key><true/><key>StandardOutPath</key><string>{directory}/server.log</string><key>StandardErrorPath</key><string>{directory}/server.log</string></dict></plist>"#).as_bytes())?;
            println!("Service installed; run pot service start");
            Ok(())
        }
        "start" | "stop" => {
            ensure!(file.exists(), "Service is not installed");
            let uid = std::process::Command::new("id").arg("-u").output()?;
            let domain = format!("gui/{}", String::from_utf8(uid.stdout)?.trim());
            let status = std::process::Command::new("launchctl")
                .args([
                    if command == "start" {
                        "bootstrap"
                    } else {
                        "bootout"
                    },
                    &domain,
                ])
                .arg(&file)
                .status()?;
            ensure!(status.success(), "launchctl failed");
            Ok(())
        }
        "uninstall" => {
            ensure!(file.exists(), "Service is not installed");
            service(root, "stop")?;
            std::fs::remove_file(file)?;
            Ok(())
        }
        _ => bail!("Unknown service command"),
    }
}

fn runtime_resources() -> Option<PathBuf> {
    std::env::var_os("POT_RUNTIME_RESOURCES")
        .map(PathBuf::from)
        .or_else(|| {
            std::env::current_exe()
                .ok()
                .and_then(|p| {
                    p.parent()
                        .and_then(|p| p.parent())
                        .map(|p| p.join("lib/pot/runtime"))
                })
                .filter(|p| p.is_dir())
        })
}
async fn start_helper(root: &Path) -> Result<Option<tokio::process::Child>> {
    let Some(resources) = runtime_resources() else {
        return Ok(None);
    };
    let helper = std::env::var_os("POT_HELPER")
        .map(PathBuf::from)
        .unwrap_or_else(|| resources.parent().unwrap().join("PotHelper"));
    ensure!(
        cfg!(target_os = "macos") && helper.is_file(),
        "Sandbox resources require the macOS PotHelper provider"
    );
    ensure!(
        !root.join("helper.sock").exists(),
        "A Helper socket already exists; confirm the previous provider stopped"
    );
    use std::os::unix::fs::OpenOptionsExt;
    let log = std::fs::OpenOptions::new()
        .create(true)
        .append(true)
        .mode(0o600)
        .open(root.join("helper.log"))?;
    let child = tokio::process::Command::new(helper)
        .arg(root)
        .arg(resources)
        .arg("--sandbox-only")
        .stdout(std::process::Stdio::from(log.try_clone()?))
        .stderr(std::process::Stdio::from(log))
        .kill_on_drop(true)
        .spawn()?;
    Ok(Some(child))
}
