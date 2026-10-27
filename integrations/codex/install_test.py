"""Isolated installation contracts; never reads or changes the real Codex home."""

import contextlib
import importlib.util
import io
import json
import os
import subprocess
from pathlib import Path
import tempfile
import unittest
from unittest import mock

spec = importlib.util.spec_from_file_location(
    "pot_codex_install", Path(__file__).with_name("install.py")
)
installer = importlib.util.module_from_spec(spec)
spec.loader.exec_module(installer)


class InstallTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        root = Path(self.temp.name).resolve()
        self.home = root / "home"
        self.binary = root / "agent"
        self.binary.write_bytes(b"fixture executable")

    def install(self):
        with contextlib.redirect_stdout(io.StringIO()):
            return installer.install(self.home, self.binary, build=False)

    def test_repeat_preserves_owner_data_and_other_plugins(self):
        catalog = self.home / ".agents/plugins/marketplace.json"
        catalog.parent.mkdir(parents=True)
        catalog.write_text(
            json.dumps(
                {
                    "name": "personal",
                    "plugins": [{"name": "other", "source": {"source": "local", "path": "/other"}}],
                }
            )
        )
        destination = self.install()
        pair = self.home / ".config/pot-codex/local-pair.json"
        pair.parent.mkdir(parents=True, exist_ok=True)
        pair.write_text('{"token":"private-owner-value"}')
        before = catalog.read_text()
        self.install()
        self.assertEqual(before, catalog.read_text())
        self.assertIn("private-owner-value", pair.read_text())
        self.assertFalse((self.home / ".config/pot-codex/bridge.json").exists())
        manifest = json.loads((destination / ".codex-plugin/plugin.json").read_text())
        self.assertEqual(manifest["name"], "pot")
        self.assertNotIn("mcpServers", manifest)
        self.assertFalse((destination / ".mcp.json").exists())
        self.assertFalse((destination / "bin").exists())
        self.assertEqual(manifest["skills"], "./skills/")
        setup = manifest["extensions"]["com.openai"]["onboardingSkill"]
        self.assertTrue((destination / setup).is_file())
        self.assertEqual(
            (destination / setup).read_bytes(), (installer.SOURCE / setup).read_bytes()
        )

    def test_local_edits_are_retained(self):
        destination = self.install()
        file = destination / ".codex-plugin/plugin.json"
        file.write_text("owner changes")
        with self.assertRaisesRegex(ValueError, "local change"):
            self.install()
        self.assertEqual(file.read_text(), "owner changes")

    def test_helper_needs_no_go_or_native_cli(self):
        with (
            mock.patch.object(installer.shutil, "which", return_value=None),
            mock.patch.object(
                installer.subprocess, "run", side_effect=AssertionError("helper spawned a process")
            ),
        ):
            self.assertTrue(self.install().is_dir())

    def test_receiver_is_explicit_and_never_launched(self):
        with mock.patch.object(
            installer.subprocess, "run", side_effect=AssertionError("receiver launched")
        ):
            installer.install(self.home, self.binary, build=False, receiver=True)
        receiver = self.home / ".local/bin/pot-codex-agent"
        self.assertEqual(receiver.read_bytes(), self.binary.read_bytes())
        with mock.patch.object(installer.shutil, "which", return_value=None):
            with self.assertRaisesRegex(ValueError, "Go is required"):
                installer.install(self.home, self.binary, build=True, receiver=True)

    def test_upgrade_removes_only_receipted_plugin_transport(self):
        destination = self.install()
        old = destination / ".mcp.json"
        old.write_text('{"mcpServers":{"pot":{"command":"old-host"}}}')
        receipt = destination / ".pot-install.json"
        previous = installer.load(receipt)
        previous[".mcp.json"] = installer.digest(old)
        receipt.write_text(json.dumps(previous))
        history = self.home / ".config/pot-codex/agent/history"
        history.parent.mkdir(parents=True)
        history.write_text("retained")
        independent = self.home / ".codex/config.toml"
        independent.write_text('[mcp_servers.owner]\nurl="https://owner.invalid/mcp"\n')
        self.install()
        self.assertFalse(old.exists())
        self.assertEqual(history.read_text(), "retained")
        self.assertIn("owner.invalid", independent.read_text())

    def test_activation_preserves_home_and_uses_explicit_native_config_root(self):
        self.install()
        captured = []
        native = self.home / "explicit-codex"
        original_home = os.environ.get("HOME")

        def run(args, **kwargs):
            captured.append({key: kwargs["env"].get(key) for key in ("HOME", "CODEX_HOME")})
            output = '{"installed":[{"pluginId":"pot@personal","enabled":true}]}'
            return installer.subprocess.CompletedProcess(args, 0, output)

        with mock.patch.object(installer.subprocess, "run", side_effect=run):
            self.assertTrue(installer.activate(self.home, native, "fake-codex"))
        self.assertTrue(captured)
        for env in captured:
            self.assertEqual(env.get("HOME"), original_home)
            self.assertEqual(env["CODEX_HOME"], str(native))

    @unittest.skipUnless(
        os.environ.get("POT_CODEX_CLI_TEST"), "Optional real Codex CLI discovery test"
    )
    def test_official_cli_discovers_installs_and_repeats(self):
        self.install()
        for _ in range(2):
            self.assertTrue(
                installer.activate(
                    self.home, self.home / ".codex", os.environ["POT_CODEX_CLI_TEST"]
                )
            )
        version = installer.load(installer.SOURCE / ".codex-plugin/plugin.json")["version"]
        cache = self.home / ".codex/plugins/cache/personal/pot" / version
        self.assertTrue((cache / ".codex-plugin/plugin.json").is_file())
        self.assertFalse((cache / "local-pair.json").exists())
        self.assertFalse((cache / "bridge.json").exists())

    def test_binary_and_config_parent_symlinks_are_rejected_before_writes(self):
        for relative in [".codex/plugins/pot/bin", ".agents/plugins"]:
            with self.subTest(relative=relative):
                with tempfile.TemporaryDirectory() as directory:
                    self.home = Path(directory) / "home"
                    outside = Path(directory) / "outside"
                    outside.mkdir()
                    link = self.home / relative
                    link.parent.mkdir(parents=True)
                    link.symlink_to(outside, target_is_directory=True)
                    with self.assertRaisesRegex(ValueError, "symlink"):
                        self.install()
                    self.assertEqual(list(outside.iterdir()), [])
                    self.assertFalse((self.home / ".agents/plugins/marketplace.json").exists())

    def test_unmanaged_package_and_symlinks_are_not_overwritten(self):
        destination = self.home / ".codex/plugins/pot"
        (destination / ".codex-plugin").mkdir(parents=True)
        (destination / ".codex-plugin/plugin.json").write_text("unmanaged")
        with self.assertRaisesRegex(ValueError, "local change"):
            self.install()
        (destination / ".codex-plugin/plugin.json").unlink()
        (destination / ".codex-plugin/plugin.json").symlink_to(self.binary)
        with self.assertRaisesRegex(ValueError, "symlink"):
            self.install()


setup_spec = importlib.util.spec_from_file_location(
    "setup", Path(__file__).parent / "plugin/scripts/pot-mcp.py"
)
setup = importlib.util.module_from_spec(setup_spec)
setup_spec.loader.exec_module(setup)
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
