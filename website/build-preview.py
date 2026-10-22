"""Compatibility entry point: regenerate all localized pages and the offline preview."""

import runpy
from pathlib import Path

runpy.run_path(str(Path(__file__).with_name("build-site.py")), run_name="__main__")
