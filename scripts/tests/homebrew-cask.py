#!/usr/bin/env python3
"""Exercise post-publication Homebrew verification and non-destructive PR retries."""

import base64
import hashlib
import json
import io
import tarfile
from pathlib import Path
import plistlib
import runpy
import subprocess
import sys
import unittest
from unittest import mock
import zipfile

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "scripts"))
from release_contract import release_assets  # noqa: E402

namespace = runpy.run_path(str(ROOT / "scripts/sync-homebrew"))
propose = namespace["propose"]
sync = namespace["sync"]
api = namespace["api"]
SHA = "a" * 40
REPO = "jjyr/pot"
CASK = 'cask "pot" do\n  version "1.2.3"\nend\n'
FORMULA = 'class PotCli < Formula\n  version "1.2.3"\nend\n'
DISABLED = 'cask "pot" do\n  disable! date: "2026-01-01", because: "first release pending"\nend\n'


def content(value):
    return {"content": base64.b64encode(value.encode()).decode()}


class Proposal(unittest.TestCase):
    def fixture(self, existing=False, prs=None, base=None, changed=None):
        self.calls = []

        def call(repository, endpoint, data=None, missing=False):
            self.assertEqual(repository, REPO)
            self.calls.append((endpoint, data))
            if endpoint == "":
                return {"default_branch": "main"}
            if endpoint == "commits/main":
                return {"sha": SHA}
            if endpoint == "contents/Casks/pot.rb?ref=" + SHA:
                return content(base if base is not None else DISABLED)
            if endpoint == "contents/Formula/pot-cli.rb?ref=" + SHA:
                return content(FORMULA if base == CASK else DISABLED)
            if endpoint.startswith("pulls?"):
                return prs or []
            if endpoint.startswith("git/ref/heads/"):
                return {"object": {"sha": "b" * 40}} if existing else None
            if endpoint.startswith("contents/"):
                return content(changed or (FORMULA if "Formula/" in endpoint else CASK))
            if endpoint.startswith("compare/"):
                return {"files": [{"filename": "Casks/pot.rb"}, {"filename": "Formula/pot-cli.rb"}]}
            if endpoint == "git/commits/" + SHA:
                return {"tree": {"sha": "base-tree"}}
            if endpoint == "git/trees":
                self.assertEqual(
                    [f["path"] for f in data["tree"]], ["Casks/pot.rb", "Formula/pot-cli.rb"]
                )
                return {"sha": "new-tree"}
            if endpoint == "git/commits":
                self.assertEqual(data["parents"], [SHA])
                return {"sha": "new-commit"}
            if endpoint == "git/refs":
                self.assertEqual(data["ref"], "refs/heads/homebrew/pot-v1.2.3")
                return {}
            if endpoint == "pulls":
                self.assertEqual(data["base"], "main")
                return {"html_url": "https://example.test/pr/1"}
            self.fail(endpoint)

        return call

    def test_creates_only_package_branch_and_pr(self):
        with mock.patch.dict(propose.__globals__, api=self.fixture()):
            self.assertIn("Created", propose(REPO, "1.2.3", CASK, FORMULA))
        self.assertEqual(
            [e for e, d in self.calls if d], ["git/trees", "git/commits", "git/refs", "pulls"]
        )

    def test_retry_reuses_identical_branch_and_open_pr(self):
        prs = [{"state": "open", "base": {"ref": "main"}, "html_url": "existing"}]
        with mock.patch.dict(propose.__globals__, api=self.fixture(existing=True, prs=prs)):
            self.assertEqual(
                propose(REPO, "1.2.3", CASK, FORMULA), "Existing Homebrew PR: existing"
            )
        self.assertFalse(any(d for _, d in self.calls))

    def test_branch_surviving_pr_permission_failure_can_be_reused(self):
        with mock.patch.dict(propose.__globals__, api=self.fixture(existing=True)):
            self.assertIn("Created", propose(REPO, "1.2.3", CASK, FORMULA))
        self.assertEqual([e for e, d in self.calls if d], ["pulls"])

    def test_conflicting_branch_and_closed_pr_are_never_rewritten(self):
        for options in [
            {"existing": True, "changed": "someone else's change"},
            {"prs": [{"state": "closed", "base": {"ref": "main"}}]},
        ]:
            with (
                self.subTest(options=options),
                mock.patch.dict(propose.__globals__, api=self.fixture(**options)),
            ):
                with self.assertRaises(ValueError):
                    propose(REPO, "1.2.3", CASK, FORMULA)
                self.assertFalse(any(d for _, d in self.calls))

    def test_no_downgrade_or_equal_version_replacement(self):
        for version in ["1.2.3", "2.0.0"]:
            with mock.patch.dict(
                propose.__globals__, api=self.fixture(base=f'  version "{version}"\n')
            ):
                with self.assertRaisesRegex(ValueError, "equal or newer"):
                    propose(REPO, "1.2.3", CASK, FORMULA)
                self.assertFalse(any(d for _, d in self.calls))

    def test_already_merged_cask_needs_no_branch_or_pr(self):
        with mock.patch.dict(propose.__globals__, api=self.fixture(base=CASK)):
            self.assertIn("already synchronized", propose(REPO, "1.2.3", CASK, FORMULA))
        self.assertFalse(any(d for _, d in self.calls))

    def test_newer_formula_also_stops_the_entire_proposal(self):
        original = self.fixture()

        def newer(repository, endpoint, *args, **kwargs):
            if endpoint == "contents/Formula/pot-cli.rb?ref=" + SHA:
                return content('  version "2.0.0"\n')
            return original(repository, endpoint, *args, **kwargs)

        with mock.patch.dict(propose.__globals__, api=newer):
            with self.assertRaisesRegex(ValueError, "equal or newer"):
                propose(REPO, "1.2.3", CASK, FORMULA)
        self.assertFalse(any(d for _, d in self.calls))

    def test_unrelated_branch_changes_stop_sync(self):
        original = self.fixture(existing=True)

        def changed(repository, endpoint, *args, **kwargs):
            if endpoint.startswith("compare/"):
                return {"files": [{"filename": "README.md"}]}
            return original(repository, endpoint, *args, **kwargs)

        with mock.patch.dict(propose.__globals__, api=changed):
            with self.assertRaisesRegex(ValueError, "other changes"):
                propose(REPO, "1.2.3", CASK, FORMULA)
        self.assertFalse(any(d for _, d in self.calls))


