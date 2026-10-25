#!/usr/bin/env python3
"""Deterministic hostile-publication fixtures, using real Ed25519 and Go metadata."""

import base64
import copy
import hashlib
import io
import json
import os
from pathlib import Path
import subprocess
import sys
import tarfile
import tempfile
import unittest
from unittest import mock
import urllib.error
import zipfile
import plistlib
import platform
import struct
import threading

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "scripts"))
import public_release_verification as verify  # noqa: E402
from release_contract import (  # noqa: E402
    checksum_name,
    distribution_names,
    macos_release_notes,
    update_artifacts,
)

VERSION = "1.2.3"
PUBLIC = base64.b64encode(
    bytes.fromhex("d75a980182b10ab7d54bfed3c964073a0ee172f3daa62325af021a68f707511a")
).decode()
PRIVATE = (
    "-----BEGIN PRIVATE KEY-----\n"
    + base64.b64encode(
        bytes.fromhex(
            "302e020100300506032b6570042204209d61b19deffd5a60ba844af492ec2cc44449c5697b326919703bac031cae7f60"
        )
    ).decode()
    + "\n-----END PRIVATE KEY-----\n"
)
STORAGE = "fixture-" + "a" * 64


def digest(data):
    return "sha256:" + hashlib.sha256(data).hexdigest()


def tar(files):
    data = io.BytesIO()
    with tarfile.open(fileobj=data, mode="w:gz") as packed:
        for name, content in files.items():
            info = tarfile.TarInfo(name)
            info.size, info.mode = len(content), 0o755
            packed.addfile(info, io.BytesIO(content))
    return data.getvalue()


def resources(root):
    files = {root + name: b"fixture" for name in verify.REQUIRED_RESOURCE_FILES}
    files.update(
        {root + "plugins/example/plugin.json": b"{}", root + "skills/example/SKILL.md": b"fixture"}
    )
    return files


class FakeNetwork(verify.Network):
    def __init__(self, fixture):
        super().__init__()
        self.fixture = fixture

    def open(self, url, headers=None):
        if url not in self.fixture:
            raise verify.Incomplete("fixture publication not available")
        data, response_headers = self.fixture[url]
        stream = io.BytesIO(data)
        stream.headers = response_headers
        return stream


