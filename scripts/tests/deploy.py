import base64
import contextlib
import copy
import hashlib
import importlib.util
import io
import json
import os
import pathlib
import shutil
import stat
import sys
import tarfile
import tempfile
import unittest
from unittest import mock

ROOT = pathlib.Path(__file__).resolve().parents[2]
_spec = importlib.util.spec_from_file_location(
    "pot_deploy_remote", ROOT / "scripts" / "pot_deploy_remote.py"
)
remote = importlib.util.module_from_spec(_spec)
sys.modules["pot_deploy_remote"] = remote
_spec.loader.exec_module(remote)

REV = "a" * 40
PLATFORM = "linux-amd64"
BASE_KEY = base64.b64encode(b"\x00" * 32).decode()
META = {
    "kind": "cli",
    "protocol": 2,
    "platform": PLATFORM,
    "version": "0.0.0",
    "storage_format": "format1",
}


class DeployFixture(unittest.TestCase):
    def setUp(self):
        self._tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self._tmp.cleanup)
        self.base = pathlib.Path(self._tmp.name).resolve()

        self.install = self.base / "installed"
        (self.install / "bin").mkdir(parents=True)
        (self.install / "Resources").mkdir()
        pot = self.install / "bin" / "pot"
        pot.write_bytes(b"old")
        os.chmod(pot, 0o755)
        (self.install / "Resources" / "update.json").write_text(json.dumps(META))

        self.data = self.base / "data-a"
        self.data.mkdir()
        self.backup = self.base / "backup"
        self.backup.mkdir(mode=0o700)

        self.cfg = {
            "host": "test-host",
            "install_dir": str(self.install),
            "data_roots": [str(self.data)],
            "backup_dir": str(self.backup),
            "services": ["pot.service"],
            "systemd_scope": "user",
            "platform": PLATFORM,
            "update_public_key": BASE_KEY,
        }

        self._run_patcher = mock.patch.object(
            remote,
            "run",
            side_effect=AssertionError("unexpected subprocess"),
        )
        self.mock_run = self._run_patcher.start()
        self.addCleanup(self._run_patcher.stop)

    def make_request(self, archive):
        archive = pathlib.Path(archive)
        data = archive.read_bytes()
        return {
            "config": copy.deepcopy(self.cfg),
            "operation_id": "c" * 32,
            "archive": str(archive),
            "archive_size": len(data),
            "sha256": hashlib.sha256(data).hexdigest(),
            "revision": REV,
            "interrupt": True,
            "readiness_timeout": 1,
        }

    def build_archive(self, path=None, extras=None, metadata=None):
        if path is None:
            path = self.base / "package.tar.gz"
        path = pathlib.Path(path)
        metadata = META if metadata is None else metadata
        top = "pot-linux-amd64"
        with tarfile.open(str(path), "w:gz") as tar:
            for d in (top, f"{top}/bin", f"{top}/Resources"):
                ti = tarfile.TarInfo(d)
                ti.type = tarfile.DIRTYPE
                ti.mode = 0o755
                tar.addfile(ti, io.BytesIO(b""))
            pot = b"new"
            ti = tarfile.TarInfo(f"{top}/bin/pot")
            ti.size = len(pot)
            ti.mode = 0o755
            tar.addfile(ti, io.BytesIO(pot))
            meta_bytes = json.dumps(metadata).encode()
            ti = tarfile.TarInfo(f"{top}/Resources/update.json")
            ti.size = len(meta_bytes)
            ti.mode = 0o644
            tar.addfile(ti, io.BytesIO(meta_bytes))
            node = b"node"
            ti = tarfile.TarInfo(f"{top}/Resources/node")
            ti.size = len(node)
            ti.mode = 0o644
            tar.addfile(ti, io.BytesIO(node))
            for name, kind, payload in extras or []:
                ti = tarfile.TarInfo(name)
                if kind == "file":
                    ti.size = len(payload)
                    ti.mode = 0o644
                    tar.addfile(ti, io.BytesIO(payload))
                elif kind == "symlink":
                    ti.type = tarfile.SYMTYPE
                    ti.linkname = payload
                    tar.addfile(ti)
                elif kind == "hardlink":
                    ti.type = tarfile.LNKTYPE
                    ti.linkname = payload
                    tar.addfile(ti)
                elif kind == "fifo":
                    ti.type = tarfile.FIFOTYPE
                    tar.addfile(ti)
                else:
                    raise ValueError(kind)
        return path

    def resolve(self, request=None):
        if request is None:
            request = {
                "config": copy.deepcopy(self.cfg),
                "operation_id": "c" * 32,
                "readiness_timeout": 1,
                "interrupt": True,
            }
        return remote.resolve_config(request)

    def stages(self):
        return list(self.base.glob(".installed.deploy-stage-*"))

    def assert_no_stage(self):
        self.assertEqual(self.stages(), [])


