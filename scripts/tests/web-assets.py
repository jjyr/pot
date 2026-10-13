#!/usr/bin/env python3
"""Cache behavior tests using tiny files and the real Brotli CLI; no WASM compilation."""

from pathlib import Path
import importlib.machinery
import importlib.util
import json
import os
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[2]


def load(name):
    loader = importlib.machinery.SourceFileLoader(
        name.replace("-", "_"), str(ROOT / "scripts" / name)
    )
    spec = importlib.util.spec_from_loader(loader.name, loader)
    module = importlib.util.module_from_spec(spec)
    loader.exec_module(module)
    return module


package = load("package-web-assets")
web_ui = load("build-web-ui")


def fixture(dist):
    for name in package.NAMES + ["desktop-assets/core/rfb.js"]:
        path = dist / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes((name + " test data").encode())


class WebAssets(unittest.TestCase):
    def test_compression_reuses_valid_files_and_repairs_changed_or_corrupt_files(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            dist, output = root / "dist", root / "compressed"
            fixture(dist)
            package.compress(dist, output)
            times = {p: p.stat().st_mtime_ns for p in output.rglob("*.br")}
            package.compress(dist, output)
            self.assertEqual(times, {p: p.stat().st_mtime_ns for p in times})
            (dist / "auth.js").write_bytes(b"new auth source")
            (output / "index.html.br").write_bytes(b"corrupt")
            package.compress(dist, output)
            manifest = json.loads((output / "manifest.json").read_text())
            self.assertEqual(
                manifest["assets"]["auth.js"]["raw_sha256"], package.digest(b"new auth source")
            )
            for name in ["auth.js", "index.html"]:
                self.assertNotEqual(
                    times[output / (name + ".br")], (output / (name + ".br")).stat().st_mtime_ns
                )
            self.assertEqual(
                times[output / "font.ttf.br"], (output / "font.ttf.br").stat().st_mtime_ns
            )
            # Compressor settings/version changes must invalidate even unchanged input.
            manifest["profile"]["version"] = "old version"
            (output / "manifest.json").write_text(json.dumps(manifest))
            with patch.object(
                package.subprocess, "check_output", wraps=package.subprocess.check_output
            ) as calls:
                package.compress(dist, output)
                self.assertTrue(
                    any("--quality=11" in call.args[0] for call in calls.call_args_list)
                )

    def test_raw_build_skips_only_when_sources_and_payloads_match(self):
        with tempfile.TemporaryDirectory() as directory:
            cache = Path(directory)
            with (
                patch("sys.argv", ["build-web-ui", "--cache-dir", directory]),
                patch.object(web_ui, "sources", return_value={"source": "v1"}) as sources,
                patch.object(
                    web_ui, "build", side_effect=lambda cache, dist: fixture(dist)
                ) as build,
                patch.dict(web_ui.helpers, {"compress": lambda *_: None}),
            ):
                web_ui.main()
                web_ui.main()
                self.assertEqual(build.call_count, 1)
                (cache / "dist/auth.js").unlink()
                web_ui.main()
                self.assertEqual(build.call_count, 2)
                sources.return_value = {"source": "v2"}
                web_ui.main()
                self.assertEqual(build.call_count, 3)

    def test_missing_brotli_has_actionable_error(self):
        with patch.object(package.shutil, "which", return_value=None):
            with self.assertRaisesRegex(SystemExit, "--no-default-features"):
                package.compress(Path("unused"), Path("unused"))

    def test_nested_cargo_has_isolated_target_and_no_native_flags(self):
        with patch.dict(
            os.environ,
            {
                "CARGO_TARGET_DIR": "/outer",
                "RUSTC": "/native-rustc",
                "CARGO_ENCODED_RUSTFLAGS": "native",
                "MAKEFLAGS": "jobserver",
            },
        ):
            env = web_ui.child_environment(Path("/web-cache"))
        self.assertEqual(env["CARGO_TARGET_DIR"], "/web-cache/cargo")
        for name in ["RUSTC", "CARGO_ENCODED_RUSTFLAGS", "MAKEFLAGS"]:
            self.assertNotIn(name, env)


if __name__ == "__main__":
    unittest.main()
