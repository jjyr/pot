#!/usr/bin/env python3
"""Exercise dispatch identity, trusted promotion, and failure reporting with a fake gh."""

import copy
from datetime import datetime, timezone
import json
import os
from pathlib import Path
import runpy
import sys
import tempfile
import unittest
from unittest import mock

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "scripts"))
MODULE = runpy.run_path(str(ROOT / "scripts/release-control"))
Controller = MODULE["Controller"]
GitHub = MODULE["GitHub"]
ReleaseError = MODULE["ReleaseError"]
REQUEST = "11111111-2222-4333-8444-555555555555"
OTHER = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
SHA = "a" * 40


def run(run_id=10, kind="prepare", attempt=2, status="completed", conclusion="success"):
    title = f"release-{kind} [{OTHER}]"
    if kind == "publish":
        title += " prepare=10 manifest=70"
    return {
        "id": run_id,
        "workflow_id": 1 if kind == "prepare" else 2,
        "path": f".github/workflows/release-{kind}.yml",
        "head_branch": "main",
        "head_sha": SHA,
        "event": "workflow_dispatch",
        "repository": {"full_name": "jjyr/pot"},
        "head_repository": {"full_name": "jjyr/pot"},
        "run_attempt": attempt,
        "status": status,
        "conclusion": conclusion,
        "display_title": title,
    }


def artifact():
    return {
        "id": 70,
        "name": "release-candidate-10-2",
        "workflow_run": {"id": 10},
        "expired": False,
        "expires_at": "2099-01-01T00:00:00Z",
        "digest": "sha256:" + "b" * 64,
    }


FAKE_GH = r"""#!/usr/bin/env python3
import copy, json, os, sys
from urllib.parse import urlsplit, parse_qs
p=os.environ["FAKE_RELEASE_STATE"]
s=json.load(open(p))
a=sys.argv[1:]
data=json.load(sys.stdin) if "--input" in a else None
s["calls"].append({"args":a,"data":data})
def done(value=None, code=0):
    with open(p,"w") as f: json.dump(s,f)
    if value is not None: print(json.dumps(value))
    if code: print("SECRET-SHOULD-NOT-APPEAR",file=sys.stderr)
    sys.exit(code)
if a[:2]==["auth","status"]:done(code=s.get("auth_error",0))
if a[:2]==["repo","view"]:done({"nameWithOwner":s.get("local_repo","jjyr/pot"),"url":"https://github.com/"+s.get("local_repo","jjyr/pot")})
if a[:3]!=["api","--hostname","github.com"]:done(code=90)
prefix="repos/jjyr/pot"
if not a[3].startswith(prefix):done(code=91)
endpoint=a[3][len(prefix):].lstrip("/")
path=urlsplit(endpoint).path
q=parse_qs(urlsplit(endpoint).query)
if path=="":done({"full_name":"jjyr/pot","default_branch":s.get("default_branch","main")})
if path.startswith("actions/workflows/"):
    wid=path.split("/")[2]
    if path.endswith("/dispatches"):
        if s.get("dispatch_mode")!="error_without_run":
            kind="prepare" if wid=="1" else "publish"
            created=copy.deepcopy(s["template"])
            created.update(id=900,workflow_id=int(wid),path=".github/workflows/release-"+kind+".yml",head_branch=data["ref"],run_attempt=1,status=s.get("dispatched_status","queued"),conclusion=s.get("dispatched_conclusion"))
            created["display_title"]="release-"+kind+" ["+data["inputs"]["request-id"]+"]"
            if kind=="publish":created["display_title"]+=" prepare="+data["inputs"]["prepare-run-id"]+" manifest="+data["inputs"]["manifest-id"]
            s["runs"]["900"]=created
            if s.get("duplicate_marker"):
                duplicate=copy.deepcopy(created);duplicate["id"]=901;s["runs"]["901"]=duplicate
        done(code=1 if s.get("dispatch_mode","").startswith("error_") else 0)
    if path.endswith("/runs"):
        rows=sorted(s["runs"].values(),key=lambda x:x["id"],reverse=True)
        rows=[r for r in rows if r["workflow_id"]==int(wid)]
        if s.get("paged_correlation") and q.get("page")==["1"]:
            rows=[dict(s["template"],id=1000+i) for i in range(100)]
        done({"workflow_runs":rows})
    done(s["workflows"].get(wid,{}))
if path.startswith("actions/artifacts/"):
    aid=int(path.split("/")[-1]);matches=[x for x in s["artifacts"] if x["id"]==aid]
    done(matches[0] if len(matches)==1 else {},code=0 if len(matches)==1 else 1)
if path.startswith("actions/runs/"):
    pieces=path.split("/");rid=pieces[2]
    if path.endswith("/artifacts"):done({"artifacts":[x for x in s["artifacts"] if x["workflow_run"]["id"]==int(rid)]})
    if path.endswith("/jobs"):done({"jobs":s.get("jobs",{}).get(rid,[])})
    if "attempts" in pieces:
        exact=s.get("attempts",{}).get(rid+":"+pieces[-1])
        done(exact if exact else s["runs"][rid])
    value=s["runs"][rid]
    reads=s.setdefault("run_reads",{});reads[rid]=reads.get(rid,0)+1
    if s.get("rerun_during_selection") and rid=="10" and reads[rid]>=2:
        value=copy.deepcopy(value);value["run_attempt"]+=1
    done(value)
done(code=92)
"""