class PublishedBytes(unittest.TestCase):
    def exercise(self, corrupt=None, draft=False, moved=False):
        assets = [{"name": "Pot-1.2.3-arm64.zip"}, {"name": "pot-1.2.3-darwin-arm64.tar.gz"}]
        proposed = mock.Mock(return_value="PR")

        def call(repository, endpoint):
            if endpoint.startswith("commits/"):
                return {"sha": "b" * 40 if moved else SHA}
            return {"draft": draft, "prerelease": False, "assets": assets}

        def download(command, **kwargs):
            output = Path(command[-1])
            for asset in assets:
                path = output / asset["name"]
                bad = corrupt
                if corrupt and corrupt.startswith("cli-"):
                    bad = corrupt.removeprefix("cli-") if path.suffix != ".zip" else None
                version = "9.9.9" if bad == "version" else "1.2.3"
                if path.suffix == ".zip":
                    with zipfile.ZipFile(path, "w") as packed:
                        packed.writestr(
                            "Pot.app/Contents/Info.plist",
                            plistlib.dumps({"CFBundleShortVersionString": version}),
                        )
                        packed.writestr("Pot.app/Contents/final-seal", "fixture final signed bytes")
                else:
                    with tarfile.open(path, "w:gz") as packed:
                        for name, data in {
                            "bin/pot": b"fixture binary",
                            "Resources/update.json": json.dumps(
                                {"version": version, "kind": "cli", "platform": "darwin-arm64"}
                            ).encode(),
                        }.items():
                            header = tarfile.TarInfo("pot-darwin-arm64/" + name)
                            header.size, header.mode = len(data), 0o755
                            packed.addfile(header, io.BytesIO(data))
                digest = hashlib.sha256(path.read_bytes()).hexdigest()
                path.with_name(path.name + ".sha256").write_text(f"{digest}  {path.name}\n")
                asset.update(digest="sha256:" + digest, size=path.stat().st_size)
                if bad == "digest":
                    asset["digest"] = "sha256:" + "0" * 64
                if bad == "size":
                    asset["size"] += 1
                if bad == "sidecar":
                    path.with_name(path.name + ".sha256").write_text("invalid")

        with (
            mock.patch.dict(sync.__globals__, api=call, propose=proposed),
            mock.patch("subprocess.run", side_effect=download),
        ):
            if corrupt or draft or moved:
                with self.assertRaises(ValueError):
                    sync("1.2.3", SHA, REPO)
                proposed.assert_not_called()
            else:
                self.assertEqual(sync("1.2.3", SHA, REPO), "PR")
                cask = proposed.call_args.args[2]
                self.assertIn(f'sha256 "{assets[0]["digest"].split(":")[1]}"', cask)
                self.assertIn('version "1.2.3"', cask)
                self.assertNotIn("disable!", cask)
                self.assertNotIn("no_check", cask)
                formula = proposed.call_args.args[3]
                self.assertIn(assets[1]["digest"].split(":")[1], formula)
                self.assertIn("pot-1.2.3-darwin-arm64.tar.gz", formula)

    def test_final_published_bytes_determine_installable_cask(self):
        self.exercise()

    def test_bad_bytes_sidecar_version_draft_or_moved_tag_never_propose(self):
        for options in [
            {"corrupt": key}
            for key in [
                "digest",
                "sidecar",
                "version",
                "cli-digest",
                "cli-sidecar",
                "cli-version",
                "cli-size",
            ]
        ] + [
            {"draft": True},
            {"moved": True},
        ]:
            with self.subTest(options=options):
                self.exercise(**options)

    def test_repository_cask_has_honest_metadata_for_its_publication_state(self):
        self.assertNotIn("pot.rb", release_assets("1.2.3", "macos"))
        cask = (ROOT / "Casks/pot.rb").read_text()
        self.assertNotIn(":no_check", cask)
        self.assertNotIn(":latest", cask)
        if "disable!" in cask:
            for stanza in ["  version ", "  sha256 ", "  url "]:
                self.assertNotIn(stanza, cask)
        else:
            self.assertRegex(cask, r'  version "[0-9]+\.[0-9]+\.[0-9]+"')
            self.assertRegex(cask, r'  sha256 "[a-f0-9]{64}"')
            self.assertIn("/v#{version}/Pot-#{version}-arm64.zip", cask)

    def test_api_permission_failures_are_reported_without_retry(self):
        for endpoint, status in [("pulls", 403), ("pulls", 422), ("git/refs", 403)]:
            with mock.patch(
                "subprocess.run",
                return_value=subprocess.CompletedProcess(
                    [], 1, json.dumps({"status": status}), "permission denied"
                ),
            ) as run:
                with self.assertRaisesRegex(RuntimeError, "permission denied"):
                    api(REPO, endpoint, {"title": "fixture"})
                self.assertEqual(run.call_count, 1)

    def test_workflow_separates_publication_from_sync_failure(self):
        workflow = (ROOT / ".github/workflows/release-publish.yml").read_text()
        homebrew = workflow.split("  homebrew:\n")[1]
        self.assertIn("needs: publish", homebrew)
        self.assertNotIn("continue-on-error:", homebrew)
        self.assertIn("if: failure()", homebrew)
        self.assertIn("pull-requests: write", homebrew)
        self.assertIn("github.token", homebrew)
        self.assertIn("::warning::macOS assets are published", homebrew)
        self.assertNotIn("secrets.", homebrew)
        self.assertNotIn("git push", homebrew)


if __name__ == "__main__":
    unittest.main()
