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
            if endpoint.startswith("git/ref/tags/"):
                return {"object": {"type": "tag", "sha": "c" * 40}}
            if endpoint.startswith("git/tags/"):
                return {
                    "tag": "v1.2.3",
                    "object": {"type": "commit", "sha": "b" * 40 if moved else SHA},
                    "message": json.dumps({"format": 1, "version": "1.2.3", "sha": SHA}),
                }
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

    def test_recovery_requires_annotated_source_and_version_binding(self):
        verify = namespace["verify_tag"]
        good = {
            "tag": "v1.2.3",
            "object": {"type": "commit", "sha": SHA},
            "message": json.dumps({"format": 1, "version": "1.2.3", "sha": SHA}),
        }
        for changed in [
            {"tag": "v9.9.9"},
            {"object": {"type": "commit", "sha": "b" * 40}},
            {"message": "not-json"},
            {"message": json.dumps({"format": 1, "version": "1.2.4", "sha": SHA})},
        ]:
            with mock.patch.dict(
                verify.__globals__,
                api=mock.Mock(
                    side_effect=[{"object": {"type": "tag", "sha": "c" * 40}}, {**good, **changed}]
                ),
            ):
                with self.assertRaises(ValueError):
                    verify(REPO, "1.2.3", SHA)
        with mock.patch.dict(
            verify.__globals__,
            api=mock.Mock(return_value={"object": {"type": "commit", "sha": SHA}}),
        ):
            with self.assertRaisesRegex(ValueError, "annotated"):
                verify(REPO, "1.2.3", SHA)

    def test_api_retries_transient_reads_but_never_writes(self):
        error = subprocess.CompletedProcess(
            [], 1, '{"status":502,"message":"unavailable"}', "HTTP 502"
        )
        success = subprocess.CompletedProcess([], 0, '{"id":7}', "")
        with (
            mock.patch("subprocess.run", side_effect=[error, success]) as run,
            mock.patch("release_transport.time.sleep"),
        ):
            self.assertEqual(api(REPO, "releases/7"), {"id": 7})
            self.assertEqual(run.call_count, 2)
        with mock.patch("subprocess.run", return_value=error) as run:
            with self.assertRaisesRegex(RuntimeError, "HTTP=502"):
                api(REPO, "pulls", {"title": "fixture"})
            self.assertEqual(run.call_count, 1)

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
        publication = (ROOT / ".github/workflows/release-publish.yml").read_text()
        homebrew = (ROOT / ".github/workflows/release-homebrew.yml").read_text()
        self.assertNotIn("  homebrew:", publication)
        for contract in [
            "workflow_run:",
            "workflows: [Release publish]",
            "workflow_dispatch:",
            "--publish-run-id",
            "github.event.repository.default_branch",
            "pull-requests: write",
            "github.token",
            "if: always()",
        ]:
            self.assertIn(contract, homebrew)
        for forbidden in [
            "continue-on-error:",
            "secrets.",
            "git push",
            "gh pr merge",
            "workflow_run.head_sha",
        ]:
            self.assertNotIn(forbidden, homebrew)

    def test_published_source_rejects_untrusted_events_before_candidate_access(self):
        select = namespace["source_from_publish"]
        valid = {
            "id": 20,
            "workflow_id": 5,
            "path": ".github/workflows/release-publish.yml",
            "event": "workflow_dispatch",
            "head_branch": "main",
            "head_sha": SHA,
            "status": "completed",
            "conclusion": "success",
            "run_attempt": 1,
            "repository": {"full_name": REPO},
            "head_repository": {"full_name": REPO},
            "display_title": "release-publish [11111111-2222-4333-8444-555555555555] prepare=10 manifest=70",
        }

        def fake_api(repository, endpoint):
            if endpoint == "":
                return {"default_branch": "main"}
            if endpoint == "actions/workflows/release-publish.yml":
                return {"id": 5, "path": ".github/workflows/release-publish.yml", "state": "active"}
            return current

        with (
            mock.patch.dict(select.__globals__, api=fake_api),
            mock.patch(
                "release_candidate.inspect_candidate",
                return_value={"run_id": 10, "manifest_id": 70, "version": "1.2.3", "sha": SHA},
            ) as inspect,
        ):
            current = valid
            self.assertEqual(select(20, REPO), ("1.2.3", SHA))
            inspect.assert_called_once_with(10, 70)
            for change in [
                {"head_branch": "untrusted"},
                {"event": "pull_request"},
                {"head_repository": {"full_name": "attacker/fork"}},
                {"conclusion": "failure"},
                {"workflow_id": 9},
                {"path": "wrong.yml"},
                {"run_attempt": 0},
                {"display_title": "untrusted"},
                {"id": 21},
            ]:
                current = {**valid, **change}
                inspect.reset_mock()
                with self.assertRaises(ValueError):
                    select(20, REPO)
                inspect.assert_not_called()
            current = valid
            inspect.return_value = {"run_id": 11, "manifest_id": 70}
            with self.assertRaisesRegex(ValueError, "identity mismatch"):
                select(20, REPO)


if __name__ == "__main__":
    unittest.main()
