#!/usr/bin/env python3
"""Portable tests for a unified skill view and readonly mount enforcement."""

import importlib.util
import os
from pathlib import Path
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import MagicMock, patch

source = Path(__file__).resolve().parents[2] / "sandbox-images/linux/default/skill_mounts.py"
spec = importlib.util.spec_from_file_location("skill_mounts", source)
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class SkillMounts(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.base = Path(self.temp.name).resolve()
        self.sources = self.base / "sources"
        (self.sources / "user").mkdir(parents=True)
        for name in ["skill-creator", "skill-installer"]:
            bundle = self.sources / "builtin" / name
            bundle.mkdir(parents=True)
            (bundle / "SKILL.md").write_text("builtin")
        self.home = self.base / "home"
        self.view = self.home / ".pot/agent/skills"
        self.libc = MagicMock()
        self.libc.unshare.return_value = self.libc.mount.return_value = 0
        source_patch = patch.object(module, "SKILL_SOURCES", self.sources)
        libc_patch = patch.object(module.ctypes, "CDLL", return_value=self.libc)
        stat_patch = patch.object(module.os, "statvfs", return_value=SimpleNamespace(f_flag=0))
        source_patch.start()
        libc_patch.start()
        stat_patch.start()
        self.addCleanup(source_patch.stop)
        self.addCleanup(libc_patch.stop)
        self.addCleanup(stat_patch.stop)

    def test_readonly_remount_preserves_locked_container_flags(self):
        module.os.statvfs.return_value.f_flag = 2 | 4 | 8 | 4096
        module.skill_namespace("task", self.home)
        remounts = [c.args[3] for c in self.libc.mount.call_args_list if c.args[3] & 0x20]
        self.assertEqual(remounts, [0x1021 | 2 | 4 | 8 | 0x200000] * 3)

    def test_same_bundles_for_main_and_task_with_different_user_permissions(self):
        main = module.skill_mounts("main", self.home)
        task = module.skill_mounts("task", self.home)
        self.assertEqual([(s, t) for s, t, _ in main], [(s, t) for s, t, _ in task])
        self.assertFalse(main[0][2])
        self.assertTrue(all(readonly for _, _, readonly in task))
        self.assertTrue(all(readonly for _, _, readonly in main[1:]))
        self.assertEqual(
            {Path(t).name for _, t, _ in main[1:]}, {"skill-creator", "skill-installer"}
        )
        with self.assertRaises(ValueError):
            module.skill_mounts("forged", self.home)

    def test_main_binds_user_root_and_remounts_both_builtin_bundles_readonly(self):
        module.skill_namespace("main", self.home)
        readonly = {
            c.args[1].decode() for c in self.libc.mount.call_args_list if c.args[3] == 0x1021
        }
        self.assertEqual(
            readonly, {str(self.view / name) for name in ["skill-creator", "skill-installer"]}
        )
        self.libc.unshare.assert_called_once_with(0x20000)
        for name in ["skill-creator", "skill-installer"]:
            self.assertTrue((self.sources / "user" / name).is_dir())
            self.assertFalse((self.sources / "user" / name / "SKILL.md").exists())

    def test_task_also_remounts_user_root_readonly(self):
        module.skill_namespace("task", self.home)
        readonly = {
            c.args[1].decode() for c in self.libc.mount.call_args_list if c.args[3] == 0x1021
        }
        self.assertEqual(
            readonly,
            {str(self.view), str(self.view / "skill-creator"), str(self.view / "skill-installer")},
        )

    def test_unshare_failure_prevents_all_mounts(self):
        self.libc.unshare.return_value = -1
        with self.assertRaises(OSError):
            module.skill_namespace("main", self.home)
        self.libc.mount.assert_not_called()

    def test_readonly_remount_failure_does_not_fall_back(self):
        self.libc.mount.side_effect = [0, 0, 0, -1]
        with self.assertRaises(OSError):
            module.skill_namespace("main", self.home)
        self.assertEqual(self.libc.mount.call_count, 4)
        self.assertEqual(self.libc.mount.call_args.args[3], 0x1021)

    def test_symlink_sources_and_builtin_mountpoints_are_rejected(self):
        outside = self.base / "outside"
        outside.mkdir()
        placeholder = self.sources / "user/skill-creator"
        placeholder.symlink_to(outside, target_is_directory=True)
        with self.assertRaisesRegex(ValueError, "symlink"):
            module.skill_namespace("main", self.home)
        self.assertEqual(self.libc.mount.call_count, 1)
        placeholder.unlink()
        builtin = self.sources / "builtin/skill-creator"
        (builtin / "SKILL.md").unlink()
        builtin.rmdir()
        builtin.symlink_to(outside, target_is_directory=True)
        self.libc.mount.reset_mock()
        with self.assertRaisesRegex(ValueError, "symlink"):
            module.skill_namespace("main", self.home)
        self.libc.mount.assert_not_called()

    def test_symlink_in_home_cannot_redirect_the_mount(self):
        outside = self.base / "outside"
        outside.mkdir()
        self.home.mkdir()
        (self.home / ".pot").symlink_to(outside, target_is_directory=True)
        with self.assertRaisesRegex(ValueError, "symlink"):
            module.skill_namespace("main", self.home)
        self.assertEqual(self.libc.mount.call_count, 1)

    def test_restrictive_umask_keeps_home_view_traversable(self):
        old = os.umask(0o077)
        try:
            module.skill_namespace("task", self.home)
        finally:
            os.umask(old)
        for directory in [self.home / ".pot", self.view.parent, self.view]:
            self.assertEqual(directory.stat().st_mode & 0o777, 0o755)

    def test_missing_source_fails_without_unrestricted_execution(self):
        (self.sources / "user").rmdir()
        with self.assertRaisesRegex(ValueError, "missing"):
            module.skill_namespace("task", self.home)
        self.libc.unshare.assert_not_called()


if __name__ == "__main__":
    unittest.main()
