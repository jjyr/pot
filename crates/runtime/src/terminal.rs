//! Ephemeral PTYs. No terminal bytes enter the product store or logs.
use crate::*;
use axum::extract::{
    Path,
    ws::{Message, WebSocket, WebSocketUpgrade},
};
use std::{
    collections::{HashMap, VecDeque},
    fs::File,
    io::{Read, Write},
    os::fd::{AsRawFd, FromRawFd},
    sync::atomic::{AtomicBool, Ordering},
};
use tokio::process::Command;
use tokio::{
    io::unix::AsyncFd,
    sync::{Notify, watch},
};

const MAX_SESSIONS: usize = 8;
const HISTORY: usize = 65536;
#[derive(Default)]
pub struct Hub {
    sessions: HashMap<String, Arc<Session>>,
}
struct Session {
    purpose: Mutex<String>,
    master: Arc<AsyncFd<File>>,
    output: broadcast::Sender<Vec<u8>>,
    history: Mutex<VecDeque<u8>>,
    exit: watch::Receiver<Option<i32>>,
    close: Notify,
    attached: AtomicBool,
}
impl Drop for Hub {
    fn drop(&mut self) {
        for session in self.sessions.values() {
            session.close.notify_one();
        }
    }
}
#[derive(Deserialize)]
pub struct Create {
    #[serde(flatten)]
    size: Size,
    agent: Option<String>,
    #[serde(default)]
    purpose: Option<String>,
}
#[derive(Deserialize)]
pub struct Size {
    cols: u16,
    rows: u16,
}
impl Size {
    fn validate(&self) -> Result<()> {
        anyhow::ensure!(
            (1..=500).contains(&self.cols) && (1..=300).contains(&self.rows),
            "Invalid terminal dimensions"
        );
        Ok(())
    }
    fn native(&self) -> libc::winsize {
        libc::winsize {
            ws_row: self.rows,
            ws_col: self.cols,
            ws_xpixel: 0,
            ws_ypixel: 0,
        }
    }
}
fn authorize(s: &Service, headers: &HeaderMap) -> ApiResult<()> {
    s.authorized(headers)?;
    // This endpoint is for the native bearer bridge only. Web content cannot
    // access the bearer, and arbitrary browser origins may not connect.
    if s.is_worker || headers.contains_key("origin") {
        return Err(error(
            StatusCode::FORBIDDEN,
            "terminal_origin",
            "Use the native terminal bridge",
        ));
    }
    Ok(())
}
pub async fn list(State(s): State<Service>, h: HeaderMap) -> ApiResult<Json<Value>> {
    authorize(&s, &h)?;
    let hub = s.environment_service.terminals.lock().unwrap();
    Ok(Json(
        json!({"boot_id":s.boot,"sessions":hub.sessions.iter().map(|(id,p)|json!({"id":id,"exit_code":*p.exit.borrow(),"attached":p.attached.load(Ordering::SeqCst),"purpose":*p.purpose.lock().unwrap()})).collect::<Vec<_>>()}),
    ))
}
pub async fn create(
    State(s): State<Service>,
    h: HeaderMap,
    Json(request): Json<Create>,
) -> ApiResult<Json<Value>> {
    authorize(&s, &h)?;
    let size = request.size;
    size.validate()
        .map_err(|e| error(StatusCode::BAD_REQUEST, "terminal_size", &e.to_string()))?;
    if request.agent.is_some() {
        return Err(error(
            StatusCode::BAD_REQUEST,
            "terminal_command",
            "Terminal opens a shell only; commands are entered by the user",
        ));
    }
    let purpose = request.purpose.unwrap_or_else(|| "shell".into());
    if !["shell", "login"].contains(&purpose.as_str()) {
        return Err(error(
            StatusCode::BAD_REQUEST,
            "terminal_purpose",
            "Invalid terminal purpose",
        ));
    }
    let mut hub = s.environment_service.terminals.lock().unwrap();
    if hub.sessions.len() >= MAX_SESSIONS {
        return Err(error(
            StatusCode::CONFLICT,
            "terminal_limit",
            "Close an existing terminal first",
        ));
    }
    let session = spawn(&s.workspace, &size).map_err(internal)?;
    *session.purpose.lock().unwrap() = purpose;
    let id = id();
    hub.sessions.insert(id.clone(), session);
    Ok(Json(json!({"id":id,"boot_id":s.boot})))
}
pub async fn close(
    State(s): State<Service>,
    h: HeaderMap,
    Path(id): Path<String>,
) -> ApiResult<Json<Value>> {
    authorize(&s, &h)?;
    let session = s
        .environment_service
        .terminals
        .lock()
        .unwrap()
        .sessions
        .get(&id)
        .cloned()
        .ok_or_else(|| {
            error(
                StatusCode::NOT_FOUND,
                "terminal_missing",
                "Terminal ended or Runtime restarted",
            )
        })?;
    session.close.notify_one();
    let mut exit = session.exit.clone();
    while exit.borrow().is_none() {
        if exit.changed().await.is_err() {
            break;
        }
    }
    s.environment_service
        .terminals
        .lock()
        .unwrap()
        .sessions
        .remove(&id);
    Ok(Json(json!({"closed":true})))
}
pub async fn connect(
    State(s): State<Service>,
    h: HeaderMap,
    Path(id): Path<String>,
    ws: WebSocketUpgrade,
) -> ApiResult<impl IntoResponse> {
    authorize(&s, &h)?;
    let session = s
        .environment_service
        .terminals
        .lock()
        .unwrap()
        .sessions
        .get(&id)
        .cloned()
        .ok_or_else(|| {
            error(
                StatusCode::NOT_FOUND,
                "terminal_missing",
                "Terminal ended or Runtime restarted",
            )
        })?;
    if session.attached.swap(true, Ordering::SeqCst) {
        return Err(error(
            StatusCode::CONFLICT,
            "terminal_attached",
            "Terminal already connected",
        ));
    }
    let failure = session.clone();
    Ok(ws
        .max_message_size(16384)
        .max_frame_size(16384)
        .on_failed_upgrade(move |_| {
            failure.attached.store(false, Ordering::SeqCst);
        })
        .on_upgrade(move |socket| bridge(socket, session)))
}
fn kill_groups(pid: i32, fd: i32) {
    // Interactive job control gives foreground programs their own group.
    let foreground = unsafe { libc::tcgetpgrp(fd) };
    unsafe {
        if foreground > 0 {
            libc::kill(-foreground, libc::SIGKILL);
        }
        libc::kill(-pid, libc::SIGKILL);
    }
    // The guest is Linux. Include background job groups in this PTY session.
    #[cfg(target_os = "linux")]
    if let Ok(entries) = std::fs::read_dir("/proc") {
        for entry in entries.flatten() {
            let Ok(stat) = std::fs::read_to_string(entry.path().join("stat")) else {
                continue;
            };
            let Some((_, fields)) = stat.rsplit_once(") ") else {
                continue;
            };
            let fields: Vec<_> = fields.split_whitespace().collect();
            if fields.get(3).and_then(|v| v.parse::<i32>().ok()) == Some(pid)
                && let Some(group) = fields
                    .get(2)
                    .and_then(|v| v.parse::<i32>().ok())
                    .filter(|v| *v > 0)
            {
                unsafe {
                    libc::kill(-group, libc::SIGKILL);
                }
            }
        }
    }
}
pub async fn shutdown(s: &Service) {
    let sessions: Vec<_> = s
        .environment_service
        .terminals
        .lock()
        .unwrap()
        .sessions
        .values()
        .cloned()
        .collect();
    for session in &sessions {
        session.close.notify_one();
    }
    for session in sessions {
        let mut exit = session.exit.clone();
        while exit.borrow().is_none() {
            if exit.changed().await.is_err() {
                break;
            }
        }
    }
}
fn spawn(cwd: &std::path::Path, size: &Size) -> Result<Arc<Session>> {
    let (mut master, mut slave) = (-1, -1);
    let mut native = size.native();
    // Both descriptors immediately acquire RAII ownership; no inherited master.
    if unsafe {
        libc::openpty(
            &mut master,
            &mut slave,
            std::ptr::null_mut(),
            std::ptr::null_mut(),
            // libc uses a const winsize pointer on Linux and a mutable one on macOS.
            &raw mut native,
        )
    } != 0
    {
        return Err(std::io::Error::last_os_error().into());
    }
    let master = unsafe { File::from_raw_fd(master) };
    let slave = unsafe { File::from_raw_fd(slave) };
    unsafe {
        if libc::fcntl(master.as_raw_fd(), libc::F_SETFD, libc::FD_CLOEXEC) < 0
            || libc::fcntl(slave.as_raw_fd(), libc::F_SETFD, libc::FD_CLOEXEC) < 0
            || libc::fcntl(master.as_raw_fd(), libc::F_SETFL, libc::O_NONBLOCK) < 0
        {
            return Err(std::io::Error::last_os_error().into());
        }
    }
    let mut command = Command::new("/bin/bash");
    command.args(["--noprofile", "--norc", "-i"]);
    command
        .env("HISTFILE", "/dev/null")
        .current_dir(cwd)
        .env("TERM", "xterm-256color")
        .stdin(slave.try_clone()?)
        .stdout(slave.try_clone()?)
        .stderr(slave)
        .kill_on_drop(true);
    unsafe {
        command.pre_exec(|| {
            if libc::setsid() < 0 || libc::ioctl(0, libc::TIOCSCTTY as _, 0) < 0 {
                return Err(std::io::Error::last_os_error());
            }
            Ok(())
        });
    }
    let mut child = command.spawn()?;
    let pid = child.id().unwrap() as i32;
    let (output, _) = broadcast::channel(32);
    let (done, exit) = watch::channel(None);
    let session = Arc::new(Session {
        purpose: Mutex::new("shell".into()),
        master: Arc::new(AsyncFd::new(master)?),
        output,
        history: Mutex::new(VecDeque::new()),
        exit,
        close: Notify::new(),
        attached: AtomicBool::new(false),
    });
    let drained = Arc::new(Notify::new());
    let reader_drained = drained.clone();
    let owner = session.clone();
    tokio::spawn(async move {
        let status = tokio::select! {
            status = child.wait() => status,
            _ = owner.close.notified() => {
                kill_groups(pid, owner.master.as_raw_fd());
                child.wait().await
            }
        };
        // Also settle background children left after the interactive shell exits.
        kill_groups(pid, owner.master.as_raw_fd());
        let _ = tokio::time::timeout(std::time::Duration::from_secs(1), drained.notified()).await;
        let _ = done.send(Some(status.ok().and_then(|s| s.code()).unwrap_or(-1)));
    });
    let reader = session.clone();
    tokio::spawn(async move {
        let mut buffer = [0; 4096];
        loop {
            let ready = reader.master.readable().await;
            let Ok(mut ready) = ready else { break };
            match ready.try_io(|fd| fd.get_ref().read(&mut buffer)) {
                Ok(Ok(0)) | Ok(Err(_)) => break,
                Ok(Ok(n)) => {
                    let mut history = reader.history.lock().unwrap();
                    history.extend(&buffer[..n]);
                    while history.len() > HISTORY {
                        history.pop_front();
                    }
                    let _ = reader.output.send(buffer[..n].to_vec());
                }
                Err(_) => continue,
            }
        }
        reader_drained.notify_one();
    });
    Ok(session)
}
async fn write(session: &Session, mut bytes: &[u8]) -> Result<()> {
    while !bytes.is_empty() {
        let mut ready = session.master.writable().await?;
        match ready.try_io(|fd| fd.get_ref().write(bytes)) {
            Ok(Ok(0)) => bail!("Terminal closed"),
            Ok(Ok(n)) => bytes = &bytes[n..],
            Ok(Err(e)) => return Err(e.into()),
            Err(_) => continue,
        }
    }
    Ok(())
}
async fn bridge(mut socket: WebSocket, session: Arc<Session>) {
    struct Attached(Arc<Session>);
    impl Drop for Attached {
        fn drop(&mut self) {
            self.0.attached.store(false, Ordering::SeqCst);
        }
    }
    let _attached = Attached(session.clone());
    let (mut output, history) = {
        let history = session.history.lock().unwrap();
        (
            session.output.subscribe(),
            history.iter().copied().collect::<Vec<_>>(),
        )
    };
    if socket.send(Message::Binary(history.into())).await.is_err() {
        return;
    }
    let mut exit = session.exit.clone();
    loop {
        let code = *exit.borrow();
        if let Some(code) = code {
            while let Ok(bytes) = output.try_recv() {
                if socket.send(Message::Binary(bytes.into())).await.is_err() {
                    return;
                }
            }
            let _ = socket
                .send(Message::Text(json!({"exit_code":code}).to_string().into()))
                .await;
            break;
        }
        tokio::select! {
            _ = exit.changed() => {},
            data = output.recv() => match data {
                Ok(bytes) => if socket.send(Message::Binary(bytes.into())).await.is_err() { break; },
                Err(_) => break, // Slow clients detach; bounded memory, reconnect replays history.
            },
            input = socket.recv() => match input {
                Some(Ok(Message::Binary(bytes))) => if write(&session,&bytes).await.is_err() { break; },
                Some(Ok(Message::Text(text))) => {
                    let Ok(size) = serde_json::from_str::<Size>(&text) else { break };
                    if size.validate().is_err() { break; }
                    if unsafe { libc::ioctl(session.master.as_raw_fd(),libc::TIOCSWINSZ,&size.native()) } < 0 { break; }
                },
                Some(Ok(Message::Ping(_)|Message::Pong(_))) => {},
                _ => break,
            }
        }
    }
}
#[cfg(test)]
mod tests {
    use super::*;
    #[tokio::test]
    async fn pty_io_detach_and_explicit_close() {
        let p = spawn(std::path::Path::new("/tmp"), &Size { cols: 80, rows: 24 }).unwrap();
        let mut rx = p.output.subscribe();
        write(
            &p,
            b"printf 'terminal-\\165nicode-\\344\\270\\255\\346\\226\\207\\n'\n",
        )
        .await
        .unwrap();
        let mut data = Vec::new();
        tokio::time::timeout(std::time::Duration::from_secs(5), async {
            loop {
                data.extend(rx.recv().await.unwrap());
                if String::from_utf8_lossy(&data).contains("terminal-unicode-中文") {
                    break;
                }
            }
        })
        .await
        .unwrap();
        drop(rx);
        assert!(p.exit.borrow().is_none());
        p.close.notify_one();
        let mut exit = p.exit.clone();
        tokio::time::timeout(std::time::Duration::from_secs(5), async {
            while exit.borrow().is_none() {
                exit.changed().await.unwrap();
            }
        })
        .await
        .unwrap();
    }
    #[test]
    fn native_auth_only_and_dimensions_bounded() {
        let s = crate::tests::service();
        let mut h = HeaderMap::new();
        assert!(authorize(&s, &h).is_err());
        h.insert(
            "authorization",
            format!("Bearer {}", s.token).parse().unwrap(),
        );
        assert!(authorize(&s, &h).is_ok());
        h.insert("origin", "https://attacker.invalid".parse().unwrap());
        assert!(authorize(&s, &h).is_err());
        assert!(Size { cols: 0, rows: 24 }.validate().is_err());
    }
}

