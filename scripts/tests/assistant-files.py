#!/usr/bin/env python3
"""Regress upgrade verification after assistant profiles moved out of SQLite."""

import hashlib
from pathlib import Path
import runpy
import shutil
import sqlite3
import subprocess
import sys
import tempfile
import unittest

REPO = Path(__file__).resolve().parents[2]
SCRIPT = REPO / "scripts/verify-assistant-files"
verify = runpy.run_path(str(SCRIPT))["verify"]


class AssistantFiles(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory(prefix="pot-assistant-files-")
        self.addCleanup(self.directory.cleanup)
        self.reference = Path(self.directory.name) / "reference with spaces"
        self.reference.mkdir()
        with sqlite3.connect(self.reference / "control.sqlite") as db:
            # The current registry deliberately has no name/revision columns.
            db.execute("CREATE TABLE assistant_definitions(id TEXT PRIMARY KEY, deleted INTEGER)")
            db.executemany(
                "INSERT INTO assistant_definitions VALUES (?, ?)", [("main", 0), ("retired", 1)]
            )
        for ident in ["main", "retired"]:
            folder = self.reference / "assistants" / ident
            folder.mkdir(parents=True)
            (folder / "profile.json").write_text('{"schema_version":1,"name":"Saved helper"}\n')
            (folder / "AGENTS.md").write_text("Retain these saved rules.\n")
        self.upgraded = self.reference.parent / "upgraded"
        shutil.copytree(self.reference, self.upgraded)

    def snapshot(self):
        return {
            str(path.relative_to(self.reference.parent)): hashlib.sha256(
                path.read_bytes()
            ).hexdigest()
            for root in [self.reference, self.upgraded]
            for path in root.rglob("*")
            if path.is_file()
        }

    def test_current_registry_and_retired_assistants_preserved_without_writes(self):
        before = self.snapshot()
        self.assertEqual(verify(self.reference, self.upgraded), {"assistants": 2, "files": 4})
        self.assertEqual(verify(self.reference, self.upgraded), {"assistants": 2, "files": 4})
        self.assertEqual(self.snapshot(), before)

    def test_changed_or_missing_profile_and_rules_fail(self):
        for name in ["profile.json", "AGENTS.md"]:
            path = self.upgraded / "assistants/retired" / name
            original = path.read_bytes()
            for content in [b"changed", None]:
                with self.subTest(name=name, content=content):
                    if content is None:
                        path.unlink()
                    else:
                        path.write_bytes(content)
                    with self.assertRaises((ValueError, FileNotFoundError)):
                        verify(self.reference, self.upgraded)
                    path.write_bytes(original)

    def test_absent_rules_are_valid_but_new_rules_fail(self):
        for root in [self.reference, self.upgraded]:
            (root / "assistants/main/AGENTS.md").unlink()
        self.assertEqual(verify(self.reference, self.upgraded), {"assistants": 2, "files": 3})
        (self.upgraded / "assistants/main/AGENTS.md").write_text("Unexpected rules")
        with self.assertRaisesRegex(ValueError, "saved file changed"):
            verify(self.reference, self.upgraded)

    def test_registry_changes_fail(self):
        with sqlite3.connect(self.upgraded / "control.sqlite") as db:
            db.execute("DELETE FROM assistant_definitions WHERE id='retired'")
        with self.assertRaisesRegex(ValueError, "identities changed"):
            verify(self.reference, self.upgraded)

    def test_same_root_and_missing_database_fail_without_creating_files(self):
        with self.assertRaisesRegex(ValueError, "independent copies"):
            verify(self.reference, self.reference)
        (self.upgraded / "control.sqlite").unlink()
        with self.assertRaises(sqlite3.OperationalError):
            verify(self.reference, self.upgraded)
        self.assertFalse((self.upgraded / "control.sqlite").exists())

    def test_symlinked_profile_is_rejected(self):
        path = self.upgraded / "assistants/main/profile.json"
        path.unlink()
        path.symlink_to(self.reference / "assistants/main/profile.json")
        with self.assertRaisesRegex(ValueError, "regular saved file"):
            verify(self.reference, self.upgraded)

    def test_cli_reports_failure_without_printing_rule_contents(self):
        path = self.upgraded / "assistants/main/AGENTS.md"
        path.write_text("private synthetic content")
        result = subprocess.run(
            [sys.executable, str(SCRIPT), str(self.reference), str(self.upgraded)],
            capture_output=True,
            text=True,
        )
        self.assertEqual(result.returncode, 1)
        self.assertIn("main/AGENTS.md", result.stderr)
        self.assertNotIn("private synthetic content", result.stdout + result.stderr)


if __name__ == "__main__":
    unittest.main()
