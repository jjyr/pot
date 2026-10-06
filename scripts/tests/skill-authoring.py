#!/usr/bin/env python3
"""Builtin creator/installer regressions, using shared Runtime format fixtures."""

import importlib.util
import io
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch
import zipfile

REPO = Path(__file__).resolve().parents[2]
CREATOR = REPO / "skills/main/skill-creator/scripts/quick_validate.py"
INSTALLER = REPO / "skills/main/skill-installer/scripts/install_skill.py"


def load(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


validator = load("validator", CREATOR)
installer = load("installer", INSTALLER)


class Authoring(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.base = Path(self.temp.name)
        self.bundle = self.base / "example"
        self.bundle.mkdir()
        self.write("---\nname: example\ndescription: useful\n---\nInstructions\n")

    def write(self, text):
        (self.bundle / "SKILL.md").write_text(text, encoding="utf-8")

    def test_shared_loader_format_cases(self):
        cases = json.loads((REPO / "scripts/tests/skill-format-cases.json").read_text())
        for case in cases:
            with self.subTest(case=case["id"]):
                self.write(case["text"])
                valid, message = validator.validate_skill(self.bundle)
                self.assertEqual(valid, case["valid"], message)

    def test_both_packaged_bundles_validate(self):
        for name in ["skill-creator", "skill-installer"]:
            valid, message = validator.validate_skill(REPO / "skills/main" / name)
            self.assertTrue(valid, message)

    def test_authoring_constraints_and_cli_exit_status(self):
        for text in [
            "---\ndescription: useful\n---\n",
            "---\nname: other\ndescription: useful\n---\n",
            "---\nname: example\ndescription: '[TODO: describe]'\n---\n",
            "---\nname: example\ndescription: useful\n---\n[TODO: instructions]\n",
            "---\nname: example\ndescription: useful\nunknown: value\n---\n",
            "---\nname: example\ndescription: useful\n---suffix\n",
        ]:
            with self.subTest(text=text):
                self.write(text)
                self.assertFalse(validator.validate_skill(self.bundle)[0])
        result = subprocess.run(
            [sys.executable, str(CREATOR), str(self.bundle)], capture_output=True
        )
        self.assertEqual(result.returncode, 1)
        self.write("---\nname: example\ndescription: useful\n---\n```\n[TODO: example]\n```\n")
        result = subprocess.run(
            [sys.executable, str(CREATOR), str(self.bundle)], capture_output=True
        )
        self.assertEqual(result.returncode, 0)

    def test_size_and_metadata_limits(self):
        for name, description in [("x" * 65, "useful"), ("example", "x" * 1025)]:
            self.write(f"---\nname: {name}\ndescription: {description}\n---\n")
            self.assertFalse(validator.validate_skill(self.bundle)[0])
        self.write("x" * (1024 * 1024 + 1))
        self.assertFalse(validator.validate_skill(self.bundle)[0])

    def test_local_resources_exist_and_cannot_escape_or_use_symlinks(self):
        self.write("---\nname: example\ndescription: useful\n---\n[guide](references/guide.md)\n")
        self.assertFalse(validator.validate_skill(self.bundle)[0])
        (self.bundle / "references").mkdir()
        (self.bundle / "references/guide.md").write_text("guide")
        self.assertTrue(validator.validate_skill(self.bundle)[0])
        self.write("---\nname: example\ndescription: useful\n---\n[escape](../outside)\n")
        (self.base / "outside").write_text("outside")
        self.assertFalse(validator.validate_skill(self.bundle)[0])
        (self.bundle / "link").symlink_to(self.base / "outside")
        self.assertFalse(validator.validate_skill(self.bundle)[0])

    def test_install_preserves_resources_permissions_and_does_not_execute_scripts(self):
        script = self.bundle / "script.sh"
        script.write_text("#!/bin/sh\ntouch SHOULD_NOT_EXIST\n")
        script.chmod(0o700)
        (self.bundle / "SKILL.md").chmod(0o600)
        target = installer.install_bundle(self.bundle, self.base / "installed")
        self.assertEqual((target / "script.sh").read_text(), script.read_text())
        self.assertEqual((target / "script.sh").stat().st_mode & 0o777, 0o755)
        self.assertEqual((target / "SKILL.md").stat().st_mode & 0o777, 0o644)
        self.assertFalse((self.bundle / "SHOULD_NOT_EXIST").exists())

    def test_conflict_requires_explicit_replace_and_invalid_update_preserves_original(self):
        destination = self.base / "installed"
        target = installer.install_bundle(self.bundle, destination)
        original = (target / "SKILL.md").read_text()
        self.write("---\nname: example\ndescription: updated\n---\n")
        with self.assertRaisesRegex(ValueError, "already exists"):
            installer.install_bundle(self.bundle, destination)
        self.assertEqual((target / "SKILL.md").read_text(), original)
        installer.install_bundle(self.bundle, destination, replace=True)
        self.assertIn("updated", (target / "SKILL.md").read_text())
        self.write("broken")
        with self.assertRaises(ValueError):
            installer.install_bundle(self.bundle, destination, replace=True)
        self.assertIn("updated", (target / "SKILL.md").read_text())
        self.assertEqual(list(destination.iterdir()), [target])

    def test_failed_publication_restores_previous_bundle(self):
        destination = self.base / "installed"
        target = installer.install_bundle(self.bundle, destination)
        original = (target / "SKILL.md").read_text()
        rename = Path.rename

        def fail_stage(path, new):
            if path.parent.name.startswith(".skill-install-") and path.name == "example":
                raise OSError("Injected publication failure")
            return rename(path, new)

        with patch.object(Path, "rename", fail_stage):
            with self.assertRaises(OSError):
                installer.install_bundle(self.bundle, destination, replace=True)
        self.assertEqual((target / "SKILL.md").read_text(), original)
        self.assertEqual(list(destination.iterdir()), [target])

    def test_github_selection_and_rejection_of_escaping_paths(self):
        self.assertEqual(
            installer.github_source(
                None, None, "main", "https://github.com/owner/repo/tree/v1/path/example"
            ),
            ("owner/repo", "path/example", "v1"),
        )
        self.assertEqual(
            installer.github_source("owner/repo", "path/example", "feature/skills"),
            ("owner/repo", "path/example", "feature/skills"),
        )
        for path in ["../example", "/example", "path/../example", "path\\example"]:
            with self.assertRaises(ValueError):
                installer.github_source("owner/repo", path, "main")

    def test_archive_extracts_only_selected_bundle_and_preserves_executables(self):
        archive = self.base / "repo.zip"
        with zipfile.ZipFile(archive, "w") as zipped:
            zipped.writestr(
                "repo-main/path/example/SKILL.md", (self.bundle / "SKILL.md").read_text()
            )
            zipped.writestr("repo-main/unrelated/file", "ignore")
            info = zipfile.ZipInfo("repo-main/path/example/run.sh")
            info.external_attr = 0o100755 << 16
            zipped.writestr(info, "#!/bin/sh\nexit 0\n")
        target = self.base / "download/example"
        installer.extract_bundle(archive, "path/example", target)
        self.assertTrue(validator.validate_skill(target)[0])
        self.assertEqual((target / "run.sh").stat().st_mode & 0o777, 0o755)
        self.assertFalse((target / "unrelated").exists())

    def test_archive_rejects_traversal_and_symlinks(self):
        for filename, mode in [("../outside", 0o100644), ("link", 0o120777)]:
            with self.subTest(filename=filename):
                archive = self.base / "bad.zip"
                with zipfile.ZipFile(archive, "w") as zipped:
                    info = zipfile.ZipInfo("repo-main/path/example/" + filename)
                    info.external_attr = mode << 16
                    zipped.writestr(info, "outside")
                with self.assertRaises(ValueError):
                    installer.extract_bundle(
                        archive, "path/example", self.base / "download/example"
                    )
                self.assertFalse((self.base / "download/outside").exists())

    def test_github_cli_downloads_and_installs_without_executing_resources(self):
        archive = io.BytesIO()
        with zipfile.ZipFile(archive, "w") as zipped:
            zipped.writestr(
                "repo-main/path/example/SKILL.md", (self.bundle / "SKILL.md").read_text()
            )
        archive.seek(0)
        destination = self.base / "installed"
        with patch.object(installer, "urlopen", return_value=archive) as request:
            self.assertEqual(
                installer.main(
                    ["--repo", "owner/repo", "--path", "path/example", "--dest", str(destination)]
                ),
                0,
            )
            self.assertEqual(
                request.call_args.args[0].full_url,
                "https://codeload.github.com/owner/repo/zip/main",
            )
        self.assertTrue(validator.validate_skill(destination / "example")[0])

    def test_install_cli_reports_download_failure_without_creating_destination(self):
        destination = self.base / "installed"
        with patch.object(installer, "urlopen", side_effect=OSError("Download failed")):
            self.assertEqual(
                installer.main(
                    ["--repo", "owner/repo", "--path", "path/example", "--dest", str(destination)]
                ),
                1,
            )
        self.assertFalse(destination.exists())


if __name__ == "__main__":
    unittest.main()