#[cfg(test)]
mod lifecycle_tests {
    use super::*;
    fn auth(s: &Service) -> HeaderMap {
        let mut headers = HeaderMap::new();
        headers.insert(
            "authorization",
            format!("Bearer {}", s.token).parse().unwrap(),
        );
        headers
    }
    #[tokio::test]
    async fn login_and_shell_sessions_are_distinct_without_command_injection() {
        let mut s = crate::tests::service();
        s.workspace = std::env::temp_dir();
        let make = |purpose| {
            Json(Create {
                size: Size { cols: 80, rows: 24 },
                agent: None,
                purpose: Some(purpose),
            })
        };
        let shell = create(State(s.clone()), auth(&s), make("shell".into()))
            .await
            .unwrap()
            .0;
        let login = create(State(s.clone()), auth(&s), make("login".into()))
            .await
            .unwrap()
            .0;
        assert_ne!(shell["id"], login["id"]);
        let public = list(State(s.clone()), auth(&s)).await.unwrap().0;
        assert!(
            public["sessions"]
                .as_array()
                .unwrap()
                .iter()
                .any(|p| p["id"] == login["id"] && p["purpose"] == "login")
        );
        let _ = close(
            State(s.clone()),
            auth(&s),
            Path(login["id"].as_str().unwrap().into()),
        )
        .await
        .unwrap();
        assert!(
            s.environment_service
                .terminals
                .lock()
                .unwrap()
                .sessions
                .contains_key(shell["id"].as_str().unwrap())
        );
        let _ = close(
            State(s.clone()),
            auth(&s),
            Path(shell["id"].as_str().unwrap().into()),
        )
        .await
        .unwrap();
    }
    #[tokio::test]
    async fn login_requests_are_rejected_without_starting_or_injecting_commands() {
        let mut s = crate::tests::service();
        s.workspace = std::env::temp_dir();
        let request = |agent| {
            Json(Create {
                size: Size { cols: 80, rows: 24 },
                agent,
                purpose: None,
            })
        };
        let shell = create(State(s.clone()), auth(&s), request(None))
            .await
            .unwrap()
            .0;
        let session = s.environment_service.terminals.lock().unwrap().sessions
            [shell["id"].as_str().unwrap()]
        .clone();
        assert!(
            create(
                State(s.clone()),
                auth(&s),
                request(Some("unsupported".into()))
            )
            .await
            .is_err()
        );
        assert_eq!(
            s.environment_service
                .terminals
                .lock()
                .unwrap()
                .sessions
                .len(),
            1
        );
        // A rejected command-injection request never creates another process or writes PTY input.
        shutdown(&s).await;
        assert!(session.exit.borrow().is_some());
    }
    #[tokio::test]
    async fn ownership_limits_and_resize_are_checked() {
        let mut s = crate::tests::service();
        s.workspace = std::env::temp_dir();
        let request = || {
            Json(Create {
                size: Size { cols: 80, rows: 24 },
                agent: None,
                purpose: None,
            })
        };
        assert!(
            create(State(s.clone()), HeaderMap::new(), request())
                .await
                .is_err()
        );
        for _ in 0..MAX_SESSIONS {
            let _ = create(State(s.clone()), auth(&s), request()).await.unwrap();
        }
        assert!(create(State(s.clone()), auth(&s), request()).await.is_err());
        let id = s
            .environment_service
            .terminals
            .lock()
            .unwrap()
            .sessions
            .keys()
            .next()
            .unwrap()
            .clone();
        let _ = close(State(s.clone()), auth(&s), Path(id.clone()))
            .await
            .unwrap();
        assert!(
            !s.environment_service
                .terminals
                .lock()
                .unwrap()
                .sessions
                .contains_key(&id)
        );
        let _ = create(State(s.clone()), auth(&s), request()).await.unwrap();
        shutdown(&s).await;
    }
}