class ArchiveTests(DeployFixture):
    def test_valid_package_with_symlink(self):
        archive = self.build_archive(extras=[("pot-linux-amd64/Resources/npm", "symlink", "node")])
        req = self.make_request(archive)
        cfg = self.resolve(req)
        self.mock_run.side_effect = None
        self.mock_run.return_value = "pot 0.0.0 (" + REV + ")"
        try:
            stage, candidate, metadata = remote.extract_package(req, cfg)
            self.assertEqual(metadata, META)
            self.mock_run.assert_called()
            staged = self.stages()
            self.assertEqual(len(staged), 1)
            s = staged[0]
            st = os.lstat(str(s))
            self.assertTrue(stat.S_ISDIR(st.st_mode))
            self.assertEqual(st.st_mode & 0o777, 0o700)
            self.assertEqual(st.st_uid, os.geteuid())
            self.assertEqual(candidate, s / "pot-linux-amd64")
            self.assertEqual(stage, s)
            self.assertTrue(candidate.is_dir())
            pot_bytes = (candidate / "bin" / "pot").read_bytes()
            self.assertEqual(pot_bytes, b"new")
            meta = json.loads((candidate / "Resources" / "update.json").read_text())
            self.assertEqual(meta, META)
            npm = candidate / "Resources" / "npm"
            self.assertTrue(npm.is_symlink())
            self.assertEqual(os.readlink(str(npm)), "node")
        finally:
            for p in self.stages():
                shutil.rmtree(p, ignore_errors=True)

    def test_invalid_archives(self):
        outside = self.base / "outside"
        outside.mkdir()
        sentinel = outside / "sentinel"
        sentinel.write_bytes(b"safe")

        cases = [
            ("pot-linux-amd64/../escape", "file", b"x"),
            ("/abs", "file", b"x"),
            ("extra/file", "file", b"x"),
            ("pot-linux-amd64/Resources/./update.json", "file", b"{}"),
            ("pot-linux-amd64/Resources/escape", "symlink", "../../../outside/sentinel"),
            ("pot-linux-amd64/Resources/hl", "hardlink", "pot-linux-amd64/Resources/node"),
            ("pot-linux-amd64/Resources/fifo", "fifo", None),
        ]
        for idx, (name, kind, payload) in enumerate(cases):
            with self.subTest(idx=idx):
                path = self.base / f"bad-{idx}.tar.gz"
                self.build_archive(path=path, extras=[(name, kind, payload)])
                req = self.make_request(path)
                cfg = self.resolve(req)
                with self.assertRaises(remote.DeploymentError):
                    remote.extract_package(req, cfg)
                self.assert_no_stage()
                self.mock_run.assert_not_called()
                self.assertEqual(sentinel.read_bytes(), b"safe")

    def test_symlink_ancestor_escape(self):
        archive = self.build_archive(
            extras=[
                ("pot-linux-amd64/Resources/link", "symlink", "../bin"),
                ("pot-linux-amd64/Resources/link/injected", "file", b"evil"),
            ]
        )
        req = self.make_request(archive)
        cfg = self.resolve(req)
        with self.assertRaises(remote.DeploymentError):
            remote.extract_package(req, cfg)
        self.assert_no_stage()

    def test_sha_and_size_mismatch(self):
        archive = self.build_archive()
        req = self.make_request(archive)
        cfg = self.resolve(req)

        bad_sha = copy.deepcopy(req)
        bad_sha["sha256"] = "b" * 64
        with self.assertRaises(remote.DeploymentError):
            remote.extract_package(bad_sha, cfg)
        self.assert_no_stage()

        bad_size = copy.deepcopy(req)
        bad_size["archive_size"] = req["archive_size"] + 1
        with self.assertRaises(remote.DeploymentError):
            remote.extract_package(bad_size, cfg)
        self.assert_no_stage()

    def test_metadata_mismatch(self):
        for label, patch in (
            ("platform", {"platform": "linux-arm64"}),
            ("protocol", {"protocol": 3}),
        ):
            with self.subTest(label=label):
                meta = dict(META)
                meta.update(patch)
                path = self.base / f"meta-{label}.tar.gz"
                self.build_archive(path=path, metadata=meta)
                req = self.make_request(path)
                cfg = self.resolve(req)
                with self.assertRaises(remote.DeploymentError):
                    remote.extract_package(req, cfg)
                self.assert_no_stage()
                self.mock_run.assert_not_called()

    def test_wrong_version_rejected(self):
        archive = self.build_archive()
        req = self.make_request(archive)
        cfg = self.resolve(req)
        self.mock_run.side_effect = None
        self.mock_run.return_value = "pot 9.9.9 (" + REV + ")"
        with self.assertRaises(remote.DeploymentError):
            remote.extract_package(req, cfg)
        self.mock_run.assert_called()
        self.assert_no_stage()


