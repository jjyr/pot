#!/usr/bin/env python3
"""Portable unit tests for the guest's per-scope mount policy and fail-closed setup."""

import importlib.util
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import MagicMock, patch

source = Path(__file__).resolve().parents[2] / "image/skill_mounts.py"
spec = importlib.util.spec_from_file_location("skill_mounts", source)
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class SkillMounts(unittest.TestCase):
    def test_unshare_failure_prevents_all_mounts(self):
        libc = MagicMock()
        libc.unshare.return_value = -1
        with patch.object(module.ctypes, "CDLL", return_value=libc):
            with self.assertRaises(OSError):
                module.skill_namespace("task")
        libc.mount.assert_not_called()

    def test_readonly_remount_failure_stops_before_other_sources(self):
        libc = MagicMock()
        libc.unshare.return_value = 0
        # Private mounts, tmpfs, bind, then failed readonly remount.
        libc.mount.side_effect = [0, 0, 0, -1]
        with tempfile.TemporaryDirectory() as temp:
            base = Path(temp).resolve()
            (base / "sources/builtin/shared").mkdir(parents=True)
            with (
                patch.object(module, "SKILL_SOURCES", base / "sources"),
                patch.object(module, "SKILL_VIEW", base / "view"),
                patch.object(module.ctypes, "CDLL", return_value=libc),
            ):
                with self.assertRaises(OSError):
                    module.skill_namespace("task")
            self.assertEqual(libc.mount.call_count, 4)
            self.assertEqual(libc.mount.call_args.args[3], 0x1021)
            self.assertFalse((base / "view/user").exists())

    def test_symlink_sources_and_symlink_parents_are_rejected_before_bind(self):
        for parent_link in (False, True):
            with self.subTest(parent_link=parent_link), tempfile.TemporaryDirectory() as temp:
                base = Path(temp).resolve()
                (base / "outside/shared").mkdir(parents=True)
                (base / "sources").mkdir()
                if parent_link:
                    (base / "sources/builtin").symlink_to(
                        base / "outside", target_is_directory=True
                    )
                else:
                    (base / "sources/builtin").mkdir()
                    (base / "sources/builtin/shared").symlink_to(
                        base / "outside/shared", target_is_directory=True
                    )
                libc = MagicMock()
                libc.unshare.return_value = libc.mount.return_value = 0
                with (
                    patch.object(module, "SKILL_SOURCES", base / "sources"),
                    patch.object(module, "SKILL_VIEW", base / "view"),
                    patch.object(module.ctypes, "CDLL", return_value=libc),
                ):
                    with self.assertRaisesRegex(ValueError, "symlink"):
                        module.skill_namespace("task")
                self.assertEqual(libc.mount.call_count, 2)

    def test_restrictive_umask_keeps_missing_sources_traversable_without_binding(self):
        libc = MagicMock()
        libc.unshare.return_value = libc.mount.return_value = 0
        with tempfile.TemporaryDirectory() as temp:
            base = Path(temp).resolve()
            old_umask = os.umask(0o077)
            try:
                with (
                    patch.object(module, "SKILL_SOURCES", base / "missing"),
                    patch.object(module, "SKILL_VIEW", base / "view"),
                    patch.object(module.ctypes, "CDLL", return_value=libc),
                ):
                    module.skill_namespace("task")
                    for directory in [
                        "builtin",
                        "builtin/shared",
                        "builtin/task",
                        "user",
                        "user/shared",
                        "user/task",
                    ]:
                        self.assertEqual((base / "view" / directory).stat().st_mode & 0o777, 0o755)
                    self.assertFalse((base / "view/user/main").exists())
                    self.assertEqual(libc.mount.call_count, 2)
            finally:
                os.umask(old_umask)

    def test_worker_view_has_only_shared_and_task_and_is_readonly(self):
        mounts = module.skill_mounts("task")
        self.assertEqual(len(mounts), 4)
        self.assertTrue(all(readonly for _, _, readonly in mounts))
        self.assertFalse(any(target.endswith("/main") for _, target, _ in mounts))
        with self.assertRaises(ValueError):
            module.skill_mounts("forged")

    def test_main_can_manage_all_user_roles_but_not_builtins(self):
        mounts = module.skill_mounts("main")
        self.assertEqual(sum(not ro for _, _, ro in mounts), 3)
        self.assertTrue(all(ro for source, _, ro in mounts if "/builtin/" in source))

    def test_namespace_remounts_readonly_and_never_ignores_failures(self):
        libc = MagicMock()
        libc.unshare.return_value = libc.mount.return_value = 0
        with tempfile.TemporaryDirectory() as temp:
            base = Path(temp).resolve()
            for source in ["user", "builtin"]:
                for role in ["shared", "main", "task"]:
                    (base / "sources" / source / role).mkdir(parents=True)
            with (
                patch.object(module, "SKILL_SOURCES", base / "sources"),
                patch.object(module, "SKILL_VIEW", base / "view"),
                patch.object(module.ctypes, "CDLL", return_value=libc),
            ):
                module.skill_namespace("task")
                readonly = [c for c in libc.mount.call_args_list if c.args[3] == 0x1021]
                self.assertEqual(len(readonly), 4)
                libc.unshare.assert_called_once_with(0x20000)
                self.assertEqual((base / "view/user").stat().st_mode & 0o777, 0o755)
                self.assertEqual((base / "view/builtin").stat().st_mode & 0o777, 0o755)
                libc.mount.return_value = -1
                with self.assertRaises(OSError):
                    module.skill_namespace("task")


if __name__ == "__main__":
    unittest.main()
