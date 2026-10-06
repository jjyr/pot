"""Root-only per-scope skill mount views; no Agent role logic in pot-agent."""

import ctypes
import os
from pathlib import Path

SKILL_SOURCES = Path("/run/pot-skill-sources")
SKILL_VIEW = Path("/mnt/pot-skills")


def skill_mounts(role):
    if role not in ("main", "task"):
        raise ValueError("Invalid execution role")
    user_roles = ("shared", "main", "task") if role == "main" else ("shared", "task")
    return [
        (
            str(SKILL_SOURCES / source / directory),
            str(SKILL_VIEW / source / directory),
            source == "builtin" or role == "task",
        )
        for source, directories in [("builtin", ("shared", role)), ("user", user_roles)]
        for directory in directories
    ]


def skill_namespace(role):
    libc = ctypes.CDLL(None, use_errno=True)
    libc.mount.argtypes = [
        ctypes.c_char_p,
        ctypes.c_char_p,
        ctypes.c_char_p,
        ctypes.c_ulong,
        ctypes.c_void_p,
    ]

    def checked(result):
        if result != 0:
            raise OSError(ctypes.get_errno(), "Skill mount isolation failed")

    def mount(source, target, kind=None, flags=0, data=None):
        checked(
            libc.mount(
                source.encode() if source else None,
                str(target).encode(),
                kind.encode() if kind else None,
                flags,
                data.encode() if data else None,
            )
        )

    checked(libc.unshare(0x00020000))  # CLONE_NEWNS, before dropping privileges
    mount(None, "/", flags=0x40000 | 0x4000)  # MS_PRIVATE | MS_REC
    mount("tmpfs", SKILL_VIEW, "tmpfs", data="mode=0755,size=1m")
    for source, target, readonly in skill_mounts(role):
        Path(target).mkdir(mode=0o755, parents=True, exist_ok=True)
        os.chmod(Path(target).parent, 0o755)
        os.chmod(target, 0o755)
        if Path(source).is_symlink() or Path(source).resolve() != Path(source):
            raise ValueError("Skill source must not be a symlink")
        if Path(source).is_dir():
            mount(source, target, flags=0x1000)  # MS_BIND
            if readonly:
                mount(None, target, flags=0x1000 | 0x20 | 1)  # MS_BIND | MS_REMOUNT | MS_RDONLY
