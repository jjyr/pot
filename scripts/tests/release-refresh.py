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

    def test_candidate_must_be_successful_exact_source_and_merged_tree(self):
        run = {
            "path": ".github/workflows/ci.yml",
            "head_sha": SHA,
            "event": "workflow_dispatch",
            "conclusion": "success",
        }
        responses = {
            "commits/refs/tags/v0.0.1": {"sha": SHA},
            "commits/" + SHA: {"commit": {"tree": {"sha": "tree"}}},
            "commits/main": {"commit": {"tree": {"sha": "tree"}}},
            "actions/runs/123": run,
        }
        with mock.patch.dict(os.environ, POT_BUILD_REVISION=SHA, CANDIDATE_RUN="123"):
            with mock.patch.dict(self.namespace, api=lambda endpoint: responses[endpoint]):
                self.assertEqual(refresh["check_source"](), SHA)
                run["conclusion"] = "failure"
                with self.assertRaises(ValueError):
                    refresh["check_source"]()
                run["conclusion"] = "success"
                responses["commits/main"]["commit"]["tree"]["sha"] = "other"
                with self.assertRaises(ValueError):
                    refresh["check_source"]()


if __name__ == "__main__":
    unittest.main()