class GuardTests(DeployFixture):
    def test_operation_identity_validation(self):
        cfg = self.resolve()
        self.assertIsInstance(cfg, dict)
        bad_values = [None, "", "C" * 32, "c" * 31, "c" * 33, 123]
        for value in bad_values:
            with self.subTest(operation_id=value):
                request = {
                    "config": copy.deepcopy(self.cfg),
                    "operation_id": value,
                    "readiness_timeout": 1,
                }
                with self.assertRaises(remote.DeploymentError):
                    remote.resolve_config(request)
        with self.subTest(operation_id="<missing>"):
            request = {
                "config": copy.deepcopy(self.cfg),
                "readiness_timeout": 1,
            }
            with self.assertRaises(remote.DeploymentError):
                remote.resolve_config(request)

    def test_data_root_overlaps_install(self):
        self.cfg["data_roots"] = [str(self.install / "sub")]
        (self.install / "sub").mkdir()
        with self.assertRaises(remote.DeploymentError):
            self.resolve()

    def test_backup_overlaps_data(self):
        self.cfg["backup_dir"] = str(self.data)
        with self.assertRaises(remote.DeploymentError):
            self.resolve()

    def test_duplicate_roots(self):
        self.cfg["data_roots"] = [str(self.data), str(self.data)]
        with self.assertRaises(remote.DeploymentError):
            self.resolve()

    def test_private_dir_symlink_rejects(self):
        real = self.base / "real-private"
        real.mkdir(mode=0o700)
        link = self.base / "link-private"
        link.symlink_to(real)
        with self.assertRaises(remote.DeploymentError):
            remote.private_dir(link)

    def test_lock_file_busy(self):
        lock_path = self.base / "the.lock"
        with remote.lock_file(lock_path):
            with self.assertRaises(remote.DeploymentError):
                with remote.lock_file(lock_path):
                    pass
        with remote.lock_file(lock_path):
            pass

    def test_read_json_symlink_rejects(self):
        target = self.base / "target.json"
        target.write_text("{}")
        link = self.base / "link.json"
        link.symlink_to(target)
        with self.assertRaises((OSError, remote.DeploymentError)):
            remote.read_json(link)


import importlib.machinery


class LocalDeployTests(DeployFixture):
    def setUp(self):
        super().setUp()
        self.config_file = self.base / "config.json"
        self.config_file.write_text(json.dumps(self.cfg))
        loader = importlib.machinery.SourceFileLoader(
            "local_deploy", str(ROOT / "scripts" / "deploy")
        )
        spec = importlib.util.spec_from_loader("local_deploy", loader)
        self.local = importlib.util.module_from_spec(spec)
        sys.modules["local_deploy"] = self.local
        loader.exec_module(self.local)

    def _patch_local(self):
        sp = mock.patch.object(
            self.local.subprocess,
            "run",
            side_effect=AssertionError("unexpected subprocess"),
        )
        sp.start()
        self.addCleanup(sp.stop)
        bs = mock.patch.object(
            self.local,
            "build_source",
            side_effect=AssertionError("unexpected build"),
        )
        bs.start()
        self.addCleanup(bs.stop)

    def _run_main(self, argv):
        buf = io.StringIO()
        with contextlib.redirect_stdout(buf):
            rc = self.local.run_main(argv)
        return rc, buf.getvalue()

    def test_offline_source_plan_no_subprocess(self):
        self._patch_local()
        ref = "$(touch bad)"
        rc, out = self._run_main(["--config", str(self.config_file), "--ref", ref])
        self.assertEqual(rc, 0)
        self.assertTrue((self.base / "bad").exists() is False)
        plan = json.loads(out)["plan"]
        self.assertEqual(plan["ref"], ref)
        self.assertIn("source", plan)

    def test_valid_offline_package_checksum(self):
        self._patch_local()
        pkg = self.build_archive()
        data = pkg.read_bytes()
        sha = hashlib.sha256(data).hexdigest()
        rc, out = self._run_main(
            [
                "--config",
                str(self.config_file),
                "--package",
                str(pkg),
                "--revision",
                REV,
                "--sha256",
                sha,
            ]
        )
        self.assertEqual(rc, 0)
        plan = json.loads(out)["plan"]
        self.assertEqual(plan["sha256"], sha)
        self.assertEqual(plan["revision"], REV)
        self.assertEqual(pathlib.Path(plan["package"]), pkg)

    def test_bad_checksum_apply_fails_before_subprocess(self):
        self._patch_local()
        pkg = self.build_archive()
        bad = "b" * 64
        with self.assertRaises(self.local.Fail):
            self._run_main(
                [
                    "--config",
                    str(self.config_file),
                    "--package",
                    str(pkg),
                    "--revision",
                    REV,
                    "--sha256",
                    bad,
                    "--apply",
                ]
            )
        self.mock_run.assert_not_called()