class PublicRelease(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.temporary = tempfile.TemporaryDirectory(prefix="pot-public-fixtures-")
        root = Path(cls.temporary.name)
        cls.source_root = root
        (root / "internal/buildinfo").mkdir(parents=True)
        (root / "go.mod").write_text("module github.com/jjyr/pot\n\ngo 1.24\n")
        (root / "internal/buildinfo/buildinfo.go").write_text(
            "package buildinfo\nvar Version, Revision, UpdatePublicKey, UpdateSchemaDigest string\n"
        )
        (root / "main.go").write_text(
            'package main\nimport ("fmt"; "github.com/jjyr/pot/internal/buildinfo")\nfunc main(){fmt.Println(buildinfo.Version,buildinfo.Revision,buildinfo.UpdatePublicKey,buildinfo.UpdateSchemaDigest)}\n'
        )
        (root / "cgo_darwin.go").write_text(
            'package main\n/* static int fixture_value = 1; */\nimport "C"\n'
            'func init() { if C.fixture_value != 1 { panic("fixture") } }\n'
        )
        subprocess.run(["git", "init", "--quiet", "--initial-branch=fixture"], cwd=root, check=True)
        subprocess.run(["git", "add", "."], cwd=root, check=True)
        subprocess.run(
            [
                "git",
                "-c",
                "user.name=jjy",
                "-c",
                "user.email=jjyruby@gmail.com",
                "commit",
                "--quiet",
                "-m",
                "Fixture",
            ],
            cwd=root,
            check=True,
            env=dict(
                os.environ,
                GIT_AUTHOR_DATE="2000-01-01T00:00:00Z",
                GIT_COMMITTER_DATE="2000-01-01T00:00:00Z",
            ),
        )
        cls.source = subprocess.check_output(
            ["git", "rev-parse", "HEAD"], cwd=root, text=True
        ).strip()
        cls.binaries = {}
        flags = "-s -w " + " ".join(
            "-X=github.com/jjyr/pot/internal/buildinfo." + k + "=" + v
            for k, v in {
                "Version": VERSION,
                "Revision": cls.source,
                "UpdatePublicKey": PUBLIC,
                "UpdateSchemaDigest": "a" * 64,
            }.items()
        )
        cls.linker_flags = flags
        for platform in ["linux-amd64", "linux-arm64", "darwin-arm64"]:
            system, architecture = platform.split("-")
            destination = root / platform
            # Output outside the source tree, so every build records vcs.modified=false.
            destination = root.parent / (root.name + "-" + platform)
            subprocess.run(
                ["go", "build", "-trimpath", "-ldflags=" + flags, "-o", str(destination), "."],
                cwd=root,
                check=True,
                env=dict(os.environ, GOOS=system, GOARCH=architecture, CGO_ENABLED="0"),
            )
            cls.binaries[platform] = destination.read_bytes()
            destination.unlink()

    @classmethod
    def tearDownClass(cls):
        cls.temporary.cleanup()

    def setUp(self):
        self.output = tempfile.TemporaryDirectory()
        self.addCleanup(self.output.cleanup)
        self.fixture = {}
        self.assets = {}
        for kind, platform, name in update_artifacts(VERSION):
            root = "Pot.app/Contents/" if kind == "app" else f"pot-{platform}/"
            files = resources(root + "Resources/")
            files[root + "Resources/update.json"] = json.dumps(
                {"version": VERSION, "kind": kind, "platform": platform, "storage_format": STORAGE}
            ).encode()
            files[root + ("MacOS/pot-app" if kind == "app" else "bin/pot")] = self.binaries[
                platform
            ]
            if kind == "app":
                files[root + "Info.plist"] = plistlib.dumps(
                    {"CFBundleShortVersionString": VERSION, "CFBundleVersion": VERSION}
                )
                stream = io.BytesIO()
                with zipfile.ZipFile(stream, "w") as packed:
                    packed.writestr("__MACOSX/Pot.app/._Contents", b"AppleDouble fixture")
                    for filename, content in files.items():
                        packed.writestr(filename, content)
                self.assets[name] = stream.getvalue()
            else:
                self.assets[name] = tar(files)
        control = tar(
            {
                "./control": f"Package: pot-desktop\nVersion: {VERSION}\nArchitecture: amd64\n".encode()
            }
        )
        files = resources("./opt/pot-desktop/Resources/")
        for name in ["pot", "pot-app"]:
            files["./opt/pot-desktop/bin/" + name] = self.binaries["linux-amd64"]
        deb = b"!<arch>\n"
        for name, content in [
            ("debian-binary", b"2.0\n"),
            ("control.tar.gz", control),
            ("data.tar.gz", tar(files)),
        ]:
            header = (
                f"{name + '/':<16}{0:<12}{0:<6}{0:<6}{'100644':<8}{len(content):<10}`\n".encode()
            )
            deb += header + content + (b"\n" if len(content) % 2 else b"")
        self.assets[f"pot-desktop_{VERSION}_linux_amd64.deb"] = deb
        payload = {"version": VERSION, "storage_format": STORAGE, "artifacts": []}
        for kind, platform, name in update_artifacts(VERSION):
            payload["artifacts"].append(
                {
                    "kind": kind,
                    "platform": platform,
                    "url": f"https://github.com/jjyr/pot/releases/download/v{VERSION}/{name}",
                    "size": len(self.assets[name]),
                    "sha256": digest(self.assets[name])[7:],
                }
            )
        data = json.dumps(payload).encode()
        signature = (
            subprocess.check_output(
                ["go", "run", "./scripts/sign-release", "sign"],
                cwd=ROOT,
                input=data,
                env=dict(os.environ, POT_UPDATE_PUBLIC_KEY=PUBLIC, POT_UPDATE_PRIVATE_KEY=PRIVATE),
            )
            .strip()
            .decode()
        )
        self.assets["update.json"] = json.dumps(
            {"payload": base64.b64encode(data).decode(), "signature": signature}
        ).encode()
        self.assets["MACOS-RELEASE.txt"] = macos_release_notes(
            VERSION, self.source, "unsigned"
        ).encode()
        for channel in verify.CHANNELS:
            names = distribution_names(VERSION, channel)
            for name in names:
                self.assets[name + ".sha256"] = (
                    f"{digest(self.assets[name])[7:]}  {name}\n".encode()
                )
            if channel == "macos":
                names += ["update.json", "MACOS-RELEASE.txt"]
            self.assets[checksum_name(channel)] = "".join(
                f"{digest(self.assets[n])[7:]}  {n}\n" for n in names
            ).encode()
        self.release = {
            "id": 123,
            "tag_name": "v" + VERSION,
            "target_commitish": self.source,
            "draft": False,
            "prerelease": False,
            "assets": [],
        }
        self.refresh_assets()
        self.put(
            f"https://api.github.com/repos/jjyr/pot/commits/refs/tags/v{VERSION}",
            {"sha": self.source},
        )
        self.registry()
        self.put("https://api.github.com/repos/jjyr/pot/commits/HEAD", {"sha": "c" * 40})
        for path, template, archive in [
            ("Casks/pot.rb", "pot.rb.in", f"Pot-{VERSION}-arm64.zip"),
            ("Formula/pot-cli.rb", "pot-cli.rb.in", f"pot-{VERSION}-darwin-arm64.tar.gz"),
        ]:
            text = (
                (ROOT / "packaging/homebrew" / template)
                .read_text()
                .replace("@VERSION@", VERSION)
                .replace("@SHA256@", digest(self.assets[archive])[7:])
            )
            self.put(
                "https://raw.githubusercontent.com/jjyr/pot/" + "c" * 40 + "/" + path, text.encode()
            )

    def put(self, url, data, headers=None):
        self.fixture[url] = (
            json.dumps(data).encode() if isinstance(data, dict) else data,
            headers or {},
        )

    def refresh_assets(self):
        self.release["assets"] = []
        for name, data in self.assets.items():
            url = f"https://github.com/jjyr/pot/releases/download/v{VERSION}/{name}"
            self.put(url, data)
            self.release["assets"].append(
                {
                    "name": name,
                    "browser_download_url": url,
                    "size": len(data),
                    "digest": digest(data),
                }
            )
        self.put(f"https://api.github.com/repos/jjyr/pot/releases/tags/v{VERSION}", self.release)

    def registry(self, architectures=("amd64", "arm64"), source=None):
        base = "https://ghcr.io/v2/jjyr/pot/"
        self.put(
            "https://ghcr.io/token?service=ghcr.io&scope=repository:jjyr/pot:pull",
            {"token": "anonymous-test-token"},
        )
        descriptors = []
        for architecture in architectures:
            layer = b"image-layer-" + architecture.encode()
            self.put(base + "blobs/" + digest(layer), layer)
            config = json.dumps(
                {
                    "os": "linux",
                    "architecture": architecture,
                    "config": {
                        "Labels": {
                            "org.opencontainers.image.source": "https://github.com/jjyr/pot",
                            "org.opencontainers.image.version": VERSION,
                            "org.opencontainers.image.revision": source or self.source,
                        }
                    },
                }
            ).encode()
            self.put(base + "blobs/" + digest(config), config)
            manifest = json.dumps(
                {
                    "config": {"digest": digest(config), "size": len(config)},
                    "layers": [{"digest": digest(layer), "size": len(layer)}],
                }
            ).encode()
            self.put(base + "manifests/" + digest(manifest), manifest)
            descriptors.append(
                {
                    "digest": digest(manifest),
                    "size": len(manifest),
                    "platform": {"os": "linux", "architecture": architecture},
                }
            )
        index = json.dumps({"manifests": descriptors}).encode()
        self.put(base + "manifests/" + VERSION, index, {"Docker-Content-Digest": digest(index)})

    def run_verifier(self, *options):
        with mock.patch.object(verify, "Network", lambda: FakeNetwork(self.fixture)):
            code = verify.main(
                [
                    VERSION,
                    self.source,
                    "--public-key",
                    PUBLIC,
                    "--macos-mode",
                    "unsigned",
                    "--output",
                    self.output.name,
                    "--markdown",
                    *options,
                ]
            )
        return code, json.loads((Path(self.output.name) / "report.json").read_text())

    def test_complete_publication_and_installation(self):
        code, report = self.run_verifier()
        self.assertEqual(code, 0, report)
        self.assertEqual(report["status"], "passed")
        self.assertEqual(set(report["downloads"]), verify.expected_assets(VERSION))
        self.assertIn("native", " ".join(report["limitations"]).lower())
        self.assertEqual(
            len(
                next(c for c in report["checks"] if c["name"] == "ghcr")["evidence"][
                    "anonymous_pulled_layers"
                ]
            ),
            2,
        )

    def test_partial_channels_and_draft_are_incomplete(self):
        for mutate in [
            lambda: self.assets.pop(f"pot-desktop_{VERSION}_linux_amd64.deb"),
            lambda: self.release.update(draft=True),
            lambda: self.release.update(prerelease=True),
        ]:
            with self.subTest(mutate=mutate):
                original_assets, original_release = (
                    copy.deepcopy(self.assets),
                    copy.deepcopy(self.release),
                )
                mutate()
                self.refresh_assets()
                self.assertEqual(self.run_verifier()[0], 2)
                self.assets, self.release = original_assets, original_release

    def test_tag_and_source_conflicts_are_failures(self):
        for field, value in [("tag_name", "v1.2.4"), ("target_commitish", "b" * 40)]:
            original = self.release[field]
            self.release[field] = value
            self.refresh_assets()
            self.assertEqual(self.run_verifier()[0], 1)
            self.release[field] = original
        self.refresh_assets()
        self.put(
            f"https://api.github.com/repos/jjyr/pot/commits/refs/tags/v{VERSION}", {"sha": "b" * 40}
        )
        self.assertEqual(self.run_verifier()[0], 1)

    def test_download_conflict_and_truncation(self):
        asset = self.release["assets"][0]
        for data in [b"x" * asset["size"], b"truncated"]:
            self.put(asset["browser_download_url"], data)
            code, report = self.run_verifier()
            self.assertEqual(code, 1, report)

    def test_unexpected_and_duplicate_assets(self):
        self.release["assets"].append(self.release["assets"][0])
        self.put(f"https://api.github.com/repos/jjyr/pot/releases/tags/v{VERSION}", self.release)
        self.assertEqual(self.run_verifier()[0], 1)
        self.assets["surprise"] = b"extra"
        self.refresh_assets()
        self.assertEqual(self.run_verifier()[0], 1)

    def test_forged_signature_and_wrong_trusted_key(self):
        self.assertEqual(
            self.run_verifier("--public-key", base64.b64encode(b"x" * 32).decode())[0], 1
        )
        feed = json.loads(self.assets["update.json"])
        feed["signature"] = base64.b64encode(b"x" * 64).decode()
        self.assets["update.json"] = json.dumps(feed).encode()
        # Update transport checksums to make sure the cryptographic check is reached.
        self.assets["SHA256SUMS-macos"] = "".join(
            f"{digest(self.assets[n])[7:]}  {n}\n"
            for n in distribution_names(VERSION, "macos") + ["update.json", "MACOS-RELEASE.txt"]
        ).encode()
        self.refresh_assets()
        code, report = self.run_verifier()
        self.assertEqual(code, 1)
        self.assertIn(
            "signature", next(c for c in report["checks"] if c["status"] == "failed")["detail"]
        )

    def test_oci_missing_architecture_bad_labels_and_metadata_mode(self):
        self.registry(("amd64",))
        self.assertEqual(self.run_verifier()[0], 1)
        self.registry(source="b" * 40)
        self.assertEqual(self.run_verifier()[0], 1)
        self.registry()
        self.assertEqual(self.run_verifier("--oci", "metadata")[0], 2)

    def test_homebrew_pending_is_separate_from_publication(self):
        for path in ["Casks/pot.rb", "Formula/pot-cli.rb"]:
            url = "https://raw.githubusercontent.com/jjyr/pot/" + "c" * 40 + "/" + path
            data = self.fixture[url][0].replace(b"1.2.3", b"1.2.2")
            self.put(url, data)
        self.assertEqual(self.run_verifier()[0], 2)
        code, report = self.run_verifier("--phase", "publication")
        self.assertEqual(code, 0, report)
        brew = next(c for c in report["checks"] if c["name"] == "homebrew")
        self.assertTrue(all(c["status"] == "pending_update" for c in brew["evidence"]))

    def test_valid_signature_cannot_rebind_artifact_bytes_or_url(self):
        feed = json.loads(base64.b64decode(json.loads(self.assets["update.json"])["payload"]))
        downloads = {
            name: {"size": len(data), "sha256": digest(data)[7:]}
            for name, data in self.assets.items()
        }
        for field, value in [
            ("url", "https://example.org/untrusted.tar.gz"),
            ("sha256", "b" * 64),
            ("size", 1),
        ]:
            wrong = copy.deepcopy(feed)
            wrong["artifacts"][0][field] = value
            data = json.dumps(wrong).encode()
            signature = (
                subprocess.check_output(
                    ["go", "run", "./scripts/sign-release", "sign"],
                    cwd=ROOT,
                    input=data,
                    env=dict(
                        os.environ, POT_UPDATE_PUBLIC_KEY=PUBLIC, POT_UPDATE_PRIVATE_KEY=PRIVATE
                    ),
                )
                .strip()
                .decode()
            )
            (Path(self.output.name) / "update.json").write_text(
                json.dumps({"payload": base64.b64encode(data).decode(), "signature": signature})
            )
            with self.subTest(field=field), self.assertRaises(ValueError):
                verify.verify_feed(Path(self.output.name), VERSION, PUBLIC, downloads, "jjyr/pot")

    def test_embedded_metadata_is_read_without_running_binaries(self):
        for platform, binary in self.binaries.items():
            verify.verify_binary(binary, VERSION, self.source, PUBLIC, STORAGE, platform)
            for field, expected in [
                ("version", "9.9.9"),
                ("source", "b" * 40),
                ("public_key", "wrong key"),
            ]:
                with self.subTest(platform=platform, field=field), self.assertRaises(ValueError):
                    verify.embedded_go_strings(binary + expected.encode(), {field: expected})
        with self.assertRaises(ValueError):
            verify.verify_binary(
                self.binaries["linux-amd64"], VERSION, self.source, PUBLIC, STORAGE, "linux-arm64"
            )

    @unittest.skipUnless(
        sys.platform == "darwin" and platform.machine() == "arm64",
        "native Apple Silicon CGO/external linker requires macOS",
    )
    def test_native_cgo_external_macho(self):
        with tempfile.TemporaryDirectory() as temporary:
            binary = Path(temporary) / "external-macho"
            subprocess.run(
                [
                    "go",
                    "build",
                    "-trimpath",
                    "-ldflags=" + self.linker_flags + " -linkmode=external",
                    "-o",
                    str(binary),
                    ".",
                ],
                cwd=self.source_root,
                check=True,
                env=dict(os.environ, GOOS="darwin", GOARCH="arm64", CGO_ENABLED="1"),
            )
            data = binary.read_bytes()
            offset, seen = 32, False
            for _ in range(struct.unpack_from("<I", data, 16)[0]):
                kind, size = struct.unpack_from("<II", data, offset)
                seen |= kind == 0x80000034
                offset += size
            self.assertTrue(seen, "native external fixture must exercise chained fixups")
            verify.verify_binary(data, VERSION, self.source, PUBLIC, STORAGE, "darwin-arm64")

    def test_interruption_retains_incomplete_json(self):
        class Interrupted(FakeNetwork):
            def open(self, url, headers=None):
                if url.startswith("https://github.com/"):
                    raise KeyboardInterrupt()
                return super().open(url, headers)

        with (
            mock.patch.object(verify, "Network", lambda: Interrupted(self.fixture)),
            self.assertRaises(KeyboardInterrupt),
        ):
            verify.main(
                [
                    VERSION,
                    self.source,
                    "--public-key",
                    PUBLIC,
                    "--macos-mode",
                    "unsigned",
                    "--output",
                    self.output.name,
                ]
            )
        report = json.loads((Path(self.output.name) / "report.json").read_text())
        self.assertEqual(report["status"], "incomplete")
        self.assertTrue(report["asset_downloads_pending"])
        self.assertEqual(report["checks"][0]["status"], "passed")
        self.assertFalse((Path(self.output.name) / "report.json.tmp").exists())

    def test_wrong_expected_mode_fails(self):
        self.assertEqual(self.run_verifier("--macos-mode", "signed")[0], 1)


def chained_macho(pointer_format=6):
    """Minimal independent dyld layout: two Go headers on one rebase chain."""
    data = bytearray(12288)
    imagebase = 0x100000000
    commands = []
    for index, (name, protection) in enumerate(
        [(b"__TEXT", 5), (b"__DATA", 3), (b"__LINKEDIT", 1)]
    ):
        commands.append(
            struct.pack(
                "<II16sQQQQIIII",
                0x19,
                72,
                name,
                imagebase + index * 4096,
                4096,
                index * 4096,
                4096,
                protection,
                protection,
                0,
                0,
            )
        )
    commands.append(struct.pack("<IIII", 0x80000034, 16, 8192, 68))
    struct.pack_into("<8I", data, 0, 0xFEEDFACF, 0x100000C, 0, 2, 4, 232, 0, 0)
    data[32:264] = b"".join(commands)
    data[512:517], data[520:560] = b"1.2.3", b"a" * 40
    target = 512 + (imagebase if pointer_format == 2 else 0)
    struct.pack_into(
        "<QQQQ",
        data,
        4096,
        target | (4 << 51),
        5,
        520 + (imagebase if pointer_format == 2 else 0),
        40,
    )
    struct.pack_into("<7I", data, 8192, 0, 28, 68, 68, 0, 1, 0)
    struct.pack_into("<4I", data, 8220, 3, 0, 16, 0)
    struct.pack_into("<IHHQIHH", data, 8236, 24, 4096, pointer_format, 4096, 0, 1, 0)
    return data


class HostileInputs(unittest.TestCase):
    def test_real_format_chained_offsets_and_absolute_rebases(self):
        for pointer_format in [2, 6]:
            data = chained_macho(pointer_format)
            self.assertEqual(
                verify.embedded_go_strings(data, {"version": "1.2.3", "source": "a" * 40}),
                ["version", "source"],
            )
            with self.assertRaises(ValueError):
                verify.embedded_go_strings(data + b"9.9.9", {"version": "9.9.9"})

    def test_malformed_and_bind_chains_are_not_string_evidence(self):
        for change in [
            lambda d: struct.pack_into("<H", d, 8242, 12),  # unsupported format
            lambda d: struct.pack_into("<H", d, 8258, 4093),  # out of page
            lambda d: struct.pack_into("<I", d, 8220, 4),  # invalid segment count
            lambda d: struct.pack_into("<Q", d, 4096, 512 | (0xFFF << 51)),
            lambda d: struct.pack_into("<Q", d, 4096, 512 | (4 << 51) | (1 << 63)),
        ]:
            data = chained_macho()
            change(data)
            with self.assertRaises(ValueError):
                verify.embedded_go_strings(data, {"version": "1.2.3", "source": "a" * 40})

    def test_asset_pool_is_bounded_and_preserves_failure_receipts(self):
        lock, barrier = threading.Lock(), threading.Barrier(3)
        active, peak = 0, 0

        class Transfer:
            def download(self, url, destination, size, digest):
                nonlocal active, peak
                with lock:
                    active += 1
                    peak = max(peak, active)
                barrier.wait(timeout=10)
                with lock:
                    active -= 1
                if destination.name == "asset-2":
                    raise ValueError("fixture byte conflict")
                return {"size": size, "sha256": "a" * 64}

        assets = [
            {"name": f"asset-{i}", "browser_download_url": "https://example.org/", "size": 1}
            for i in range(6)
        ]
        receipts = {}
        with self.assertRaises(ValueError):
            verify.download_assets(
                Transfer(),
                assets,
                Path("unused-fixture"),
                lambda name, result: receipts.update({name: result}),
            )
        self.assertEqual(peak, 3)
        self.assertEqual(len(receipts), 6)
        self.assertIsInstance(receipts["asset-2"], ValueError)
        self.assertEqual(receipts["asset-5"]["size"], 1)

    def test_homebrew_fields_are_active_and_unique(self):
        good = 'version "1.2.3"\nsha256 "' + "a" * 64 + '"\nurl "https://example.org/package"\n'
        self.assertEqual(verify.homebrew_fields(good)["version"], "1.2.3")
        for text in [
            good.replace('version "1.2.3"', '# version "1.2.3"'),
            good + 'version "9.9.9"\n',
            good.replace('sha256 "', '# sha256 "'),
        ]:
            with self.assertRaises(ValueError):
                verify.homebrew_fields(text)

    def test_manifest_traversal_duplicate_and_wrong_hash(self):
        good = "a" * 64
        for text in [
            good + "  ../escape\n",
            good + "  package\n" + good + "  package\n",
            "b" * 64 + "  package\n",
        ]:
            with self.assertRaises(ValueError):
                verify.verify_manifest(text, ["package"], {"package": {"sha256": good}})
        verify.verify_manifest(good + "  package\n", ["package"], {"package": {"sha256": good}})

    def test_archive_traversal_duplicate_and_escaping_links(self):
        for members in [
            [("../escape", None)],
            [("root/file", None), ("root/file", None)],
            [("root/link", "../../outside")],
            [("root/link", "/absolute")],
        ]:
            stream = io.BytesIO()
            with tarfile.open(fileobj=stream, mode="w") as packed:
                for name, link in members:
                    entry = tarfile.TarInfo(name)
                    if link:
                        entry.type, entry.linkname = tarfile.SYMTYPE, link
                    packed.addfile(entry)
            stream.seek(0)
            with tarfile.open(fileobj=stream) as packed, self.assertRaises(ValueError):
                verify.Archive(packed)

    def test_eventual_consistency_retries_only_transient_http(self):
        for status, attempts in [(503, 3), (429, 3), (404, 1), (401, 1), (403, 1)]:
            network = verify.Network()
            error = urllib.error.HTTPError(
                "https://example.org", status, "fixture", {"Retry-After": "0"}, None
            )
            with (
                mock.patch.object(network.opener, "open", side_effect=error) as request,
                mock.patch.object(verify.time, "sleep"),
            ):
                with self.assertRaises((ValueError, verify.Incomplete)):
                    network.open("https://example.org")
                self.assertEqual(request.call_count, attempts)
        network = verify.Network()
        success = io.BytesIO(b"ok")
        error = urllib.error.HTTPError(
            "https://example.org", 503, "fixture", {"Retry-After": "0"}, None
        )
        with (
            mock.patch.object(network.opener, "open", side_effect=[error, success]),
            mock.patch.object(verify.time, "sleep"),
        ):
            self.assertEqual(network.open("https://example.org").read(), b"ok")
            self.assertEqual(len(network.retries), 1)

    def test_workflow_dependencies_and_read_only_entrypoint(self):
        ci = (ROOT / ".github/workflows/ci.yml").read_text().split("  verify-release:\n")[1]
        self.assertIn(
            "needs: [source, publish-server, publish-desktop, publish-macos, publish-container]", ci
        )
        workflow = (ROOT / ".github/workflows/verify-release.yml").read_text()
        self.assertIn("workflow_dispatch:", workflow)
        self.assertIn("scripts/verify-release", workflow)
        self.assertIn("if: always()", workflow)
        self.assertNotIn("contents: write", workflow)
        self.assertNotIn("secrets.", workflow)
        self.assertNotIn("GH_TOKEN", workflow)


if __name__ == "__main__":
    unittest.main()
