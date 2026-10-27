"""Synthetic configuration + fake client tests. No native login or real config."""

import contextlib
import importlib.util
import io
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest import mock

spec = importlib.util.spec_from_file_location(
    "setup", Path(__file__).parent / "plugin/scripts/pot-mcp.py"
)
setup = importlib.util.module_from_spec(spec)
spec.loader.exec_module(setup)
URL = "https://pot.example.invalid/custom-mcp"


class ConfigTests(unittest.TestCase):
    def test_conflicts_and_exact_endpoint_matching(self):
        cases = [
            ([], "add"),
            ([("pot", {"url": URL})], "keep"),
            ([("pot", {"url": URL, "enabled": False})], "conflict"),
            ([("other", {"url": URL})], "conflict"),
            ([("pot", {"url": URL + "/"})], "conflict"),
            ([("pot", {"command": "old-host"})], "conflict"),
            ([("pot", {"url": URL}), ("pot", {"url": URL})], "conflict"),
            ([("unrelated", {"url": URL + "/"})], "add"),
        ]
        for entries, want in cases:
            self.assertEqual(setup.decision(entries, "pot", URL), want)

    def test_url_rejects_secret_bearing_forms_without_echo(self):
        for url in [
            "https://user:secret@example.invalid",
            URL + "?token=secret",
            URL + "#secret",
            "file:///tmp/private",
            "https://pot.invalid:bad",
        ]:
            with self.assertRaisesRegex(ValueError, "^invalid_endpoint$"):
                setup.endpoint(url)

    def test_real_subprocess_fake_cli_add_preserves_other_config_and_no_home_change(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            home = root / "codex"
            home.mkdir()
            workspace = root / "project"
            workspace.mkdir()
            config = home / "config.toml"
            original = '[mcp_servers.other]\nurl="https://other.invalid"\nenabled_tools=["safe"]\nhttp_headers={Authorization="synthetic-private"}\n'
            config.write_text(original)
            fake = root / "codex-cli"
            fake.write_text("""#!/usr/bin/env python3
import json,os,sys,tomllib
from pathlib import Path
p=Path(os.environ["CODEX_HOME"])/"config.toml"
a=sys.argv[1:]
if a==["mcp","add","--help"]: sys.exit(0)
if a==["mcp","list","--json"]:
 d=tomllib.loads(p.read_text()).get("mcp_servers",{})
 print(json.dumps([{"name":k,"transport":{"type":"streamable_http","url":v.get("url"),"http_headers":v.get("http_headers")},"enabled":v.get("enabled",True)} for k,v in d.items()]))
elif a[:2]==["mcp","add"]:
 with p.open("a") as f: f.write("\\n[mcp_servers."+a[2]+"]\\nurl="+json.dumps(a[4])+"\\n")
 print("synthetic-private-must-not-leak")
else: sys.exit(3)
""")
            fake.chmod(0o700)
            before_home = os.environ.get("HOME")
            with (
                mock.patch.dict(os.environ, {"CODEX_HOME": str(home)}),
                contextlib.redirect_stdout(io.StringIO()) as out,
            ):
                self.assertEqual(
                    setup.configure(str(fake), home, workspace, "pot", URL),
                    "new_connection_requires_apply",
                )
                self.assertEqual(config.read_text(), original)
                self.assertEqual(
                    setup.configure(str(fake), home, workspace, "pot", URL, True),
                    "connection_added_authenticate_in_client",
                )
                after = config.read_text()
                self.assertTrue(after.startswith(original))
                self.assertEqual(
                    setup.configure(str(fake), home, workspace, "pot", URL, True),
                    "existing_connection_preserved",
                )
                self.assertEqual(config.read_text(), after)
            self.assertEqual(os.environ.get("HOME"), before_home)
            self.assertEqual(out.getvalue(), "")

    def test_cross_scope_and_effective_plugin_conflict_stops_before_write(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            home = root / "codex"
            home.mkdir()
            workspace = root / "project"
            (workspace / ".codex").mkdir(parents=True)
            (home / "config.toml").write_text('[mcp_servers.pot]\nurl="' + URL + '"\n')
            (workspace / ".codex/config.toml").write_text('[mcp_servers.pot]\nurl="' + URL + '"\n')
            with (
                mock.patch.object(setup, "effective_entries", return_value=[("pot", {"url": URL})]),
                mock.patch.object(setup, "client", side_effect=AssertionError("write attempted")),
            ):
                with self.assertRaisesRegex(ValueError, "conflict"):
                    setup.configure("fake", home, workspace, "pot", URL, True)

    def test_cli_failure_does_not_echo_private_diagnostics(self):
        with mock.patch.object(
            setup.subprocess,
            "run",
            return_value=subprocess.CompletedProcess([], 1, "token-secret", "header-secret"),
        ):
            with self.assertRaisesRegex(ValueError, "^client_command_failed$"):
                setup.client("fake", "mcp", "list", "--json")


if __name__ == "__main__":
    unittest.main()