class ReadinessTests(DeployFixture):
    def setUp(self):
        super().setUp()
        self.previous = {
            "pot.service": {
                "pid": 101,
                "root": str(self.data),
                "boot_id": "old",
                "version": "0.0.0",
            }
        }
        self.live = {
            101: {
                "root": str(self.data),
                "boot_id": "fresh",
                "version": "0.0.0",
                "work": {"tasks": 3, "replies": 1, "sessions": 2},
                "socket": "/tmp/fake",
            }
        }

    def _inspect(self, live=None, executable=True, previous=None):
        if live is None:
            live = self.live
        if previous is None:
            previous = self.previous

        def fake_service_info(config, unit):
            return {
                "pid": 101,
                "active": "active",
                "sub": "running",
            }

        with (
            mock.patch.object(remote, "service_info", side_effect=fake_service_info),
            mock.patch.object(remote, "executable_matches", return_value=executable),
            mock.patch.object(remote, "participants", return_value=live),
        ):
            return remote.inspect_services(self.resolve(), META, previous=previous, allow_work=True)

    def test_same_pid_new_boot_accepted_with_active_work(self):
        result = self._inspect()
        self.assertIn("pot.service", result)
        self.assertEqual(result["pot.service"]["pid"], 101)
        self.assertEqual(result["pot.service"]["boot_id"], "fresh")
        self.assertEqual(result["pot.service"]["version"], "0.0.0")

    def test_same_boot_rejected(self):
        live = {101: dict(self.live[101], boot_id="old")}
        with self.assertRaises(remote.DeploymentError):
            self._inspect(live=live)

    def test_different_executable_rejected(self):
        with self.assertRaises(remote.DeploymentError):
            self._inspect(executable=False)

    def test_extra_live_participant_rejected(self):
        live = dict(self.live)
        live[202] = dict(self.live[101], boot_id="another")
        with self.assertRaises(remote.DeploymentError):
            self._inspect(live=live)

    def test_different_version_rejected(self):
        live = {101: dict(self.live[101], version="9.9.9")}
        with self.assertRaises(remote.DeploymentError):
            self._inspect(live=live)

    def test_unknown_root_rejected(self):
        other = self.install / "other-root"
        live = {101: dict(self.live[101], root=str(other))}
        with self.assertRaises(remote.DeploymentError):
            self._inspect(live=live)


import shlex