#[cfg(test)]
mod websocket_tests {
    use super::*;
    use futures::{SinkExt, StreamExt};
    use tokio_tungstenite::{
        connect_async,
        tungstenite::{Message as ClientMessage, client::IntoClientRequest},
    };
    #[tokio::test]
    async fn authenticated_websocket_resize_unicode_and_reconnect() {
        let mut s = crate::tests::service();
        s.workspace = std::env::temp_dir();
        let session = spawn(&s.workspace, &Size { cols: 80, rows: 24 }).unwrap();
        s.environment_service
            .terminals
            .lock()
            .unwrap()
            .sessions
            .insert("test".into(), session.clone());
        let router = Router::new()
            .route("/term/{id}", get(connect))
            .with_state(s.clone());
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let url = format!("ws://{}/term/test", listener.local_addr().unwrap());
        let server = tokio::spawn(async move {
            axum::serve(listener, router).await.unwrap();
        });
        assert!(connect_async(&url).await.is_err());
        let request = || {
            let mut r = url.clone().into_client_request().unwrap();
            r.headers_mut().insert(
                "authorization",
                format!("Bearer {}", s.token).parse().unwrap(),
            );
            r
        };
        let mut foreign = request();
        foreign
            .headers_mut()
            .insert("origin", "https://untrusted.invalid".parse().unwrap());
        assert!(connect_async(foreign).await.is_err());
        let (mut socket, _) = connect_async(request()).await.unwrap();
        assert!(connect_async(request()).await.is_err());
        // Wait for interactive shell initialization before resizing. Otherwise
        // readline startup can restore the initial dimensions after our ioctl.
        socket
            .send(ClientMessage::Binary(
                b"printf 'TERMINAL_%s\\n' ready\n".to_vec().into(),
            ))
            .await
            .unwrap();
        let mut startup = Vec::new();
        tokio::time::timeout(std::time::Duration::from_secs(5), async {
            while let Some(Ok(message)) = socket.next().await {
                if let ClientMessage::Binary(bytes) = message {
                    startup.extend(bytes);
                }
                if String::from_utf8_lossy(&startup).contains("TERMINAL_ready") {
                    break;
                }
            }
        })
        .await
        .unwrap_or_else(|e| panic!("shell startup: {e}; output: {startup:?}"));
        socket
            .send(ClientMessage::Text("{\"cols\":91,\"rows\":32}".into()))
            .await
            .unwrap();
        socket
            .send(ClientMessage::Binary(
                b"printf '\\344\\270\\255\\346\\226\\207'; stty size\n"
                    .to_vec()
                    .into(),
            ))
            .await
            .unwrap();
        let mut all = Vec::new();
        tokio::time::timeout(std::time::Duration::from_secs(5), async {
            while let Some(Ok(message)) = socket.next().await {
                if let ClientMessage::Binary(bytes) = message {
                    all.extend(bytes);
                }
                let text = String::from_utf8_lossy(&all);
                if text.contains("中文") && text.contains("32 91") {
                    break;
                }
            }
        })
        .await
        .unwrap_or_else(|e| panic!("terminal resize/output: {e}; output: {all:?}"));
        socket.close(None).await.unwrap();
        drop(socket);
        tokio::time::timeout(std::time::Duration::from_secs(5), async {
            while session.attached.load(Ordering::SeqCst) {
                tokio::time::sleep(std::time::Duration::from_millis(10)).await;
            }
        })
        .await
        .unwrap();
        assert!(session.exit.borrow().is_none());
        let (mut reconnected, _) = connect_async(request()).await.unwrap();
        let history = reconnected.next().await.unwrap().unwrap().into_data();
        assert!(String::from_utf8_lossy(&history).contains("中文"));
        shutdown(&s).await;
        server.abort();
    }
}
