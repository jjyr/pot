import json, os, subprocess, sys, tempfile, threading, time, unittest
from pathlib import Path
from runtime import Runtime,DesktopGate
class RuntimeTests(unittest.TestCase):
 def setUp(self):self.tmp=tempfile.TemporaryDirectory();self.path=Path(self.tmp.name)/'state.sqlite';self.r=Runtime(self.path)
 def tearDown(self):self.r.close();self.tmp.cleanup()
 def restart(self):self.r.close();self.r=Runtime(self.path)
 def test_default_name_setup_resume(self):
  self.assertEqual(self.r.settings('name'),'Pot');self.r.settings('engine','pi');self.r.settings('onboarding','name_answered');self.restart();self.assertEqual(self.r.settings('onboarding'),'name_answered');self.assertEqual(self.r.settings('engine'),'pi')
 def test_task_thread_routing_environment(self):
  a=self.r.task('report','node-a');b=self.r.task('files','node-b');self.assertNotEqual(self.r.db.execute('select conversation from tasks where id=?',(a,)).fetchone(),self.r.db.execute('select conversation from tasks where id=?',(b,)).fetchone())
 def test_input_dedup_and_ack_is_not_applied(self):
  self.r.accept('request-1','main','hello');self.r.accept('request-1','main','hello');self.restart();self.assertEqual(self.r.db.execute('select count(*) from messages').fetchone()[0],1);self.assertEqual(self.r.db.execute('select status from messages').fetchone()[0],'accepted');self.r.apply_input('request-1');self.assertEqual(self.r.db.execute('select status from messages').fetchone()[0],'applied')
 def test_transaction_outbox_survives_crash(self):
  tid=self.r.task('work');self.r.close()
  code="from runtime import Runtime;import os;r=Runtime(%r);r.transition(%r,'running',1);os._exit(73)"%(str(self.path),tid)
  p=subprocess.run([sys.executable,'-c',code],cwd=Path(__file__).parent);self.assertEqual(p.returncode,73);self.r=Runtime(self.path);self.assertEqual(len(self.r.pending()),1);self.assertEqual(self.r.db.execute('select status from tasks').fetchone()[0],'running')
 def test_uncommitted_transaction_rolls_back_on_process_death(self):
  tid=self.r.task('work');self.r.close()
  code="from runtime import Runtime;import os;r=Runtime(%r);r.db.execute('BEGIN');r.db.execute(\"UPDATE tasks SET status='running'\");os._exit(74)"%str(self.path)
  subprocess.run([sys.executable,'-c',code],cwd=Path(__file__).parent);self.r=Runtime(self.path);self.assertEqual(self.r.db.execute('select status from tasks').fetchone()[0],'queued');self.assertEqual(self.r.pending(),[])
 def test_at_least_once_event_and_idempotent_consumer(self):
  tid=self.r.task('work');self.r.transition(tid,'running',1);event=self.r.pending()[0][0];delivered=set();delivered.add(event);self.restart();delivered.add(self.r.pending()[0][0]);self.assertEqual(len(delivered),1);self.r.ack(event);self.assertFalse(self.r.pending())
 def test_cancel_fences_late_worker(self):
  tid=self.r.task('work');self.r.cancel(tid)
  with self.assertRaises(ValueError):self.r.transition(tid,'succeeded',1)
 def test_recovery_does_not_mark_success(self):
  tid=self.r.task('work');self.r.transition(tid,'running',1);self.r.recover();self.assertEqual(self.r.db.execute('select status from tasks').fetchone()[0],'waiting_user')
 def test_unknown_operation_never_blindly_retries(self):
  tid=self.r.task('send');self.r.begin_operation('operation-1',tid);self.restart();self.r.recover();self.assertEqual(self.r.db.execute('select status from operations').fetchone()[0],'unknown')
  with self.assertRaises(Exception):self.r.begin_operation('operation-1',tid)
  self.r.resolve('operation-1','remote-object-1');self.assertEqual(self.r.db.execute('select external_id from operations').fetchone()[0],'remote-object-1')
 def test_memory_correction_and_binding_invalidation(self):
  tid=self.r.task('work');self.r.fact('language','Chinese',True);self.r.bind(tid,'pi','thread-1');self.assertTrue(self.r.binding_valid(tid));self.r.fact('language','English',True);self.assertEqual(self.r.context(),['English']);self.assertFalse(self.r.binding_valid(tid))
 def test_forget_cannot_relearn_from_same_source_id(self):
  self.r.fact('preference','secret fact',True);self.r.forget('preference');self.restart();self.assertEqual(self.r.context(),[]);self.assertFalse(self.r.fact('preference','secret fact'));self.assertTrue(self.r.fact('preference','new explicit fact',True))
 def test_provider_switch_keeps_product_identity(self):
  tid=self.r.task('work');self.r.settings('name','Pot');self.r.bind(tid,'codex','codex-thread');self.r.bind(tid,'pi','pi-session');self.assertEqual(self.r.settings('name'),'Pot');self.assertEqual(self.r.db.execute('select thread from bindings').fetchone()[0],'pi-session')
 def test_schedule_coalesces_missed_runs_once(self):
  tid=self.r.task('check');self.r.schedule('weekly',tid,100,10);self.assertEqual(len(self.r.tick(145)),1);self.assertEqual(self.r.tick(145),[]);self.assertEqual(self.r.db.execute('select next_at from schedules').fetchone()[0],150)
 def test_schedule_skip_and_disable(self):
  tid=self.r.task('check');self.r.schedule('weekly',tid,100,10,'skip');self.assertEqual(self.r.tick(145),[]);self.r.disable_schedule('weekly');self.assertEqual(self.r.tick(160),[])
 def test_schedule_cancellation_not_running_task(self):
  tid=self.r.task('check');self.r.transition(tid,'running',1);self.r.schedule('weekly',tid,100,10);self.r.disable_schedule('weekly');self.assertEqual(self.r.db.execute('select status from tasks').fetchone()[0],'running')
 def test_main_cancel_not_all_workers(self):
  a=self.r.task('main');b=self.r.task('child');self.r.cancel(a);self.assertEqual(self.r.db.execute('select status from tasks where id=?',(b,)).fetchone()[0],'queued')
 def test_account_scope_and_revoke_enforced(self):
  self.r.grant('g','github-a','repo-read');self.assertEqual(self.r.review('g','github-b','repo-read','read','allow'),'deny');self.assertEqual(self.r.review('g','github-a','repo-write','send','allow'),'deny');self.r.revoke('g');self.assertEqual(self.r.review('g','github-a','repo-read','read','allow'),'deny')
 def test_action_rules_and_draft_not_send(self):
  self.r.grant('g','account','messages');self.assertEqual(self.r.review('g','account','messages','send','explicit',False),'ask');self.assertEqual(self.r.review('g','account','messages','send','explicit',True),'allow');self.assertEqual(self.r.review('g','account','messages','send','ask',True),'ask');self.assertEqual(self.r.review('g','account','messages','send','handoff',True),'handoff');self.assertEqual(self.r.review('g','account','messages','change_password','allow',True),'handoff')
 def test_research_cannot_write_or_control(self):
  self.r.grant('g','account','messages')
  for action in ('send','delete','desktop'):self.assertEqual(self.r.review('g','account','messages',action,'allow',True,True),'deny')
 def test_takeover_waits_for_inflight(self):
  g=DesktopGate(self.r);entered=threading.Event();release=threading.Event()
  def work():
   with g.agent_input():entered.set();release.wait(2)
  t=threading.Thread(target=work);t.start();entered.wait(1)
  self.assertFalse(g.take_over(.01));self.assertFalse(g.human_input())
  with self.assertRaises(PermissionError):
   with g.agent_input():pass
  release.set();t.join();self.assertTrue(g.take_over());self.assertTrue(g.human_input());self.assertEqual(g.return_control(),'reobserve_required')
 def test_takeover_survives_runtime_restart(self):
  g=DesktopGate(self.r);self.assertTrue(g.take_over());self.restart();g=DesktopGate(self.r);self.assertFalse(g.enabled);self.assertTrue(g.human_input())
 def test_takeover_not_non_desktop_task_stop(self):
  tid=self.r.task('api work');self.r.transition(tid,'running',1);DesktopGate(self.r).take_over();self.assertEqual(self.r.db.execute('select status from tasks').fetchone()[0],'running')
if __name__=='__main__':
 suite=unittest.defaultTestLoader.loadTestsFromTestCase(RuntimeTests)
 start=time.monotonic();result=unittest.TextTestRunner(verbosity=2).run(suite)
 Path(__file__).parent.joinpath('evidence/runtime-tests.json').write_text(json.dumps({'tests':result.testsRun,'failures':len(result.failures),'errors':len(result.errors),'seconds':time.monotonic()-start,'limitations':['cooperative desktop broker only','no semantic memory evaluation','no real service side effects']},indent=2))
 sys.exit(not result.wasSuccessful())
