//! Contract test with a fixture subprocess. Does not claim official CLI/account acceptance.
use anyhow::Result;
use futures::future::BoxFuture;
use pot_core::*;
use pot_driver::claude::ClaudeHost;
use serde_json::Value;
use std::{
    os::unix::fs::PermissionsExt,
    sync::{
        Arc, Mutex,
        atomic::{AtomicBool, Ordering},
    },
    time::Duration,
};
#[derive(Default)]
struct Feedback {
    observations: Mutex<Vec<String>>,
    stop: AtomicBool,
    notify: tokio::sync::Notify,
}
impl Feedback {
    fn observe(&self, value: impl Into<String>) {
        self.observations.lock().unwrap().push(value.into());
    }
}
impl ExecutionFeedback for Feedback {
    fn cancelled(&self) -> bool {
        self.stop.load(Ordering::SeqCst)
    }
    fn cancellation(&self) -> BoxFuture<'_, ()> {
        Box::pin(self.notify.notified())
    }
    fn native_identity(&self, id: &str) -> Result<()> {
        self.observe(format!("identity:{id}"));
        Ok(())
    }
    fn binding_broken(&self, _: &str) -> Result<()> {
        anyhow::bail!("Unexpected broken binding")
    }
    fn needs_rebind(&self, _: &str) -> Result<()> {
        anyhow::bail!("Unexpected rebind")
    }
    fn record_turn(&self, request: &str) -> Result<()> {
        self.observe(format!("turn:{request}"));
        Ok(())
    }
    fn input_streaming(&self, _: &str) -> Result<()> {
        Ok(())
    }
    fn begin_usage(&self, _: &str, _: &ResolvedSpec, _: u64) -> Result<()> {
        self.observe("begin_usage");
        Ok(())
    }
    fn initialize_usage(&self, _: &str) -> Result<()> {
        Ok(())
    }
    fn cumulative_usage(&self, _: &str, _: &str, _: &Value) -> Result<()> {
        anyhow::bail!("Unexpected cumulative usage")
    }
    fn final_usage(&self, _: &str, engine: Engine, usage: &Value) -> Result<()> {
        assert_eq!(engine, Engine::Claude);
        assert_eq!(usage["output_tokens"], 5);
        self.observe("final_usage");
        Ok(())
    }
    fn incomplete_usage(&self, _: &str) -> Result<()> {
        self.observe("incomplete_usage");
        Ok(())
    }
}
struct Config;
impl LegacyProfileConfig for Config {
    fn model(&self) -> &str {
        "claude-fixture"
    }
    fn max_tokens(&self) -> u64 {
        100
    }
}
#[tokio::test]
async fn driver_executes_and_cancels_with_only_context_and_feedback() {
    let root = std::env::temp_dir().join(format!("pot-claude-driver-{}", uuid::Uuid::new_v4()));
    std::fs::create_dir_all(&root).unwrap();
    let executable = root.join("claude-fixture");
    std::fs::write(&executable, r#"#!/usr/bin/env python3
import json, signal, sys, time
args=sys.argv[1:]
if args == ['--help']:
    print('--system-prompt-snapshot --strict-mcp-config --setting-sources --tools --include-partial-messages --prompt-suggestions'); sys.exit(0)
assert '--strict-mcp-config' in args
assert args[args.index('--setting-sources')+1] == ''
assert 'Agent' not in args[args.index('--tools')+1]
identity=args[args.index('--resume')+1] if '--resume' in args else args[args.index('--session-id')+1]
text=sys.stdin.read()
def emit(value): print(json.dumps({'session_id':identity, **value}), flush=True)
signal.signal(signal.SIGINT, lambda *_: sys.exit(0))
emit({'type':'system','subtype':'init','model':'claude-fixture','capabilities':['interrupt_receipt_v1']})
if text == 'WAIT':
    while True: time.sleep(0.05)
emit({'type':'assistant','message':{'content':[{'type':'text','text':'fixture result'}]}})
emit({'type':'result','is_error':False,'usage':{'input_tokens':10,'output_tokens':5,'cache_read_input_tokens':0,'cache_creation_input_tokens':0}})
"#).unwrap();
    std::fs::set_permissions(&executable, std::fs::Permissions::from_mode(0o700)).unwrap();
    let mut profile = Profile::legacy(&Config);
    profile.engine = Engine::Claude;
    profile.auth_ref = "cli:claude".into();
    profile.launch_spec = Some(LaunchSpec {
        executable: executable.to_string_lossy().into_owned(),
        ..LaunchSpec::claude()
    });
    let identity = uuid::Uuid::new_v4().to_string();
    let mut binding = Binding {
        pot_owner: "fixture".into(),
        epoch: 1,
        spec: profile.resolve().unwrap(),
        native_ref: NativeRef::Claude {
            session_id: identity.clone(),
            started: false,
        },
        schema_version: SCHEMA_VERSION,
        state: BindingState::Active,
        model_switches: vec![],
        handoff_ref: None,
        failure: None,
    };
    let feedback = Arc::new(Feedback::default());
    let context = ExecutionContext {
        data: root.clone(),
        workspace: root.clone(),
        owner: "fixture".into(),
        role: ExecutionRole::Worker,
        backend_url: "http://127.0.0.1:1".into(),
        lane_token: "fixture-lane-token".into(),
        pi_program: "unused".into(),
        main_prompt: String::new(),
        model_api: String::new(),
        feedback: feedback.clone(),
    };
    let mut session = ClaudeHost
        .session(context.clone(), binding.clone())
        .unwrap();
    let mut updates = vec![];
    session
        .start_turn(
            &TurnRequest {
                request_id: "first".into(),
                text: "GO".into(),
            },
            false,
            &mut |e| {
                updates.push(e);
                Ok(())
            },
        )
        .await
        .unwrap();
    assert!(updates.iter().any(
        |e| matches!(&e.update, Update::MessageFinished { text, .. } if text == "fixture result")
    ));
    assert!(matches!(
        updates.last().unwrap().update,
        Update::TurnFinished(Outcome::Success)
    ));
    assert!(
        feedback
            .observations
            .lock()
            .unwrap()
            .contains(&"final_usage".into())
    );
    binding.native_ref = NativeRef::Claude {
        session_id: identity.clone(),
        started: true,
    };
    let mut session = ClaudeHost.session(context, binding).unwrap();
    let before = feedback.observations.lock().unwrap().len();
    let cancel_feedback = feedback.clone();
    let cancel = async move {
        tokio::time::timeout(Duration::from_secs(5), async {
            loop {
                if cancel_feedback.observations.lock().unwrap()[before..]
                    .contains(&format!("identity:{identity}"))
                {
                    break;
                }
                tokio::time::sleep(Duration::from_millis(5)).await;
            }
        })
        .await
        .unwrap();
        cancel_feedback.stop.store(true, Ordering::SeqCst);
        cancel_feedback.notify.notify_one();
    };
    let mut events = vec![];
    let mut sink = |e| {
        events.push(e);
        Ok(())
    };
    let request = TurnRequest {
        request_id: "second".into(),
        text: "WAIT".into(),
    };
    let (result, ()) = tokio::join!(session.start_turn(&request, false, &mut sink), cancel);
    result.unwrap();
    assert!(matches!(
        events.last().unwrap().update,
        Update::TurnFinished(Outcome::Cancelled)
    ));
    assert!(
        feedback
            .observations
            .lock()
            .unwrap()
            .contains(&"incomplete_usage".into())
    );
    std::fs::remove_dir_all(root).unwrap();
}
