//! Real JSON-lines runner tests with isolated homes, no model/VM/provider credentials.
use pot_core::ShellSettings;
use serde_json::{Value, json};
use std::{
    io::{BufRead, BufReader, Write},
    path::PathBuf,
    process::{Child, ChildStdin, ChildStdout, Command, Stdio},
    time::Duration,
};
struct Runner {
    child: Child,
    input: Option<ChildStdin>,
    output: BufReader<ChildStdout>,
    root: PathBuf,
    policy: ShellSettings,
}
impl Runner {
    fn new() -> Self {
        let root = std::env::temp_dir().join(format!("pot-execution-{}", uuid::Uuid::new_v4()));
        std::fs::create_dir_all(root.join("dir")).unwrap();
        let mut child = Command::new(env!("CARGO_BIN_EXE_pot-tool-executor"))
            .env_clear()
            .env("PATH", "/usr/bin:/bin")
            .env("HOME", &root)
            .env("LANG", "C.UTF-8")
            .stdin(Stdio::piped())
            .stdout(Stdio::piped())
            .stderr(Stdio::inherit())
            .spawn()
            .unwrap();
        Self {
            input: child.stdin.take(),
            output: BufReader::new(child.stdout.take().unwrap()),
            child,
            root,
            policy: ShellSettings {
                default_timeout_ms: 2000,
                max_timeout_ms: 5000,
                default_yield_ms: 30,
                max_yield_ms: 1000,
                ..Default::default()
            },
        }
    }
    fn call(&mut self, operation: &str, args: Value) -> Value {
        let request = json!({"id":uuid::Uuid::new_v4().to_string(),"operation":operation,"args":args,"cwd":self.root,"policy":self.policy});
        writeln!(self.input.as_mut().unwrap(), "{request}").unwrap();
        loop {
            let mut line = String::new();
            assert!(
                self.output.read_line(&mut line).unwrap() > 0,
                "runner exited"
            );
            let value: Value = serde_json::from_str(&line).unwrap();
            if let Some(result) = value.get("result") {
                return result.clone();
            }
        }
    }
    fn exec(&mut self, cmd: &str) -> Value {
        self.call("exec_command", json!({"cmd":cmd,"login":false}))
    }
}
impl Drop for Runner {
    fn drop(&mut self) {
        self.input.take();
        let _ = self.child.wait();
        let _ = std::fs::remove_dir_all(&self.root);
    }
}
#[test]
fn separate_commands_keep_explicit_cwd_and_do_not_inherit_exports() {
    let mut r = Runner::new();
    assert_eq!(r.exec("cd dir; export POT_TEST_CHANGED=1")["exit_code"], 0);
    let result = r.exec("pwd; printf '%s' \"${POT_TEST_CHANGED-unset}\"");
    let out = result["stdout"].as_str().unwrap();
    assert!(out.contains(r.root.to_str().unwrap()));
    assert!(out.ends_with("unset"));
    let result = r.call(
        "exec_command",
        json!({"cmd":"pwd","workdir":"dir","login":false}),
    );
    assert!(result["stdout"].as_str().unwrap().trim().ends_with("/dir"));
}
#[test]
fn login_snapshot_reuses_baseline_and_invalidates_after_profile_edit() {
    let mut r = Runner::new();
    let profile = r.root.join(".bash_profile");
    std::fs::write(&profile,"printf x >> \"$HOME/captures\"\nexport POT_BASELINE=first\npot_function() { printf function-ok; }\n").unwrap();
    assert_eq!(
        r.exec("printf '%s' \"${POT_BASELINE-unset}\"")["stdout"],
        "unset"
    );
    for _ in 0..2 {
        let result=r.call("exec_command",json!({"cmd":"printf '%s:' \"$POT_BASELINE\"; pot_function; export POT_BASELINE=mutated"}));
        assert_eq!(result["stdout"], "first:function-ok");
        assert_eq!(result["exit_code"], 0);
    }
    assert_eq!(
        std::fs::read_to_string(r.root.join("captures")).unwrap(),
        "x"
    );
    std::fs::write(
        &profile,
        "printf x >> \"$HOME/captures\"\nexport POT_BASELINE=second\n",
    )
    .unwrap();
    assert_eq!(
        r.call(
            "exec_command",
            json!({"cmd":"printf '%s' \"$POT_BASELINE\""})
        )["stdout"],
        "second"
    );
    assert_eq!(
        std::fs::read_to_string(r.root.join("captures")).unwrap(),
        "xx"
    );
    r.policy.allow_login_shell = false;
    assert!(r.call("exec_command", json!({"cmd":"true","login":true}))["isError"] == true);
}
#[test]
fn failed_snapshot_falls_back_without_repeated_capture_attempts() {
    let mut r = Runner::new();
    // Exceed bounded shell-state storage, while ordinary login execution still works.
    let body = "x".repeat(70_000);
    std::fs::write(
        r.root.join(".bash_profile"),
        format!("printf x >> \"$HOME/captures\"\npot_large() {{ printf '{body}'; }}\n"),
    )
    .unwrap();
    for _ in 0..2 {
        assert_eq!(
            r.call("exec_command", json!({"cmd":"printf fallback-ok"}))["stdout"],
            "fallback-ok"
        );
    }
    assert_eq!(
        std::fs::read_to_string(r.root.join("captures")).unwrap(),
        "xxx"
    );
}
#[test]
fn pty_reports_terminal_accepts_input_resize_and_interrupt() {
    let mut r = Runner::new();
    assert_eq!(
        r.exec("if test -t 0; then printf tty; else printf pipe; fi")["stdout"],
        "pipe"
    );
    let open=r.call("exec_command",json!({"cmd":"test -t 0 || exit 17; trap 'printf INTERRUPTED; exit 0' INT; while IFS= read -r line; do eval \"$line\"; done","tty":true,"login":false,"yield_time_ms":50}));
    let id = open["session_id"].as_str().unwrap().to_owned();
    let result=r.call("write_stdin",json!({"session_id":id,"rows":32,"cols":101,"chars":"stty size; printf 'RESULT_%s\\n' ok\n","yield_time_ms":200}));
    let out = result["stdout"].as_str().unwrap();
    assert!(out.contains("32 101"), "{result}");
    assert!(out.contains("RESULT_ok"), "{result}");
    let result = r.call(
        "write_stdin",
        json!({"session_id":id,"interrupt":true,"yield_time_ms":200}),
    );
    assert!(
        result["stdout"].as_str().unwrap().contains("INTERRUPTED"),
        "{result}"
    );
    assert!(result["session_id"].is_null());
}
#[test]
fn live_sessions_outlast_default_command_timeout_and_expire_when_idle() {
    let mut r = Runner::new();
    r.policy.default_timeout_ms = 20;
    r.policy.session_idle_timeout_ms = 400;
    let open = r.call(
        "exec_command",
        json!({"cmd":"read line; printf 'RECEIVED:%s' \"$line\"","login":false,"yield_time_ms":10}),
    );
    let id = open["session_id"].clone();
    std::thread::sleep(Duration::from_millis(60));
    assert_eq!(r.call("inspect", json!({}))["active"], true);
    let result = r.call(
        "write_stdin",
        json!({"session_id":id,"chars":"hello\n","yield_time_ms":100}),
    );
    assert_eq!(result["stdout"], "RECEIVED:hello");
    let open = r.call(
        "exec_command",
        json!({"cmd":"sleep 10","login":false,"yield_time_ms":10}),
    );
    std::thread::sleep(Duration::from_millis(600));
    let result = r.call(
        "write_stdin",
        json!({"session_id":open["session_id"],"yield_time_ms":50}),
    );
    assert_eq!(result["reason"], "idle_timeout");
    assert!(result["session_id"].is_null());
}
#[test]
fn explicit_timeout_termination_and_pipe_interrupt_settle_processes() {
    let mut r = Runner::new();
    let result = r.call(
        "exec_command",
        json!({"cmd":"sleep 10","login":false,"timeout_ms":20}),
    );
    assert_eq!(result["reason"], "timeout");
    for field in ["interrupt", "terminate"] {
        let open = r.call(
            "exec_command",
            json!({"cmd":"sleep 10","login":false,"yield_time_ms":10}),
        );
        let mut args = json!({"session_id":open["session_id"],"yield_time_ms":200});
        args[field] = json!(true);
        let result = r.call("write_stdin", args);
        assert!(result["session_id"].is_null(), "{result}");
        assert_eq!(r.call("inspect", json!({}))["active"], false);
    }
}

