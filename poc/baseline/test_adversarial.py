"""Negative tests found during independent verification, not provider integration tests."""
import tempfile,unittest
from pathlib import Path
from runtime import Runtime,DesktopGate
class AdversarialTests(unittest.TestCase):
 def setUp(self):
  self.tmp=tempfile.TemporaryDirectory();self.r=Runtime(Path(self.tmp.name)/'state.sqlite')
 def tearDown(self):self.r.close();self.tmp.cleanup()
 def test_return_control_cannot_release_inflight_takeover(self):
  gate=DesktopGate(self.r)
  with gate.agent_input():
   self.assertFalse(gate.take_over(0))
   with self.assertRaises(RuntimeError):gate.return_control()
   self.assertFalse(gate.enabled)
  self.assertTrue(gate.take_over());self.assertTrue(gate.human_input())
 def test_unknown_rule_cannot_grant_external_action(self):
  self.r.grant('g','account','messages')
  self.assertEqual(self.r.review('g','account','messages','send','alow',True),'deny')
 def test_unknown_action_cannot_default_to_allow(self):
  self.r.grant('g','account','messages')
  self.assertEqual(self.r.review('g','account','messages','unrecognized_mutation','allow',True),'deny')
 def test_recovery_invalidates_running_binding(self):
  task=self.r.task('work');self.r.bind(task,'pi','s1');self.r.transition(task,'running',1)
  self.r.recover();self.assertFalse(self.r.binding_valid(task))
if __name__=='__main__':unittest.main(verbosity=2)
