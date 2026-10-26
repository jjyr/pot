#!/usr/bin/env python3
"""Hermetic refusal tests for the fixed v0.0.3 recovery boundary."""

import copy
import io
from pathlib import Path
import runpy
import sys
import tempfile
import unittest
from unittest import mock
import zipfile

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "scripts"))
RECOVERY = runpy.run_path(str(ROOT / "scripts/recover-v003"))
STATE = RECOVERY["release_state"]
EXECUTION = RECOVERY["execution"]


def baseline():
    return {
        "id": RECOVERY["RELEASE_ID"],
        "tag_name": "v0.0.3",
        "target_commitish": RECOVERY["SOURCE"],
        "draft": False,
        "prerelease": False,
        "assets": [
            {"name": name, "id": value[0], "size": value[1], "digest": "sha256:" + value[2]}
            for name, value in RECOVERY["BASELINE"].items()
        ],
    }


class RecoveryBoundary(unittest.TestCase):
    def test_existing_identity_and_bytes_must_remain_exact(self):
        original = baseline()
        STATE(original)
        for field, value in [
            ("id", 1),
            ("tag_name", "v0.0.4"),
            ("target_commitish", RECOVERY["HARNESS"]),
            ("draft", True),
            ("prerelease", True),
        ]:
            changed = dict(original, **{field: value})
            with self.subTest(field=field), self.assertRaises(ValueError):
                STATE(changed)
        for field, value in [("id", 1), ("size", 1), ("digest", "sha256:" + "0" * 64)]:
            changed = copy.deepcopy(original)
            changed["assets"][0][field] = value
            with self.subTest(asset_field=field), self.assertRaises(ValueError):
                STATE(changed)
        for assets in [
            original["assets"][:-1],
            original["assets"] + [original["assets"][0]],
            original["assets"] + [{"name": "foreign"}],
        ]:
            with self.assertRaises(ValueError):
                STATE(dict(original, assets=assets))

    def test_partial_effects_require_the_original_qualified_candidate_bytes(self):
        release = baseline()
        name = "update.json"
        with tempfile.TemporaryDirectory() as temporary:
            dist = Path(temporary)
            for addition in RECOVERY["ADDITIONS"]:
                (dist / addition).write_bytes(b"candidate")
            sha = RECOVERY["digest"](dist / name)
            release["assets"].append({"name": name, "size": 9, "digest": "sha256:" + sha})
            with self.assertRaises(ValueError):
                STATE(release)  # A full restart cannot rebuild after a partial publish.
            STATE(release, before_signing=True)  # Identity-only fence precedes key access.
            STATE(release, dist)
            (dist / name).write_bytes(b"different")
            with self.assertRaises(ValueError):
                STATE(release, dist)

    def test_publication_guards_need_only_existing_contents_permissions(self):
        def read(endpoint):
            if endpoint == "git/ref/tags/v0.0.3":
                return {"object": {"type": "tag", "sha": RECOVERY["TAG_OBJECT"]}}
            if endpoint == "git/tags/" + RECOVERY["TAG_OBJECT"]:
                return {"object": {"sha": RECOVERY["SOURCE"]}}
            if endpoint in {"releases/423774760", "releases/tags/v0.0.3"}:
                return baseline()
            self.fail("publication guard requested additional permission: " + endpoint)

        provenance = RECOVERY["provenance"]
        with mock.patch.dict(provenance.__globals__, api=read):
            provenance(qualification=False)

    def test_only_the_reviewed_manual_nonversion_execution_ref_is_accepted(self):
        sha = "a" * 40
        env = {
            "GITHUB_REPOSITORY": "jjyr/pot",
            "GITHUB_EVENT_NAME": "workflow_dispatch",
            "GITHUB_REF": "refs/tags/" + RECOVERY["EXECUTION_TAG"],
            "GITHUB_WORKFLOW_REF": "jjyr/pot/.github/workflows/recover-v003.yml@refs/tags/"
            + RECOVERY["EXECUTION_TAG"],
            "GITHUB_WORKFLOW_SHA": sha,
            "GITHUB_SHA": sha,
            "RELEASE_SHA": RECOVERY["SOURCE"],
            "RELEASE_VERSION": "0.0.3",
            "POT_UPDATE_PUBLIC_KEY": RECOVERY["PUBLIC_KEY"],
        }
        EXECUTION(env, sha)
        for field, value in [
            ("GITHUB_REPOSITORY", "fork/pot"),
            ("GITHUB_EVENT_NAME", "push"),
            ("GITHUB_REF", "refs/heads/main"),
            ("GITHUB_REF", "refs/tags/v0.0.3"),
            ("GITHUB_WORKFLOW_REF", "other"),
            ("GITHUB_SHA", "b" * 40),
            ("RELEASE_SHA", RECOVERY["HARNESS"]),
            ("RELEASE_VERSION", "0.0.4"),
            ("POT_UPDATE_PUBLIC_KEY", "different"),
            ("GOFLAGS", "-overlay=fixture"),
        ]:
            with self.subTest(field=field, value=value), self.assertRaises(ValueError):
                EXECUTION(dict(env, **{field: value}), sha)
        with self.assertRaises(ValueError):
            EXECUTION(env, "b" * 40)
        plan = runpy.run_path(str(ROOT / "scripts/release-plan"))["plan"]
        result = plan("push", env["GITHUB_REF"], "", "", False, sha, lambda *_: sha)
        self.assertEqual((result["version"], result["official"]), ("0.0.0", "false"))

    def test_zip_paths_and_public_bytes_are_checked_without_package_execution(self):
        with tempfile.TemporaryDirectory() as temporary:
            directory = Path(temporary)
            package = directory / "bad.zip"
            with zipfile.ZipFile(package, "w") as archive:
                archive.writestr("../escape", "bad")
            with self.assertRaises(ValueError):
                RECOVERY["extract_cli"](package, directory / "out", "amd64")
            self.assertFalse((directory / "escape").exists())
            path = directory / "fixture"
            path.write_bytes(b"same bytes")
            asset = {
                "size": path.stat().st_size,
                "digest": "sha256:" + RECOVERY["digest"](path),
                "browser_download_url": "https://github.com/jjyr/pot/releases/download/v0.0.3/fixture",
            }
            with mock.patch("urllib.request.urlopen", return_value=io.BytesIO(b"same bytes")):
                RECOVERY["compare_public"](path, asset)
            with mock.patch("urllib.request.urlopen", return_value=io.BytesIO(b"other data")):
                with self.assertRaises(ValueError):
                    RECOVERY["compare_public"](path, asset)


if __name__ == "__main__":
    unittest.main()