class ReleaseControl(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.directory = Path(self.temp.name)
        self.state_path = self.directory / "state.json"
        self.executable = self.directory / "gh"
        self.executable.write_text(FAKE_GH)
        self.executable.chmod(0o700)
        self.state = {
            "calls": [],
            "template": run(),
            "runs": {"10": run(), "999": run(999, status="queued", conclusion=None)},
            "artifacts": [artifact()],
            "workflows": {
                f"release-{kind}.yml": {
                    "id": i,
                    "path": f".github/workflows/release-{kind}.yml",
                    "state": "active",
                }
                for i, kind in enumerate(("prepare", "publish"), 1)
            },
        }
        self.environment = mock.patch.dict(os.environ, FAKE_RELEASE_STATE=str(self.state_path))
        self.environment.start()
        self.addCleanup(self.environment.stop)
        self.output = []
        self.tick = 0
        self.controller = Controller(
            GitHub(str(self.executable)),
            clock=lambda: self.tick,
            sleep=self.advance,
            now=lambda: datetime(2026, 1, 1, tzinfo=timezone.utc).timestamp(),
            new_uuid=lambda: REQUEST,
            out=self.output.append,
            inspect=self.inspect_candidate,
        )
        self.save()

    def inspect_candidate(self, run_id, manifest_id, require_fresh=True):
        state = self.read()
        if state.get("bundle_missing") or (require_fresh and state.get("bundle_expired")):
            raise ReleaseError("Candidate bundle missing or expired")
        return {
            "version": "0.0.9",
            "sha": SHA,
            "mode": "unsigned",
            "run_id": run_id,
            "run_attempt": 2,
            "manifest_id": manifest_id,
            "manifest_digest": "sha256:" + "b" * 64,
            "bundle_id": 60,
            "expires_at": "2098-01-01T00:00:00Z",
            "expired": state.get("bundle_expired", False)
            or any(a.get("expired") for a in state["artifacts"] if a["id"] == manifest_id),
        }

    def save(self):
        self.state_path.write_text(json.dumps(self.state))

    def read(self):
        return json.loads(self.state_path.read_text())

    def advance(self, seconds):
        self.tick += seconds

    def posts(self):
        return [call for call in self.read()["calls"] if call["data"] is not None]

    def test_prepare_correlates_uuid_not_newest_and_always_dispatches_default_branch(self):
        self.assertEqual(self.controller.prepare("v0.0.9", "c" * 40), 0)
        dispatch = self.posts()
        self.assertEqual(len(dispatch), 1)
        self.assertEqual(
            dispatch[0]["data"],
            {"ref": "main", "inputs": {"version": "0.0.9", "ref": "c" * 40, "request-id": REQUEST}},
        )
        self.assertIn("run-id: 900", "\n".join(self.output))
        self.assertNotIn("run-id: 999", "\n".join(self.output))

    def test_publish_pins_exact_current_attempt_manifest(self):
        old = artifact()
        old.update(id=69, name="release-candidate-10-1")
        self.state["artifacts"].insert(0, old)
        self.save()
        self.assertEqual(self.controller.publish(10), 0)
        self.assertEqual(
            self.posts()[0]["data"],
            {
                "ref": "main",
                "inputs": {"prepare-run-id": "10", "manifest-id": "70", "request-id": REQUEST},
            },
        )
        text = "\n".join(self.output)
        self.assertIn("publish identity: prepare-run-id=10 manifest-id=70", text)
        self.assertIn("expires_at=2099-01-01T00:00:00Z", text)
        self.assertIn("Homebrew:", text)

    def test_uncertain_dispatch_found_does_not_duplicate(self):
        self.state["dispatch_mode"] = "error_with_run"
        self.save()
        self.assertEqual(self.controller.prepare("0.0.9"), 0)
        self.assertEqual(len(self.posts()), 1)
        self.assertIn("outcome uncertain", "\n".join(self.output))
        self.assertNotIn("SECRET-SHOULD-NOT-APPEAR", "\n".join(self.output))

    def test_uncertain_dispatch_unresolved_reports_recovery_without_retry(self):
        self.state["dispatch_mode"] = "error_without_run"
        self.save()
        with self.assertRaisesRegex(ReleaseError, "Do not redispatch.*release:status"):
            self.controller.prepare("0.0.9")
        self.assertEqual(len(self.posts()), 1)
        self.assertGreaterEqual(self.tick, MODULE["CORRELATION_SECONDS"])
        self.assertIn(REQUEST, "\n".join(self.output))

    def test_ambiguous_marker_is_rejected(self):
        self.state["duplicate_marker"] = True
        self.save()
        with self.assertRaisesRegex(ReleaseError, "Multiple runs"):
            self.controller.prepare("0.0.9")
        self.assertEqual(len(self.posts()), 1)

    def test_paginated_correlation_finds_exact_marker(self):
        self.state["paged_correlation"] = True
        self.save()
        self.assertEqual(self.controller.prepare("0.0.9"), 0)
        self.assertTrue(
            any("page=2" in c["args"][3] for c in self.read()["calls"] if c["args"][0] == "api")
        )

    def test_failed_cancelled_timedout_runs_exit_nonzero(self):
        for conclusion in ("failure", "cancelled", "timed_out"):
            with self.subTest(conclusion=conclusion):
                self.state["runs"]["10"].update(conclusion=conclusion)
                self.save()
                self.assertEqual(self.controller.status(10), 1)
                self.assertIn(f"conclusion: {conclusion}", "\n".join(self.output))

    def test_wait_failure_and_bounded_timeout(self):
        self.state.update(dispatched_status="completed", dispatched_conclusion="failure")
        self.save()
        self.assertEqual(self.controller.prepare("0.0.9", wait=True), 1)
        self.state.update(dispatched_status="queued", dispatched_conclusion=None)
        self.save()
        with mock.patch.dict(Controller.wait.__globals__, WAIT_SECONDS=20):
            with self.assertRaisesRegex(ReleaseError, "Wait timed out.*release:status -- 900"):
                self.controller.prepare("0.0.9", wait=True)
        self.assertEqual(len(self.posts()), 1)  # save() reset the independent request log

    def test_missing_expired_duplicate_and_foreign_manifests_prevent_dispatch(self):
        variants = [
            [],
            [dict(artifact(), expired=True)],
            [dict(artifact(), expires_at="2020-01-01T00:00:00Z")],
            [artifact(), artifact()],
            [dict(artifact(), digest="bad")],
        ]
        for artifacts in variants:
            with self.subTest(artifacts=artifacts):
                self.state["artifacts"] = artifacts
                self.save()
                with self.assertRaises(ReleaseError):
                    self.controller.publish(10)
                self.assertEqual(self.posts(), [])

    def test_wrong_workflow_fork_branch_event_and_failed_prepare_prevent_dispatch(self):
        variants = [
            {"workflow_id": 99},
            {"head_repository": {"full_name": "fork/pot"}},
            {"repository": {"full_name": "other/pot"}},
            {"head_branch": "untrusted"},
            {"event": "push"},
            {"conclusion": "failure"},
        ]
        for changes in variants:
            with self.subTest(changes=changes):
                self.state["runs"]["10"] = {**run(), **changes}
                self.save()
                with self.assertRaises(ReleaseError):
                    self.controller.publish(10)
                self.assertEqual(self.posts(), [])

    def test_rerun_during_selection_prevents_dispatch(self):
        self.state["rerun_during_selection"] = True
        self.save()
        with self.assertRaisesRegex(ReleaseError, "attempt changed"):
            self.controller.publish(10)
        self.assertEqual(self.posts(), [])

    def test_wrong_repository_and_auth_fail_without_token_output(self):
        self.state["local_repo"] = "fork/pot"
        self.save()
        with self.assertRaisesRegex(ReleaseError, "require.*jjyr/pot"):
            self.controller.prepare("0.0.9")
        self.assertEqual(self.posts(), [])
        self.state["auth_error"] = 1
        self.save()
        with self.assertRaisesRegex(ReleaseError, "GitHub CLI request failed") as error:
            self.controller.prepare("0.0.9")
        self.assertNotIn("SECRET-SHOULD-NOT-APPEAR", str(error.exception))

    def test_publish_status_uses_pinned_attempt_after_later_rerun(self):
        self.state["runs"]["20"] = run(20, "publish")
        self.state["attempts"] = {"10:2": run()}
        self.state["runs"]["10"] = run(attempt=3, conclusion="failure")
        self.state["jobs"] = {
            "20": [
                {
                    "name": "Propose Homebrew packages after publication",
                    "status": "completed",
                    "conclusion": "success",
                    "steps": [],
                }
            ]
        }
        self.save()
        self.assertEqual(self.controller.status(20), 0)
        text = "\n".join(self.output)
        self.assertIn("pinned manifest: attempt=2", text)
        self.assertIn("PR review/merge pending verification", text)
        calls = self.read()["calls"]
        self.assertTrue(
            any("runs/20/attempts/2/jobs" in c["args"][3] for c in calls if c["args"][0] == "api")
        )

    def test_expired_successful_prepare_status_displays_actual_expiry(self):
        self.state["artifacts"][0]["expired"] = True
        self.save()
        self.assertEqual(self.controller.status(10), 1)
        self.assertIn("expires_at=2099-01-01T00:00:00Z", "\n".join(self.output))

    def test_missing_or_expired_bundle_blocks_candidate_admission(self):
        for flag in ("bundle_missing", "bundle_expired"):
            with self.subTest(flag=flag):
                self.state[flag] = True
                self.save()
                with self.assertRaisesRegex(ReleaseError, "bundle missing or expired"):
                    self.controller.publish(10)
                self.assertEqual(self.posts(), [])
                del self.state[flag]

    def test_expired_bundle_status_is_not_ready_and_shows_effective_expiry(self):
        self.state["bundle_expired"] = True
        self.save()
        self.assertEqual(self.controller.status(10), 1)
        text = "\n".join(self.output)
        self.assertIn("expired/not publishable", text)
        self.assertIn("expires_at=2098-01-01T00:00:00Z", text)

    def test_candidate_status_shows_version_sha_and_effective_bundle_expiry(self):
        self.assertEqual(self.controller.status(10), 0)
        text = "\n".join(self.output)
        self.assertIn("version=0.0.9 source=" + SHA, text)
        self.assertIn("expires_at=2098-01-01T00:00:00Z", text)

    def test_expired_or_missing_evidence_does_not_change_published_run_success(self):
        self.state["runs"]["20"] = run(20, "publish")
        for change in ("bundle_expired", "manifest_expired", "manifest_missing"):
            with self.subTest(change=change):
                self.state["artifacts"] = [artifact()]
                self.state["bundle_expired"] = change == "bundle_expired"
                if change == "manifest_expired":
                    self.state["artifacts"][0]["expired"] = True
                if change == "manifest_missing":
                    self.state["artifacts"] = []
                self.save()
                self.assertEqual(self.controller.status(20), 0)
        self.assertIn("status: completed conclusion: success", "\n".join(self.output))

    def test_local_packaging_task_and_cli_registration(self):
        import tomllib

        tasks = tomllib.loads((ROOT / "mise.toml").read_text())["tasks"]
        self.assertIn('python scripts/release "$usage_version"', tasks["release"]["run"])
        for kind in ("prepare", "publish", "status"):
            self.assertEqual(
                tasks["release:" + kind]["run"], "python scripts/release-control " + kind
            )
        self.assertIn("python scripts/tests/release-control.py", tasks["test:release"]["run"])
        self.assertNotIn("python scripts/tests/release-refresh.py", tasks["test:packaging"]["run"])
        for command in (
            "python scripts/tests/release-control.py",
            "python scripts/tests/release-candidate.py",
            "python scripts/tests/public-release-verification.py",
        ):
            self.assertEqual(tasks["ci:light"]["run"].count(command), 1)


if __name__ == "__main__":
    unittest.main()
