#!/usr/bin/env python3
"""Exercise immutable Go resource exports and safe retained-runtime packaging."""

import contextlib
import gzip
import importlib.util
import io
import json
import shutil
from pathlib import Path
import subprocess
import sys
import tarfile
import tempfile
import unittest
from unittest import mock
import hashlib

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "scripts"))
import go_packaging  # noqa: E402


def exporter():
    loader = __import__("importlib.machinery").machinery.SourceFileLoader(
        "export_go_resources", str(ROOT / "scripts/export-go-sandbox-resources")
    )
    spec = importlib.util.spec_from_loader(loader.name, loader)
    module = importlib.util.module_from_spec(spec)
    loader.exec_module(module)
    return module


class GoPackaging(unittest.TestCase):
    def guest_fixture(self, root):
        """Use real source/localization inputs with tiny verified vendor archives."""
        for relative in (
            "resources/sandbox/linux/default",
            "resources/app/locales",
            "desktop",
        ):
            shutil.copytree(
                ROOT / relative,
                root / relative,
                ignore=shutil.ignore_patterns("__pycache__", "*.pyc"),
            )
        (root / "scripts").mkdir()
        shutil.copy2(ROOT / "scripts/localize-web", root / "scripts/localize-web")
        build = root / ".local/go"
        (build / "downloads").mkdir(parents=True)
        (build / "resources").mkdir()
        hashes = {}
        for arch in ("arm64", "amd64"):
            platform = "linux-" + arch
            name = f"node-v{go_packaging.NODE_VERSION}-{platform.replace('amd64', 'x64')}"
            archive = build / "downloads" / (name + ".tar.gz")
            with tarfile.open(archive, "w:gz") as target:
                data = ("node fixture for " + arch).encode()
                item = tarfile.TarInfo(name + "/bin/node")
                item.size = len(data)
                target.addfile(item, io.BytesIO(data))
            hashes[platform] = go_packaging.digest(archive)
        novnc = build / "resources/novnc.tar.gz"
        with tarfile.open(novnc, "w:gz") as target:
            data = b"noVNC fixture"
            item = tarfile.TarInfo("noVNC-1.7.0/LICENSE.txt")
            item.size = len(data)
            target.addfile(item, io.BytesIO(data))
        recipe = root / "resources/sandbox/linux/default"
        metadata = json.loads((recipe / "desktop-assets.json").read_text())
        for entry in metadata["assets"]:
            data = ("desktop fixture for " + entry["filename"]).encode()
            for cache in (build / "downloads", build / "resources"):
                (cache / entry["filename"]).write_bytes(data)
            entry["sha256"] = hashlib.sha256(data).hexdigest()
        (recipe / "desktop-assets.json").write_text(json.dumps(metadata))
        return build, hashes, go_packaging.digest(novnc)

    @contextlib.contextmanager
    def guest_environment(self, root):
        build, hashes, novnc_hash = self.guest_fixture(root)

        def compile_executor(package, destination, platform, **_):
            self.assertEqual(package, "./cmd/pot-tool-executor")
            destination.write_bytes(("static executor fixture for " + platform).encode())

        with (
            mock.patch.object(go_packaging, "ROOT", root),
            mock.patch.object(go_packaging, "BUILD", build),
            mock.patch.object(go_packaging, "NODE_HASHES", hashes),
            mock.patch.object(go_packaging, "NOVNC_HASH", novnc_hash),
            mock.patch.object(go_packaging, "go_build", side_effect=compile_executor),
            mock.patch.object(
                go_packaging.urllib.request,
                "urlopen",
                side_effect=AssertionError("Guest preparation unexpectedly used the network"),
            ),
        ):
            yield build

    def test_guest_preparation_uses_target_node_and_atomic_complete_inputs(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            with self.guest_environment(root):
                for arch in ("arm64", "amd64"):
                    with self.subTest(architecture=arch):
                        assets = root / "contexts" / arch / "assets"
                        manifest = go_packaging.prepare_guest_assets(assets, arch)
                        self.assertEqual(manifest["architecture"], arch)
                        self.assertEqual(manifest["base"]["distribution"], "debian")
                        self.assertEqual(manifest["base"]["suite"], "trixie")
                        self.assertEqual(manifest["base"], go_packaging.guest_image_config())
                        self.assertEqual(
                            sorted(manifest["assets"]),
                            sorted(item.name for item in assets.iterdir()),
                        )
                        self.assertIn("index.html", manifest["assets"])
                        self.assertIn("desktop-assets.json", manifest["assets"])
                        self.assertIn(arch, (assets / "pot-tool-executor").read_text())
                        expected = go_packaging.NODE_HASHES["linux-" + arch]
                        self.assertEqual(go_packaging.digest(assets / "node.tar.gz"), expected)
                        self.assertEqual(
                            (assets / "node.sha256").read_text(), expected + "  node.tar.gz\n"
                        )
                        for archive in ("adw-gtk3", "adwaita-fonts"):
                            checksum = (assets / (archive + ".sha256")).read_text().split()[0]
                            self.assertEqual(
                                go_packaging.digest(assets / (archive + ".tar.xz")), checksum
                            )
                        self.assertTrue((assets / "adw-gtk3.LICENSE").is_file())
                        self.assertFalse(any(item.suffix == ".pyc" for item in assets.iterdir()))

    def test_guest_preparation_rejects_cross_arch_cache_and_leaves_no_partial_assets(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            with self.guest_environment(root) as build:
                arm = build / "downloads" / f"node-v{go_packaging.NODE_VERSION}-linux-arm64.tar.gz"
                x64 = build / "downloads" / f"node-v{go_packaging.NODE_VERSION}-linux-x64.tar.gz"
                arm.write_bytes(x64.read_bytes())
                destination = root / "context/assets"
                with self.assertRaisesRegex(ValueError, "[Cc]hecksum"):
                    go_packaging.prepare_guest_assets(destination, "arm64")
                self.assertFalse(destination.exists())
                self.assertEqual(list(destination.parent.iterdir()), [])

    def test_guest_preparation_refuses_missing_source_and_stale_destination(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            with self.guest_environment(root):
                destination = root / "assets"
                destination.mkdir()
                marker = destination / "stale-executor"
                marker.write_text("old architecture")
                with self.assertRaisesRegex(ValueError, "fresh|empty"):
                    go_packaging.prepare_guest_assets(destination, "amd64")
                self.assertEqual(marker.read_text(), "old architecture")
                (root / "resources/sandbox/linux/default/executor.py").unlink()
                fresh = root / "fresh/assets"
                with self.assertRaisesRegex(ValueError, "Missing guest asset"):
                    go_packaging.prepare_guest_assets(fresh, "arm64")
                self.assertFalse(fresh.exists())
                with self.assertRaisesRegex(ValueError, "architecture"):
                    go_packaging.prepare_guest_assets(fresh, "riscv64")

    def test_guest_base_rejects_floating_or_other_distribution_inputs(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            with self.guest_environment(root):
                path = root / "resources/sandbox/linux/default/base-image.json"
                original = json.loads(path.read_text())
                for field, value in (
                    ("base_image", "docker.io/library/debian:trixie-slim"),
                    ("distribution", "alpine"),
                    ("snapshot", "latest"),
                ):
                    with self.subTest(field=field):
                        path.write_text(json.dumps({**original, field: value}))
                        with self.assertRaisesRegex(ValueError, "pinned Debian"):
                            go_packaging.guest_image_config()

    def minimal_oci(
        self, layout, label, *, nested=False, architecture="arm64", source_hash="fixture"
    ):
        """Write real OCI descriptors, an image config and a gzip tar layer."""
        blobs = layout / "blobs/sha256"
        blobs.mkdir(parents=True)
        raw = io.BytesIO()
        with tarfile.open(fileobj=raw, mode="w") as archive:
            data = ("PRETTY_NAME=" + label + "\n").encode()
            item = tarfile.TarInfo("etc/os-release")
            item.size = len(data)
            archive.addfile(item, io.BytesIO(data))

        def blob(data, media):
            encoded = data if isinstance(data, bytes) else json.dumps(data).encode()
            digest = hashlib.sha256(encoded).hexdigest()
            (blobs / digest).write_bytes(encoded)
            return {"mediaType": media, "digest": "sha256:" + digest, "size": len(encoded)}

        config = blob(
            {
                "architecture": architecture,
                "os": "linux",
                # This is image configuration, not another OCI descriptor.
                "config": {
                    "Cmd": ["/bin/sh"],
                    "Env": ["FIXTURE=" + label],
                    "Labels": {"dev.pot.input.sha256": source_hash},
                },
                "rootfs": {
                    "type": "layers",
                    "diff_ids": ["sha256:" + hashlib.sha256(raw.getvalue()).hexdigest()],
                },
            },
            "application/vnd.oci.image.config.v1+json",
        )
        layer = blob(
            gzip.compress(raw.getvalue(), mtime=0),
            "application/vnd.oci.image.layer.v1.tar+gzip",
        )
        manifest = blob(
            {"schemaVersion": 2, "config": config, "layers": [layer]},
            "application/vnd.oci.image.manifest.v1+json",
        )
        if nested:
            manifest = blob(
                {"schemaVersion": 2, "manifests": [manifest]},
                "application/vnd.oci.image.index.v1+json",
            )
        manifest["annotations"] = {
            "org.opencontainers.image.ref.name": "localhost/pot-sandbox:test"
        }
        index = {"schemaVersion": 2, "manifests": [manifest]}
        (layout / "index.json").write_text(json.dumps(index))
        (layout / "oci-layout").write_text('{"imageLayoutVersion":"1.0.0"}')
        return index, blobs / layer["digest"].split(":", 1)[1]

    def tree_bytes(self, root):
        return {
            path.relative_to(root).as_posix(): path.read_bytes()
            for path in root.rglob("*")
            if path.is_file()
        }

    def metadata(self, layout, architecture="arm64"):
        descriptor, _ = go_packaging.oci_image_descriptor(layout, architecture)
        return {
            "schema_version": 1,
            "os": "linux",
            "architecture": architecture,
            "manifest_digest": descriptor["digest"],
            "reference": "localhost/pot-sandbox:test",
        }

    def test_nested_oci_selects_leaf_manifest_and_publishes_one_complete_directory(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            layout, output = root / "image", root / "published"
            index, _ = self.minimal_oci(layout, "nested-image", nested=True)
            metadata = self.metadata(layout)
            self.assertNotEqual(metadata["manifest_digest"], index["manifests"][0]["digest"])
            go_packaging.publish_guest_image(layout, output, metadata)
            self.assertEqual(self.tree_bytes(output / "sandbox-image"), self.tree_bytes(layout))
            self.assertEqual(json.loads((output / "sandbox-image.json").read_text()), metadata)
            self.assertEqual(
                sorted(path.name for path in output.iterdir()),
                ["sandbox-image", "sandbox-image.json"],
            )

    def test_invalid_oci_architecture_digest_and_publish_failure_preserve_previous_image(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            layout, output = root / "image", root / "published"
            _, layer = self.minimal_oci(layout, "new")
            metadata = self.metadata(layout)
            output.mkdir()
            (output / "old").write_bytes(b"retain old output")
            previous = self.tree_bytes(output)
            for invalid in (
                {**metadata, "architecture": "amd64"},
                {**metadata, "manifest_digest": "sha256:" + "0" * 64},
            ):
                with self.assertRaises(ValueError):
                    go_packaging.publish_guest_image(layout, output, invalid)
                self.assertEqual(self.tree_bytes(output), previous)
            original = layer.read_bytes()
            layer.write_bytes(b"x" * len(original))
            with self.assertRaisesRegex(ValueError, "missing or corrupt"):
                go_packaging.publish_guest_image(layout, output, metadata)
            self.assertEqual(self.tree_bytes(output), previous)
            layer.write_bytes(original)
            rename = Path.rename

            def fail_publish(source, target):
                if source.name == "new":
                    raise OSError("injected publish failure")
                return rename(source, target)

            with mock.patch.object(Path, "rename", new=fail_publish):
                with self.assertRaisesRegex(OSError, "injected publish"):
                    go_packaging.publish_guest_image(layout, output, metadata)
            self.assertEqual(self.tree_bytes(output), previous)
            self.assertEqual(sorted(path.name for path in root.iterdir()), ["image", "published"])

    def test_oci_rejects_linked_blobs_and_unsafe_archive_paths(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            layout = root / "image"
            _, layer = self.minimal_oci(layout, "new")
            real = root / "real-layer"
            layer.rename(real)
            layer.symlink_to(real)
            with self.assertRaisesRegex(ValueError, "Unsafe"):
                go_packaging.verify_oci_layout(layout)
            for name in ("../escaped", "/absolute", "same", "link"):
                with self.subTest(name=name):
                    archive, destination = root / "archive.tar", root / "unpacked"
                    with tarfile.open(archive, "w") as target:
                        item = tarfile.TarInfo(name)
                        item.size = 1
                        if name == "link":
                            item.type, item.linkname = tarfile.SYMTYPE, "outside"
                            target.addfile(item)
                        else:
                            target.addfile(item, io.BytesIO(b"x"))
                            if name == "same":
                                target.addfile(item, io.BytesIO(b"x"))
                    with self.assertRaises(ValueError):
                        go_packaging.unpack_oci_archive(archive, destination)
                    self.assertFalse(destination.exists())

    def test_podman_builder_requires_explicit_mac_connection_and_never_provisions(self):
        self.assertEqual(go_packaging.podman_command(system="Linux"), ["podman", "--remote=false"])
        self.assertEqual(
            go_packaging.podman_command("pot-builder", system="Darwin"),
            ["podman", "--connection", "pot-builder"],
        )
        with self.assertRaisesRegex(ValueError, "--connection"):
            go_packaging.podman_command(system="Darwin")
        for name in ("--default", "a b", "../builder"):
            with self.assertRaises(ValueError):
                go_packaging.podman_command(name)

    def test_prepared_context_build_binds_target_inputs_and_saved_leaf_manifest(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            with self.guest_environment(root):
                context, output, layout = root / "context", root / "output", root / "image"
                manifest = go_packaging.prepare_guest_context(context, "amd64")
                self.minimal_oci(
                    layout, "built", architecture="amd64", source_hash=manifest["source_hash"]
                )
                calls = []

                def call(command):
                    command = [str(item) for item in command]
                    calls.append(command)
                    if "save" in command:
                        with tarfile.open(command[command.index("--output") + 1], "w") as target:
                            for path in sorted(layout.rglob("*")):
                                target.add(path, arcname=path.relative_to(layout), recursive=False)

                with mock.patch.object(go_packaging, "call", side_effect=call):
                    result = go_packaging.build_guest_image(context, output, "amd64", "pot-builder")
                self.assertEqual(len(calls), 2)
                self.assertEqual(calls[0][:4], ["podman", "--connection", "pot-builder", "build"])
                self.assertIn("--platform=linux/amd64", calls[0])
                self.assertIn("BASE_IMAGE=" + manifest["base"]["base_image"], calls[0])
                self.assertIn("--format=oci-archive", calls[1])
                self.assertEqual(
                    result["manifest_digest"], self.metadata(layout, "amd64")["manifest_digest"]
                )
                self.assertEqual(result["source_hash"], manifest["source_hash"])
                (context / "assets/build-inputs.json").write_text("unreviewed nested input")
                with self.assertRaisesRegex(ValueError, "checksum changed"):
                    go_packaging.validate_guest_context(context, "amd64")

    def test_tray_patch_refuses_source_or_patch_drift(self):
        patch = (ROOT / "patches/wails-v3.0.0-beta.30-tray-aspect.patch").read_bytes()
        lines = patch.decode().splitlines(keepends=True)
        start = next(i for i, line in enumerate(lines) if line.startswith("@@ ")) + 1
        source = "".join(line[1:] for line in lines[start:] if line.startswith((" ", "-"))).encode()
        with mock.patch.object(
            go_packaging, "WAILS_TRAY_SOURCE_HASH", hashlib.sha256(source).hexdigest()
        ):
            patched = go_packaging.apply_tray_patch(source, patch)
            self.assertIn(b"originalSize.width / originalSize.height", patched)
            self.assertNotIn(b"NSMakeSize(thickness, thickness)", patched)
            with self.assertRaisesRegex(ValueError, "source changed"):
                go_packaging.apply_tray_patch(source + b"changed", patch)
            with self.assertRaisesRegex(ValueError, "patch changed"):
                go_packaging.apply_tray_patch(source, patch + b"changed")

    def test_resource_exports_preserve_exact_reviewed_bytes_and_both_arch_defaults(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            output, catalog = root / "output", root / "catalog.json"
            export = exporter().export
            with contextlib.redirect_stdout(io.StringIO()):
                for arch in ("arm64", "amd64"):
                    source = root / arch
                    self.minimal_oci(
                        source / "sandbox-image", "reviewed-" + arch, architecture=arch
                    )
                    metadata = self.metadata(source / "sandbox-image", arch)
                    (source / "sandbox-image.json").write_text(json.dumps(metadata))
                    export(source, output, "sandbox-go-resources-v0.3.0", catalog, True, arch)
                    first = self.tree_bytes(output)
                    export(source, output, "sandbox-go-resources-v0.3.0", catalog, False, arch)
                    self.assertEqual(first, self.tree_bytes(output))
            trust = json.loads(catalog.read_text())
            self.assertEqual(trust["schema_version"], 2)
            self.assertEqual(set(trust["default_linux_bundles"]), {"arm64", "amd64"})
            self.assertEqual(len(trust["resources"]), 2)
            self.assertTrue(
                all(entry["format"] == "oci-tar-gzip" for entry in trust["resources"].values())
            )
            source = root / "amd64"
            (source / "sandbox-image/unreviewed-extra").write_bytes(b"changed reviewed bytes")
            with self.assertRaisesRegex(ValueError, "differ from embedded"):
                export(source, output, "sandbox-go-resources-v0.3.0", catalog, False, "amd64")
            (source / "sandbox-image/unreviewed-extra").unlink()
            (source / "sandbox-image/extra").symlink_to(catalog)
            with self.assertRaisesRegex(ValueError, "Unsafe"):
                export(source, output, "sandbox-go-resources-v0.3.0", catalog, True, "amd64")

    def test_resource_export_rejects_reference_that_does_not_select_manifest(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            source, output, catalog = root / "source", root / "output", root / "catalog.json"
            self.minimal_oci(source / "sandbox-image", "reviewed", nested=True)
            metadata = self.metadata(source / "sandbox-image")
            metadata["reference"] = "localhost/pot-sandbox:unrelated"
            (source / "sandbox-image.json").write_text(json.dumps(metadata))
            with self.assertRaisesRegex(ValueError, "reference does not select"):
                exporter().export(source, output, "sandbox-go-resources-v0.3.0", catalog, True)
            self.assertFalse(output.exists())
            self.assertFalse(catalog.exists())
            metadata["reference"] = "localhost/pot-sandbox:test"
            (source / "sandbox-image.json").write_text(json.dumps(metadata))
            with contextlib.redirect_stdout(io.StringIO()):
                exporter().export(source, output, "sandbox-go-resources-v0.3.0", catalog, True)
            self.assertEqual(len(list(output.iterdir())), 1)

    def test_download_cache_and_tar_extraction_fail_closed(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            archive = root / "unsafe.tar"
            with tarfile.open(archive, "w") as target:
                member = tarfile.TarInfo("../escaped")
                member.size = 1
                target.addfile(member, io.BytesIO(b"x"))
            with self.assertRaises(tarfile.FilterError):
                go_packaging.extract(archive, root / "destination")
            self.assertFalse((root / "escaped").exists())
            cached = root / "download"
            cached.write_bytes(b"corrupt")
            with self.assertRaisesRegex(ValueError, "Checksum mismatch"):
                go_packaging.download("https://invalid.example/never-requested", cached, "0" * 64)

    def test_service_render_is_reviewable_and_never_installs(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            binary = root / "package/bin/pot"
            binary.parent.mkdir(parents=True)
            binary.write_bytes(b"fixture")
            output = root / "artifacts/pot-go-runtime.service"
            subprocess.run(
                [
                    sys.executable,
                    ROOT / "scripts/render-go-service",
                    "--binary",
                    binary,
                    "--root",
                    root / "fresh-go-data",
                    "--output",
                    output,
                ],
                check=True,
                capture_output=True,
            )
            unit = output.read_text()
            self.assertIn(str(binary), unit)
            self.assertIn(str(root / "fresh-go-data"), unit)
            self.assertIn("UMask=0077", unit)
            self.assertFalse((root / "fresh-go-data").exists())
            self.assertFalse((root / ".config/systemd/user").exists())


if __name__ == "__main__":
    unittest.main()
