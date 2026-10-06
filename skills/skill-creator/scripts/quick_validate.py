#!/usr/bin/env python3
"""Validate a Pot skill bundle; adapted from Codex skill-creator's quick validator."""

import re
import stat
import sys
from pathlib import Path
from urllib.parse import unquote, urlsplit

import yaml

ALLOWED_FIELDS = {
    "name",
    "description",
    "disable-model-invocation",
    "license",
    "metadata",
    "allowed-tools",
}


def validate_skill(skill_path):
    """Return (valid, message). No bundled code is executed."""
    try:
        root = Path(skill_path).absolute()
        if root.is_symlink() or not root.is_dir():
            return False, "Skill must be an ordinary directory"
        skill_md = root / "SKILL.md"
        if not skill_md.is_file():
            return False, "SKILL.md not found"
        for entry in [root, *root.rglob("*")]:
            mode = entry.lstat().st_mode
            if not (stat.S_ISREG(mode) or stat.S_ISDIR(mode)):
                return False, f"Unsupported file type or symlink: {entry}"
        if skill_md.stat().st_size > 1024 * 1024:
            return False, "SKILL.md exceeds 1 MiB"
        content = skill_md.read_text(encoding="utf-8-sig")
        match = re.match(r"\A---\n(.*?)\n---(?:\n|\Z)", content, re.DOTALL)
        if not match:
            return False, "Invalid or missing YAML frontmatter"
        try:
            header = yaml.safe_load(match.group(1))
        except yaml.YAMLError as error:
            return False, f"Invalid YAML: {error}"
        if not isinstance(header, dict):
            return False, "Frontmatter must be a YAML dictionary"
        if set(header) - ALLOWED_FIELDS:
            return False, "Unsupported frontmatter fields"
        name = header.get("name")
        if (
            not isinstance(name, str)
            or not re.fullmatch(r"[a-z0-9]+(?:-[a-z0-9]+)*", name)
            or len(name) > 64
        ):
            return False, "Name must be 1–64 lowercase letters, digits or single hyphens"
        if root.name != name:
            return False, "Directory name must match skill name"
        description = header.get("description")
        if not isinstance(description, str) or not description.strip() or len(description) > 1024:
            return False, "Description must be nonempty text of at most 1024 characters"
        if not isinstance(header.get("disable-model-invocation", False), bool):
            return False, "disable-model-invocation must be a boolean"
        if description.lstrip().startswith("[TODO:"):
            return False, "Description contains an unfinished TODO placeholder"
        fence_marker = None
        fence_length = 0
        for line in content[match.end() :].splitlines():
            fence = re.match(r"^[ \t]*(?:(?:[-+*]|\d+[.)])[ \t]+)?(`{3,}|~{3,})(.*)$", line)
            if fence:
                marker = fence.group(1)
                if fence_marker is None:
                    fence_marker, fence_length = marker[0], len(marker)
                elif (
                    marker[0] == fence_marker
                    and len(marker) >= fence_length
                    and not fence.group(2).strip()
                ):
                    fence_marker = None
                continue
            if fence_marker is not None:
                continue
            if re.fullmatch(r"[ ]{0,3}\[TODO:[^\n]*\][ \t]*", line):
                return False, "Instructions contain an unfinished TODO placeholder"
            for target in re.findall(r"\[[^\]]*\]\(([^\s)]+)(?:\s+[^)]*)?\)", line):
                parsed = urlsplit(target.strip("<>"))
                if parsed.scheme or parsed.netloc or not parsed.path:
                    continue
                resource = (root / unquote(parsed.path)).resolve()
                if not resource.is_relative_to(root.resolve()):
                    return False, f"Resource link escapes the bundle: {target}"
                if not resource.exists():
                    return False, f"Resource link not found: {target}"
        return True, "Skill is valid (format and resources; behavior not tested)"
    except (OSError, UnicodeError, ValueError) as error:
        return False, str(error)


if __name__ == "__main__":
    if len(sys.argv) != 2:
        print("Usage: python3 quick_validate.py <skill_directory>", file=sys.stderr)
        raise SystemExit(1)
    valid, message = validate_skill(sys.argv[1])
    print(message)
    raise SystemExit(0 if valid else 1)
