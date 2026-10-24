#!/usr/bin/env python3
"""Exercise snapshot and staging fences for the explicit 0.0.1 refresh."""

import copy
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
refresh = runpy.run_path(str(ROOT / "scripts/refresh-release"))
SHA = "a" * 40


class RefreshFences(unittest.TestCase):
    def setUp(self):
        self.namespace = refresh["activate"].__globals__
        self.assets = [
            {"id": 1, "name": "Pot-0.0.1-arm64.zip", "size": 4, "digest": "sha256:" + "b" * 64}
        ]
        self.old = {"target_commitish": "c" * 40, "assets": copy.deepcopy(self.assets)}
        self.plan = [
            dict(self.assets[0], id=2, digest="sha256:" + "d" * 64, staged_name="staged-app")
        ]
        self.assets.append(dict(self.plan[0], name="staged-app"))
        self.calls = []

    def api(self, endpoint, method="GET", body=None):
        self.calls.append((endpoint, method, body))
        if endpoint == "releases/422513414":
            return dict(
                self.old,
                id=422513414,
                tag_name="v0.0.1",
                draft=False,
                assets=copy.deepcopy(self.assets),
            )
        asset = next(a for a in self.assets if a["id"] == int(endpoint.split("/")[-1]))
        if method == "PATCH":
            asset.update(body)
        return copy.deepcopy(asset)

    def activate(self):
        with tempfile.TemporaryDirectory() as temporary:
            before = Path.cwd()
            os.chdir(temporary)
            try:
                Path("recovery").mkdir()
                Path("recovery/replacement-plan.json").write_text(json.dumps(self.plan))
                with mock.patch.dict(self.namespace, api=self.api, check_source=lambda: SHA):
                    refresh["activate"](self.old, SHA)
            finally:
                os.chdir(before)

    def test_binary_source_uses_vcs_receipt_without_assuming_linker_flags(self):
        with mock.patch(
            "subprocess.check_output",
            return_value="binary: go1.27.2\n\tbuild\tvcs.revision=" + SHA + "\n",
        ):
            refresh["verify_build"](b"fixture", SHA)
            with self.assertRaisesRegex(ValueError, "source revision"):
                refresh["verify_build"](b"fixture", "b" * 40)

    def test_retag_entry_validates_candidate_before_requiring_replacement_tag(self):
        # The old published tag necessarily differs before the retag phase.
        with mock.patch.object(sys, "argv", ["refresh-release", "retag", "dist"]):
            with mock.patch.dict(self.namespace, baseline=lambda: self.old):
                with mock.patch.dict(
                    self.namespace,
                    check_source=mock.Mock(side_effect=AssertionError("early tag check")),
                    retag=mock.Mock(),
                ) as namespace:
                    refresh["main"]()
                    namespace["retag"].assert_called_once_with(self.old)

    def test_release_environment_is_reached_only_from_the_existing_version_tag(self):
        workflow = (ROOT / ".github/workflows/refresh-release.yml").read_text()
        lease, publication = workflow.split("  refresh:\n", 1)
        self.assertIn("github.ref == 'refs/heads/main' && inputs.lease-tag-only", lease)
        self.assertNotIn("environment:", lease)
        self.assertNotIn("secrets.", lease)
        self.assertIn("github.ref == 'refs/tags/v0.0.1' && !inputs.lease-tag-only", publication)
        self.assertIn("environment: release", publication)
        self.assertNotIn("refresh-release retag", publication)
        self.assertNotIn("actions: write", workflow)

    def test_retag_rejects_local_and_release_environment_execution(self):
        for ref in ("", "refs/tags/v0.0.1"):
            with mock.patch.dict(os.environ, GITHUB_ACTIONS="true", GITHUB_REF=ref):
                with self.assertRaisesRegex(ValueError, "dedicated main workflow"):
                    refresh["retag"](self.old)

    def test_retag_stale_object_never_pushes_and_success_uses_exact_lease(self):
        expected, replacement = "e" * 40, "f" * 40
        current = {"object": {"sha": "b" * 40}}
        with tempfile.TemporaryDirectory() as temporary:
            before = Path.cwd()
            os.chdir(temporary)
            try:
                with mock.patch.dict(
                    os.environ,
                    GITHUB_ACTIONS="true",
                    GITHUB_REF="refs/heads/main",
                    PREVIOUS_TAG_OBJECT=expected,
                ):
                    with mock.patch.dict(
                        self.namespace,
                        check_source=lambda **kwargs: SHA,
                        check_baseline=lambda *args: None,
                        selected_release=lambda: {},
                        api=lambda endpoint: current,
                    ):
                        with mock.patch(
                            "subprocess.check_output", side_effect=["123\n", replacement + "\n"]
                        ):
                            with mock.patch("subprocess.run") as push:
                                with self.assertRaisesRegex(
                                    ValueError, "Original tag object changed"
                                ):
                                    refresh["retag"](self.old)
                                push.assert_not_called()
                        current["object"]["sha"] = expected

                        def pushed(*args, **kwargs):
                            current["object"]["sha"] = replacement

                        with mock.patch(
                            "subprocess.check_output", side_effect=["123\n", replacement + "\n"]
                        ):
                            with mock.patch("subprocess.run", side_effect=pushed) as push:
                                refresh["retag"](self.old)
                                command = push.call_args.args[0]
                                self.assertIn(
                                    "--force-with-lease=refs/tags/v0.0.1:" + expected, command
                                )
                                self.assertIn(replacement + ":refs/tags/v0.0.1", command)
                                self.assertEqual(
                                    json.loads(Path("recovery/tag-before.json").read_text())[
                                        "object"
                                    ]["sha"],
                                    expected,
                                )
            finally:
                os.chdir(before)

    def test_stale_original_stops_before_any_mutation(self):
        self.assets[0]["size"] = 99
        with self.assertRaises(ValueError):
            self.activate()
        self.assertTrue(all(method == "GET" for _, method, _ in self.calls))

    def test_stale_staged_digest_stops_before_any_mutation(self):
        self.assets[1]["digest"] = "sha256:" + "e" * 64
        with self.assertRaises(ValueError):
            self.activate()
        self.assertTrue(all(method == "GET" for _, method, _ in self.calls))

    def test_activation_preserves_original_id_and_switches_only_verified_candidate(self):
        self.activate()
        self.assertEqual(self.assets[0]["name"], "previous-1-Pot-0.0.1-arm64.zip")
        self.assertEqual(self.assets[1]["name"], "Pot-0.0.1-arm64.zip")
        writes = [(endpoint, body) for endpoint, method, body in self.calls if method == "PATCH"]
        self.assertEqual(writes[-1], ("releases/422513414", {"target_commitish": SHA}))
        self.assertFalse(any(method == "DELETE" for _, method, _ in self.calls))

    def test_new_archive_names_are_live_before_switching_same_name_archives(self):
        archive = dict(
            self.plan[0], id=3, name="pot-0.0.1-linux-amd64.tar.gz", staged_name="staged-cli"
        )
        self.plan.append(archive)
        self.assets.append(dict(archive, name="staged-cli"))
        self.activate()
        writes = [(endpoint, body) for endpoint, method, body in self.calls if method == "PATCH"]
        self.assertEqual(writes[0], ("releases/assets/3", {"name": archive["name"]}))

    def test_candidate_binds_exact_qualification_pr_and_naming_ancestry(self):
        reviewed = "b" * 40
        naming = refresh["NAMING_MERGE"]
        qualification = "pulls/" + str(refresh["QUALIFICATION_PR"])
        compare = "compare/" + naming + "..." + SHA
        repo = {"full_name": "jjyr/pot"}
        responses = {
            "commits/refs/tags/v0.0.1": {"sha": SHA},
            "commits/" + SHA: {"commit": {"tree": {"sha": "tree"}}},
            "commits/" + reviewed: {
                "commit": {"tree": {"sha": "tree"}},
                "parents": [{"sha": naming}, {"sha": SHA}],
            },
            "pulls/280": {
                "merged": True,
                "base": {"ref": "main", "repo": dict(repo)},
                "merge_commit_sha": naming,
            },
            qualification: {
                "merged": True,
                "base": {"ref": "main", "repo": dict(repo)},
                "head": {"sha": SHA, "repo": dict(repo)},
                "merge_commit_sha": reviewed,
            },
            compare: {"status": "ahead", "merge_base_commit": {"sha": naming}},
            "actions/runs/123": {
                "path": ".github/workflows/ci.yml",
                "head_sha": SHA,
                "event": "workflow_dispatch",
                "conclusion": "success",
            },
        }
        self.assertEqual(qualification, "pulls/291")
        # No rolling-main lookup or generic PR selection. Both reviewed anchors
        # and the actual candidate/merge relationship remain mandatory.
        with mock.patch.dict(
            os.environ, POT_BUILD_REVISION=SHA, REVIEWED_MERGE=reviewed, CANDIDATE_RUN="123"
        ):
            with mock.patch.dict(self.namespace, api=lambda endpoint: responses[endpoint]):
                self.assertEqual(refresh["check_source"](), SHA)
            for label, endpoint, path, value in [
                ("not merged", qualification, ("merged",), False),
                ("wrong base", qualification, ("base", "ref"), "other"),
                ("wrong repository", qualification, ("base", "repo", "full_name"), "other/pot"),
                ("fork head", qualification, ("head", "repo", "full_name"), "other/pot"),
                ("wrong head", qualification, ("head", "sha"), "d" * 40),
                ("wrong merge", qualification, ("merge_commit_sha",), "d" * 40),
                ("naming not merged", "pulls/280", ("merged",), False),
                ("naming merge changed", "pulls/280", ("merge_commit_sha",), "d" * 40),
                ("naming wrong base", "pulls/280", ("base", "ref"), "other"),
                ("not descendant", compare, ("status",), "diverged"),
                ("wrong ancestor", compare, ("merge_base_commit", "sha"), "d" * 40),
                (
                    "candidate not parent",
                    "commits/" + reviewed,
                    ("parents",),
                    [{"sha": naming}, {"sha": "d" * 40}],
                ),
                ("not normal merge", "commits/" + reviewed, ("parents",), [{"sha": SHA}]),
                ("tree changed", "commits/" + reviewed, ("commit", "tree", "sha"), "other"),
                ("failed run", "actions/runs/123", ("conclusion",), "failure"),
                ("different run source", "actions/runs/123", ("head_sha",), "d" * 40),
                ("wrong workflow", "actions/runs/123", ("path",), ".github/workflows/tests.yml"),
                ("tag moved", "commits/refs/tags/v0.0.1", ("sha",), "d" * 40),
            ]:
                with self.subTest(label):
                    invalid = copy.deepcopy(responses)
                    target = invalid[endpoint]
                    for key in path[:-1]:
                        target = target[key]
                    target[path[-1]] = value
                    with mock.patch.dict(self.namespace, api=lambda endpoint: invalid[endpoint]):
                        with self.assertRaises(ValueError):
                            refresh["check_source"]()


if __name__ == "__main__":
    unittest.main()