class LocalTransportTests(LocalDeployTests):
    def test_run_main_local_transport_worker_recovery_matrix(self):
        self.cfg["ssh_options"] = ["-o", "ServerAliveInterval=9"]
        with open(self.config_file, "w") as f:
            json.dump(self.cfg, f)

        cases = [
            ("worker255", None),
            ("malformed", (0, b"notjson")),
            ("nonobject", (0, b"[]")),
            ("oserror", "oserror"),
        ]

        for name, override in cases:
            with self.subTest(case=name):
                upload_calls = []
                cleanup_calls = []
                run_calls = []

                def fake_upload(*a, **kw):
                    upload_calls.append((a, kw))

                def fake_cleanup(*a, **kw):
                    cleanup_calls.append((a, kw))

                worker_payload = {}

                def fake_run(cmd, *a, **kw):
                    run_calls.append((cmd, a, kw))
                    idx = len(run_calls)
                    if idx == 1:
                        payload = {
                            "upload_dir": "/tmp/.installed.upload-fixture",
                        }
                        return mock.Mock(returncode=0, stdout=json.dumps(payload).encode())
                    if idx == 2:
                        payload = json.loads(kw["input"]) if "input" in kw else None
                        worker_payload["data"] = payload
                        assert payload["operation_id"] == "c" * 32
                        if override is None:
                            return mock.Mock(returncode=255, stdout=b"")
                        if override == "oserror":
                            raise OSError("boom")
                        rc, out = override
                        return mock.Mock(returncode=rc, stdout=out)
                    if idx == 3:
                        payload = worker_payload["data"]
                        journal = {
                            "operation_id": payload["operation_id"],
                            "revision": payload["revision"],
                            "archive_sha256": payload["sha256"],
                            "phase": "completed",
                            "install": str(self.install),
                            "hosts": {"pot.service": {"pid": 1, "boot_id": "fresh"}},
                        }
                        return mock.Mock(returncode=0, stdout=json.dumps(journal).encode())
                    raise AssertionError("unexpected subprocess call %d: %r" % (idx, cmd))

                with (
                    mock.patch.object(self.local, "upload", mock.Mock(side_effect=fake_upload)),
                    mock.patch.object(
                        self.local, "cleanup_remote", mock.Mock(side_effect=fake_cleanup)
                    ),
                    mock.patch.object(
                        self.local.uuid, "uuid4", mock.Mock(return_value=mock.Mock(hex="c" * 32))
                    ),
                    mock.patch.object(
                        self.local.subprocess, "run", mock.Mock(side_effect=fake_run)
                    ),
                ):
                    rc, text = self._run_main(self.package_args())

                self.assertEqual(rc, 0)
                self.assertIn("recovered", text.lower())
                result = json.loads(text)["result"]
                self.assertTrue(result["recovered_from_journal"])
                self.assertEqual(
                    result["services"],
                    {"pot.service": {"pid": 1, "boot_id": "fresh"}},
                )

                self.assertEqual(len(run_calls), 3)
                self.assertEqual(len(upload_calls), 2)
                self.assertEqual(len(cleanup_calls), 0)

                worker_cmd = run_calls[1][0]
                self.assertEqual(worker_cmd[-1], "python3 /tmp/.installed.upload-fixture/worker.py")

                query_cmd, _, query_kw = run_calls[2]
                self.assertEqual(query_kw.get("timeout"), 30)

                ssh_argv = run_calls[0][0]
                self.assertIn("ServerAliveInterval=9", ssh_argv)
                self.assertIn("ServerAliveInterval=30", ssh_argv)
                self.assertIn("ConnectTimeout=15", ssh_argv)
                self.assertIn("ServerAliveCountMax=6", ssh_argv)
                idx_custom = ssh_argv.index("ServerAliveInterval=9")
                idx_default = ssh_argv.index("ServerAliveInterval=30")
                self.assertLess(idx_custom, idx_default)
                self.assertLess(idx_default, ssh_argv.index(self.cfg["host"]))

    def test_recover_worker_result_success(self):
        import json, subprocess
        from unittest import mock

        hosts = {
            "pot.service": {
                "pid": 123,
                "boot_id": "fresh",
                "root": str(self.data),
                "version": "0.0.0",
            }
        }
        journal = {
            "operation_id": "c" * 32,
            "revision": REV,
            "archive_sha256": "d" * 64,
            "phase": "completed",
            "install": str(self.install),
            "backup": None,
            "old_package": "/tmp/old",
            "hosts": hosts,
        }
        proc = subprocess.CompletedProcess(
            args=[], returncode=0, stdout=json.dumps(journal).encode("utf-8"), stderr=b""
        )
        payload = {"operation_id": "c" * 32, "revision": REV, "sha256": "d" * 64}
        with (
            mock.patch.object(self.local.subprocess, "run", return_value=proc) as run_mock,
            mock.patch.object(self.local, "upload") as upload_mock,
            mock.patch.object(self.local, "build_source") as build_mock,
            mock.patch.object(self.local, "cleanup_remote") as cleanup_mock,
        ):
            result = self.local.recover_worker_result(["ssh", "test-host"], self.cfg, payload)

        self.assertTrue(result["ok"])
        self.assertTrue(result["recovered_from_journal"])
        self.assertEqual(result["services"], hosts)
        self.assertEqual(run_mock.call_count, 1)
        args, kwargs = run_mock.call_args
        self.assertEqual(kwargs["timeout"], 30)
        self.assertEqual(
            json.loads(kwargs["input"].decode("utf-8")), {"install_dir": self.cfg["install_dir"]}
        )
        upload_mock.assert_not_called()
        build_mock.assert_not_called()
        cleanup_mock.assert_not_called()

    def test_recover_worker_result_mismatch_and_bad_phase(self):
        import json, subprocess
        from unittest import mock

        base_journal = {
            "operation_id": "c" * 32,
            "revision": REV,
            "archive_sha256": "d" * 64,
            "phase": "completed",
            "install": str(self.install),
            "backup": None,
            "old_package": "/tmp/old",
            "hosts": {},
        }
        payload = {"operation_id": "c" * 32, "revision": REV, "sha256": "d" * 64}
        mutations = [
            ("operation_id", "e" * 32),
            ("revision", "f" * 40),
            ("archive_sha256", "0" * 64),
            ("phase", "blocked"),
            ("phase", "starting"),
            ("phase", "stopping"),
            ("phase", "rolled_back"),
        ]
        for key, value in mutations:
            with self.subTest(field=key, value=value):
                journal = dict(base_journal)
                journal[key] = value
                proc = subprocess.CompletedProcess(
                    args=[], returncode=0, stdout=json.dumps(journal).encode("utf-8"), stderr=b""
                )
                with (
                    mock.patch.object(self.local.subprocess, "run", return_value=proc) as run_mock,
                    mock.patch.object(self.local, "upload") as upload_mock,
                    mock.patch.object(self.local, "build_source") as build_mock,
                    mock.patch.object(self.local, "cleanup_remote") as cleanup_mock,
                ):
                    with self.assertRaises(self.local.Fail):
                        self.local.recover_worker_result(["ssh", "test-host"], self.cfg, payload)
                self.assertEqual(run_mock.call_count, 1)
                upload_mock.assert_not_called()
                build_mock.assert_not_called()
                cleanup_mock.assert_not_called()

    def test_recover_worker_result_run_failures(self):
        import subprocess
        from unittest import mock

        payload = {"operation_id": "c" * 32, "revision": REV, "sha256": "d" * 64}
        scenarios = [
            ("oserror", OSError(2, "boom")),
            (
                "malformed",
                subprocess.CompletedProcess(args=[], returncode=0, stdout=b"{", stderr=b""),
            ),
            (
                "nonobject",
                subprocess.CompletedProcess(args=[], returncode=0, stdout=b"[]", stderr=b""),
            ),
            (
                "rc255",
                subprocess.CompletedProcess(args=[], returncode=255, stdout=b"{}", stderr=b""),
            ),
        ]
        for name, outcome in scenarios:
            with self.subTest(scenario=name):
                kwargs = {}
                if isinstance(outcome, OSError):
                    kwargs["side_effect"] = outcome
                else:
                    kwargs["return_value"] = outcome
                with (
                    mock.patch.object(self.local.subprocess, "run", **kwargs) as run_mock,
                    mock.patch.object(self.local, "upload") as upload_mock,
                    mock.patch.object(self.local, "build_source") as build_mock,
                    mock.patch.object(self.local, "cleanup_remote") as cleanup_mock,
                ):
                    with self.assertRaises(self.local.Fail):
                        self.local.recover_worker_result(["ssh", "test-host"], self.cfg, payload)
                self.assertEqual(run_mock.call_count, 1)
                upload_mock.assert_not_called()
                build_mock.assert_not_called()
                cleanup_mock.assert_not_called()

    def package_args(self):
        pkg = self.build_archive()
        sha = hashlib.sha256(pkg.read_bytes()).hexdigest()
        return [
            "--config",
            str(self.config_file),
            "--package",
            str(pkg),
            "--revision",
            REV,
            "--sha256",
            sha,
            "--apply",
        ]

    def test_upload_failure_cleans_up_once(self):
        boot = mock.Mock()
        boot.returncode = 0
        boot.stdout = json.dumps({"upload_dir": "/tmp/.installed.upload-fixture"}).encode()
        run = mock.patch.object(self.local.subprocess, "run", return_value=boot)
        self.mock_run = run.start()
        self.addCleanup(run.stop)

        upload = mock.patch.object(
            self.local,
            "upload",
            side_effect=self.local.Fail("upload failed"),
        )
        self.mock_upload = upload.start()
        self.addCleanup(upload.stop)

        cleanup = mock.patch.object(self.local, "cleanup_remote")
        self.mock_cleanup = cleanup.start()
        self.addCleanup(cleanup.stop)

        with self.assertRaises(self.local.Fail):
            self._run_main(self.package_args())

        self.assertEqual(self.mock_cleanup.call_count, 1)
        self.assertEqual(self.mock_run.call_count, 1)

    def test_worker_failure_no_cleanup_and_input(self):
        boot = mock.Mock()
        boot.returncode = 0
        boot.stdout = json.dumps({"upload_dir": "/tmp/.installed.upload-fixture"}).encode()
        worker = mock.Mock()
        worker.returncode = 7
        worker.stdout = json.dumps({"ok": False, "error": "worker failed"}).encode()
        run = mock.patch.object(
            self.local.subprocess,
            "run",
            side_effect=[boot, worker],
        )
        self.mock_run = run.start()
        self.addCleanup(run.stop)

        upload = mock.patch.object(self.local, "upload")
        self.mock_upload = upload.start()
        self.addCleanup(upload.stop)

        cleanup = mock.patch.object(self.local, "cleanup_remote")
        self.mock_cleanup = cleanup.start()
        self.addCleanup(cleanup.stop)

        buf = io.StringIO()
        with contextlib.redirect_stderr(buf):
            with self.assertRaises(self.local.Fail):
                self._run_main(self.package_args())

        self.assertEqual(self.mock_cleanup.call_count, 0)
        self.assertEqual(self.mock_run.call_count, 2)

        second = self.mock_run.call_args_list[1]
        payload = second.kwargs["input"]
        if isinstance(payload, bytes):
            payload = payload.decode()
        spec = json.loads(payload)
        self.assertEqual(spec["revision"], REV)
        self.assertEqual(
            pathlib.Path(spec["archive"]),
            pathlib.Path("/tmp/.installed.upload-fixture/package.tar.gz"),
        )
        self.assertNotIn("interrupt", spec)

    def test_upload_quoting(self):
        run = mock.patch.object(self.local.subprocess, "run")
        self.mock_run = run.start()
        self.mock_run.return_value = mock.Mock(returncode=0)
        self.addCleanup(run.stop)

        local = self.base / "upload-pkg.tar"
        local.write_bytes(b"data")
        hostile = "/tmp/a b;$(id)"

        self.local.upload(self.cfg, ["ssh", "test-host"], local, hostile)

        self.assertEqual(self.mock_run.call_count, 1)
        args, kwargs = self.mock_run.call_args
        cmd = args[0] if args else kwargs["args"]

        parsed = shlex.split(cmd[-1]) if isinstance(cmd, list) else shlex.split(cmd)
        self.assertEqual(parsed, ["cat", ">", hostile])
        self.assertNotIn("shell=True", str(kwargs))
        self.assertFalse(kwargs.get("shell", False))


