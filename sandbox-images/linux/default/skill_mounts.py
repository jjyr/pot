"""A flat skills view at the execution user's home, with readonly builtin bundles."""

import ctypes
import os
from pathlib import Path

SKILL_SOURCES = Path("/run/pot-skill-sources")


def ordinary_directory(path):
    if path.is_symlink() or path.resolve() != path:
        raise ValueError("Skill source must not be a symlink")
    if not path.is_dir():
        raise ValueError("Skill source directory missing")


def skill_mounts(role, home):
    if role not in ("main", "task"):
        raise ValueError("Invalid execution role")
    view = Path(home) / ".pot/agent/skills"
    user = SKILL_SOURCES / "user"
    builtin = SKILL_SOURCES / "builtin"
    ordinary_directory(user)
    ordinary_directory(builtin)
    mounts = [(str(user), str(view), role == "task")]
    for bundle in sorted(builtin.iterdir()):
        if bundle.name.startswith(".") or not bundle.is_dir():
            continue
        ordinary_directory(bundle)
        if not (bundle / "SKILL.md").is_file() or (bundle / "SKILL.md").is_symlink():
            raise ValueError("Invalid builtin skill bundle")
        mounts.append((str(bundle), str(view / bundle.name), True))
    return mounts


def skill_namespace(role, home):
    mounts = skill_mounts(role, home)
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

    def mount(source, target, flags=0):
        checked(
            libc.mount(source.encode() if source else None, str(target).encode(), None, flags, None)
        )

    checked(libc.unshare(0x00020000))
    mount(None, "/", flags=0x40000 | 0x4000)  # MS_PRIVATE | MS_REC
    view = Path(home) / ".pot/agent/skills"
    # Create builtin mountpoints in the writable source before the task view
    # becomes readonly. They contain no builtin files on the backing store.
    for _, target, _ in mounts[1:]:
        placeholder = SKILL_SOURCES / "user" / Path(target).name
        placeholder.mkdir(mode=0o755, exist_ok=True)
        ordinary_directory(placeholder)
    if view.resolve() != view:
        raise ValueError("Skill view must not be a symlink")
    view.mkdir(mode=0o755, parents=True, exist_ok=True)
    ordinary_directory(view)
    for parent in [view.parent.parent, view.parent, view]:
        os.chmod(parent, 0o755)
    for source, target, readonly in mounts:
        mount(source, target, flags=0x1000)
        if readonly:
            # A rootless user namespace locks inherited mount restrictions.
            # A readonly remount must retain them instead of clearing them.
            inherited = os.statvfs(target).f_flag
            flags = 0x1021  # MS_BIND | MS_REMOUNT | MS_RDONLY
            for stat_flag, mount_flag in [
                (2, 2),
                (4, 4),
                (8, 8),  # NOSUID, NODEV, NOEXEC
                (1024, 1024),
                (2048, 2048),
                (4096, 0x200000),  # atime policy
            ]:
                if inherited & stat_flag:
                    flags |= mount_flag
            mount(None, target, flags=flags)
