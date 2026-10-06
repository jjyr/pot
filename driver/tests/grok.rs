//! ACP fixture contract only: no provider login or inference.
use anyhow::Result;
use futures::future::BoxFuture;
use pot_core::*;
use pot_driver::grok::GrokHost;
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
    identity: Mutex<Option<String>>,
    streaming: AtomicBool,
    stop: AtomicBool,
    notify: tokio::sync::Notify,
    incomplete: AtomicBool,
}
impl ExecutionFeedback for Feedback {
    fn cancelled(&self) -> bool {
        self.stop.load(Ordering::SeqCst)
    }
    fn cancellation(&self) -> BoxFuture<'_, ()> {
        Box::pin(self.notify.notified())
    }
    fn native_identity(&self, id: &str) -> Result<()> {
        *self.identity.lock().unwrap() = Some(id.into());
        Ok(())
    }
    fn binding_broken(&self, _: &str) -> Result<()> {
        anyhow::bail!("Unexpected broken binding")
    }
    fn needs_rebind(&self, _: &str) -> Result<()> {
        anyhow::bail!("Unexpected rebind")
    }
    fn record_turn(&self, _: &str) -> Result<()> {
        Ok(())
    }
    fn input_streaming(&self, _: &str) -> Result<()> {
        self.streaming.store(true, Ordering::SeqCst);
        Ok(())
    }
    fn begin_usage(&self, _: &str, _: &ResolvedSpec, _: u64) -> Result<()> {
        Ok(())
    }
    fn initialize_usage(&self, _: &str) -> Result<()> {
        anyhow::bail!("No verified Grok native usage")
    }
    fn cumulative_usage(&self, _: &str, _: &str, _: &Value) -> Result<()> {
        anyhow::bail!("No verified Grok native usage")
    }
    fn final_usage(&self, _: &str, _: Engine, _: &Value) -> Result<()> {
        anyhow::bail!("No fabricated usage")
    }
    fn incomplete_usage(&self, _: &str) -> Result<()> {
        self.incomplete.store(true, Ordering::SeqCst);
        Ok(())
    }
}
struct Config;
impl LegacyProfileConfig for Config {
    fn model(&self) -> &str {
        "grok-fixture"
    }
    fn max_tokens(&self) -> u64 {
        100
    }
}
#[tokio::test]
async fn session_streams_resumes_denies_permissions_and_settles_cancel() {
    let root = std::env::temp_dir().join(format!("pot-grok-fixture-{}", uuid::Uuid::new_v4()));
    std::fs::create_dir_all(&root).unwrap();
    let executable = root.join("grok");
    std::fs::write(&executable,r#"#!/usr/bin/env python3
import json,sys
assert sys.argv[1:]==['agent','--no-leader','stdio']
native='12345678-1234-4234-8234-123456789012'
prompt_id=None
def emit(v): print(json.dumps({'jsonrpc':'2.0',**v}),flush=True)
def reply(v,result):emit({'id':v['id'],'result':result})
for line in sys.stdin:
 v=json.loads(line);assert v['jsonrpc']=='2.0';m=v.get('method');p=v.get('params',{})
 if m=='initialize':
  assert p['clientCapabilities']=={};reply(v,{'protocolVersion':1})
 elif m=='_x.ai/auth/check_subscription':reply(v,{'authenticated':True,'meta':{'gate':None}})
 elif m=='session/new':
  assert p['mcpServers']==[] and p['_meta']['yoloMode']==False
  reply(v,{'sessionId':native})
 elif m=='session/load':assert p['sessionId']==native;reply(v,{})
 elif m=='session/set_config_option':
  assert p['configId']=='model' and p['value']=={'value':'grok-fixture'};reply(v,{})
 elif m=='session/prompt':
  prompt_id=v['id']
  if p['prompt'][0]['text'].endswith('WAIT'):continue
  emit({'id':9999,'method':'session/request_permission','params':{'sessionId':native,'options':[{'optionId':'allow','kind':'allow_once'}]}})
 elif m=='session/cancel':
  emit({'id':prompt_id,'result':{'stopReason':'cancelled'}});break
 elif v.get('id')==9999:
  assert v['result']['outcome']['outcome']=='cancelled'
  for text in ['fixture ','result']:
   emit({'method':'session/update','params':{'sessionId':native,'update':{'sessionUpdate':'agent_message_chunk','content':{'type':'text','text':text}}}})
  emit({'id':prompt_id,'result':{'stopReason':'end_turn'}})
 else:raise Exception('Unexpected RPC')
"#).unwrap();
    std::fs::set_permissions(&executable, std::fs::Permissions::from_mode(0o700)).unwrap();
    let mut profile = Profile::legacy(&Config);
    profile.engine = Engine::Grok;
    profile.provider = "xai".into();
    profile.auth_ref = "cli:grok".into();
    profile.launch_spec = Some(LaunchSpec {
        executable: executable.to_string_lossy().into_owned(),
        ..LaunchSpec::grok()
    });
    let mut binding = Binding {
        pot_owner: "fixture".into(),
        epoch: 1,
        spec: profile.resolve().unwrap(),
        native_ref: NativeRef::Grok { session_id: None },
        schema_version: SCHEMA_VERSION,
        state: BindingState::Active,
        model_switches: vec![],
        handoff_ref: None,
        failure: None,
    };
    assert!(
        !binding.spec.capabilities.tool_policy_enforcement
            && !binding.spec.capabilities.usage_reporting
    );
    let feedback = Arc::new(Feedback::default());
    let context = ExecutionContext {
        data: root.clone(),
        workspace: root.clone(),
        owner: "fixture".into(),
        role: ExecutionRole::Main,
        backend_url: "unused".into(),
        lane_token: "not-exposed".into(),
        pi_program: "unused".into(),
        main_prompt: String::new(),
        model_api: String::new(),
        feedback: feedback.clone(),
    };
    let mut session = GrokHost.session(context.clone(), binding.clone()).unwrap();
    let mut events = vec![];
    session
        .start_turn(
            &TurnRequest {
                request_id: "one".into(),
                text: "GO".into(),
            },
            false,
            &mut |e| {
                events.push(e);
                Ok(())
            },
        )
        .await
        .unwrap();
    assert!(
        events
            .iter()
            .any(|e| matches!(e.update, Update::PolicyDenied))
    );
    assert!(
        events.iter().any(
            |e| matches!(&e.update,Update::MessageFinished{text,..} if text=="fixture result")
        )
    );
    assert!(matches!(
        events.last().unwrap().update,
        Update::TurnFinished(Outcome::Success)
    ));
    assert!(feedback.incomplete.load(Ordering::SeqCst));
    binding.native_ref = NativeRef::Grok {
        session_id: feedback.identity.lock().unwrap().clone(),
    };
    feedback.streaming.store(false, Ordering::SeqCst);
    let mut session = GrokHost.session(context, binding).unwrap();
    let mut events = vec![];
    let mut sink = |e| {
        events.push(e);
        Ok(())
    };
    let canceller = async {
        tokio::time::timeout(Duration::from_secs(5), async {
            while !feedback.streaming.load(Ordering::SeqCst) {
                tokio::time::sleep(Duration::from_millis(5)).await;
            }
        })
        .await
        .unwrap();
        feedback.stop.store(true, Ordering::SeqCst);
        feedback.notify.notify_one();
    };
    let request = TurnRequest {
        request_id: "two".into(),
        text: "WAIT".into(),
    };
    let (result, ()) = tokio::join!(session.start_turn(&request, false, &mut sink), canceller);
    result.unwrap();
    assert!(matches!(
        events.last().unwrap().update,
        Update::TurnFinished(Outcome::Cancelled)
    ));
    std::fs::remove_dir_all(root).unwrap();
}