class TransactionTests(DeployFixture):
    def setUp(self):
        super().setUp()
        self.mock_run.side_effect = None
        self.mock_run.return_value = "pot 0.0.0 (" + REV + ")"

        patcher = mock.patch.object(remote, "_check_launch_preconditions", lambda *a, **k: None)
        patcher.start()
        self.addCleanup(patcher.stop)

        self.prepare = mock.Mock(return_value=None)
        patcher = mock.patch.object(remote, "prepare_services", self.prepare)
        patcher.start()
        self.addCleanup(patcher.stop)

        self.stop = mock.Mock(return_value=None)
        patcher = mock.patch.object(remote, "stop_services", self.stop)
        patcher.start()
        self.addCleanup(patcher.stop)

        self.start = mock.Mock(return_value=None)
        patcher = mock.patch.object(remote, "start_services", self.start)
        patcher.start()
        self.addCleanup(patcher.stop)

        self.wait = mock.Mock(
            return_value={
                "pot.service": {
                    "pid": 123,
                    "root": str(self.data),
                    "boot_id": "boot",
                    "version": "0.0.0",
                    "socket": "/tmp/fake",
                }
            }
        )
        patcher = mock.patch.object(remote, "wait_ready", self.wait)
        patcher.start()
        self.addCleanup(patcher.stop)

        self.inspect = mock.Mock(
            return_value={
                "pot.service": {
                    "pid": 123,
                    "root": str(self.data),
                    "boot_id": "boot",
                    "version": "0.0.0",
                    "socket": "/tmp/fake",
                }
            }
        )
        patcher = mock.patch.object(remote, "inspect_services", self.inspect)
        patcher.start()
        self.addCleanup(patcher.stop)

        self.backup_mock = mock.Mock(return_value=None)
        patcher = mock.patch.object(remote, "backup_roots", self.backup_mock)
        patcher.start()
        self.addCleanup(patcher.stop)

    def test_success_same_storage_skips_backup(self):
        payload = self.build_archive()
        req = self.make_request(payload)
        old_bytes = (self.install / "bin" / "pot").read_bytes()

        result = remote.deploy(req)

        self.assertTrue(result["ok"])
        new_bytes = (self.install / "bin" / "pot").read_bytes()
        self.assertEqual(new_bytes, b"new")
        self.assertTrue((self.install / "bin" / "pot").is_file())
        self.assertNotEqual(new_bytes, old_bytes)
        self.assertIsNone(result.get("backup"))
        journal = remote.read_json(
            self.install.parent / (self.install.name + ".deploy") / "state.json"
        )
        self.assertEqual(journal["phase"], "completed")
        self.assertEqual(journal["operation_id"], "c" * 32)
        self.assertIs(journal["first_start_attempted"], True)
        self.assertFalse(journal["storage_changed"])
        self.assertEqual(self.start.call_count, 1)
        self.assertEqual(self.prepare.call_count, 1)
        self.assertEqual(self.stop.call_count, 1)
        self.assertEqual(self.wait.call_count, 1)
        self.backup_mock.assert_not_called()

    def test_storage_changed_backup_failure_rolls_back(self):
        payload = self.build_archive(metadata=dict(META, storage_format="format2"))
        req = self.make_request(payload)
        old_bytes = (self.install / "bin" / "pot").read_bytes()

        self.backup_mock.side_effect = remote.DeploymentError("backup boom")

        with self.assertRaises(remote.DeploymentError):
            remote.deploy(req)

        self.assertEqual((self.install / "bin" / "pot").read_bytes(), old_bytes)
        journal = remote.read_json(
            self.install.parent / (self.install.name + ".deploy") / "state.json"
        )
        self.assertEqual(journal["phase"], "rolled_back")
        self.assertIs(journal["first_start_attempted"], False)
        self.assertEqual(self.start.call_count, 1)
        self.assertEqual(self.stop.call_count, 2)

    def test_start_failure_blocked_keeps_new(self):
        payload = self.build_archive()
        req = self.make_request(payload)
        old_bytes = (self.install / "bin" / "pot").read_bytes()

        def start_side_effect(config):
            journal = remote.read_json(
                self.install.parent / (self.install.name + ".deploy") / "state.json"
            )
            self.assertEqual(journal["phase"], "starting")
            self.assertIs(journal["first_start_attempted"], True)
            raise remote.DeploymentError("start boom")

        self.start.side_effect = start_side_effect

        with self.assertRaises(remote.DeploymentError):
            remote.deploy(req)

        new_bytes = (self.install / "bin" / "pot").read_bytes()
        self.assertEqual(new_bytes, b"new")
        self.assertNotEqual(new_bytes, old_bytes)
        journal = remote.read_json(
            self.install.parent / (self.install.name + ".deploy") / "state.json"
        )
        self.assertEqual(journal["phase"], "blocked")
        self.assertIs(journal["first_start_attempted"], True)
        self.assertEqual(self.start.call_count, 1)
        self.assertEqual(self.stop.call_count, 2)

        with self.assertRaises(remote.DeploymentError):
            remote.deploy(req)
        self.assertEqual(self.start.call_count, 1)
        self.assertEqual(self.stop.call_count, 2)

    def test_prepare_failure_restarts_old_no_replacement(self):
        payload = self.build_archive()
        req = self.make_request(payload)
        old_bytes = (self.install / "bin" / "pot").read_bytes()

        self.prepare.side_effect = remote.DeploymentError("prepare boom")

        with self.assertRaises(remote.DeploymentError):
            remote.deploy(req)

        self.assertEqual((self.install / "bin" / "pot").read_bytes(), old_bytes)
        journal = remote.read_json(
            self.install.parent / (self.install.name + ".deploy") / "state.json"
        )
        self.assertEqual(journal["phase"], "rolled_back")
        self.assertIs(journal["first_start_attempted"], False)
        self.assertEqual(self.prepare.call_count, 1)
        self.assertEqual(self.start.call_count, 1)
        self.assertEqual(self.stop.call_count, 1)
        self.assertEqual(self.wait.call_count, 1)

    def test_stop_failure_blocked_old_retained_never_start(self):
        payload = self.build_archive()
        req = self.make_request(payload)
        old_bytes = (self.install / "bin" / "pot").read_bytes()

        self.stop.side_effect = remote.DeploymentError("stop boom")

        with self.assertRaises(remote.DeploymentError):
            remote.deploy(req)

        self.assertEqual((self.install / "bin" / "pot").read_bytes(), old_bytes)
        journal = remote.read_json(
            self.install.parent / (self.install.name + ".deploy") / "state.json"
        )
        self.assertEqual(journal["phase"], "blocked")
        self.assertIs(journal["first_start_attempted"], False)
        self.assertEqual(self.start.call_count, 0)
        self.assertEqual(self.stop.call_count, 2)
        self.assertEqual(self.prepare.call_count, 1)

    def test_second_rename_failure_restores_old_under_locks(self):
        payload = self.build_archive()
        req = self.make_request(payload)
        old_bytes = (self.install / "bin" / "pot").read_bytes()

        state_dir = self.install.parent / (self.install.name + ".deploy")
        update_lock = self.install.parent / "installed.updates" / "installation.lock"
        runtime_lock = self.data / "runtime.lock"
        web_lock = self.data / "web-ui-server.lock"
        locks = [update_lock, runtime_lock, web_lock]

        real_rename = os.rename
        rename_calls = []

        def rename_side_effect(src, dst):
            rename_calls.append((src, dst))
            for lock in locks:
                with self.assertRaises(remote.DeploymentError):
                    with remote.lock_file(lock):
                        pass
            if len(rename_calls) == 2:
                raise OSError("injected candidate rename failure")
            return real_rename(src, dst)

        def start_side_effect(config):
            self.assertEqual((self.install / "bin" / "pot").read_bytes(), old_bytes)
            for lock in locks:
                with remote.lock_file(lock):
                    pass

        self.start.side_effect = start_side_effect

        patcher = mock.patch.object(remote.os, "rename", rename_side_effect)
        patcher.start()
        self.addCleanup(patcher.stop)

        with self.assertRaises(remote.DeploymentError):
            remote.deploy(req)

        self.assertEqual(len(rename_calls), 3)
        journal = remote.read_json(state_dir / "state.json")
        self.assertEqual(journal["phase"], "rolled_back")
        self.assertIs(journal["first_start_attempted"], False)
        self.assertEqual(self.start.call_count, 1)

        version_calls = [c for c in self.mock_run.call_args_list if "--version" in str(c)]
        self.assertEqual(len(self.mock_run.call_args_list), len(version_calls))
        for c in self.mock_run.call_args_list:
            self.assertIn("--version", str(c))


if __name__ == "__main__":
    unittest.main()