#[test]
fn zsh_login_baseline_preserves_functions_aliases_and_umask_when_available() {
    if !std::path::Path::new("/bin/zsh").exists() {
        return;
    }
    let mut r = Runner::new();
    std::fs::write(r.root.join(".zprofile"), "printf x >> \"$HOME/z-captures\"\nexport POT_ZBASE=ready\npot_zfunction() { printf function; }\nalias pot_zalias='printf alias'\numask 077\n").unwrap();
    for _ in 0..2 {
        let result=r.call("exec_command",json!({"shell":"/bin/zsh","cmd":"printf '%s:' \"$POT_ZBASE\"; pot_zfunction; pot_zalias; printf ':'; umask"}));
        assert_eq!(result["stdout"], "ready:functionalias:077\n");
        assert_eq!(result["exit_code"], 0);
    }
    assert_eq!(
        std::fs::read_to_string(r.root.join("z-captures")).unwrap(),
        "x"
    );
}

#[test]
fn pipe_input_survives_invalid_resize_and_completed_output_can_be_read_later() {
    let mut r = Runner::new();
    let open = r.call(
        "exec_command",
        json!({"cmd":"read line; printf '%s' \"$line\"","login":false,"yield_time_ms":10}),
    );
    let error = r.call(
        "write_stdin",
        json!({"session_id":open["session_id"],"rows":40}),
    );
    assert_eq!(error["isError"], true);
    let result = r.call(
        "write_stdin",
        json!({"session_id":open["session_id"],"chars":"hello\n","yield_time_ms":100}),
    );
    assert_eq!(result["stdout"], "hello");
    let open = r.call(
        "exec_command",
        json!({"cmd":"sleep 0.05; printf delayed","login":false,"yield_time_ms":1}),
    );
    std::thread::sleep(Duration::from_millis(100));
    assert_eq!(r.exec("true")["exit_code"], 0);
    let result = r.call("write_stdin", json!({"session_id":open["session_id"]}));
    assert_eq!(result["stdout"], "delayed");
    assert_eq!(result["exit_code"], 0);
}
