import base64
import contextlib
import errno
import fcntl
import hashlib
import http.client
import math
import json
import os
import pathlib
import posixpath
import re
import shutil
import socket
import stat
import subprocess
import sys
import tarfile
import tempfile
import time
import uuid


class DeploymentError(RuntimeError):
    pass


def run(argv, timeout=60):
    try:
        proc = subprocess.run(
            argv,
            shell=False,
            text=True,
            capture_output=True,
            check=False,
            timeout=timeout,
        )
    except subprocess.TimeoutExpired as exc:
        raise DeploymentError(f"command timed out: {argv!r}") from exc
    except OSError as exc:
        raise DeploymentError(f"failed to run {argv!r}: {exc}") from exc
    if proc.returncode != 0:
        stderr = (proc.stderr or "").strip()
        raise DeploymentError(f"command failed ({proc.returncode}): {argv!r}: {stderr}")
    return proc.stdout


def read_json(path):
    fd = os.open(str(path), os.O_RDONLY | os.O_NOFOLLOW)
    try:
        st = os.fstat(fd)
        if not stat.S_ISREG(st.st_mode):
            raise DeploymentError(f"not a regular file: {path}")
        if st.st_size > 1024 * 1024:
            raise DeploymentError(f"file too large: {path}")
        with os.fdopen(fd, "r", encoding="utf-8") as fh:
            fd = -1
            try:
                return json.load(fh)
            except json.JSONDecodeError as exc:
                raise DeploymentError(f"invalid JSON in {path}: {exc}") from exc
    finally:
        if fd >= 0:
            os.close(fd)


def write_state(path, value):
    path = pathlib.Path(path)
    parent = path.parent
    data = json.dumps(value, sort_keys=True, indent=2).encode("utf-8")
    tmp_name = f".{path.name}.tmp.{os.getpid()}.{uuid.uuid4().hex}"
    tmp_path = parent / tmp_name
    fd = os.open(
        str(tmp_path),
        os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW,
        0o600,
    )
    try:
        try:
            view = memoryview(data)
            try:
                while view:
                    written = os.write(fd, view)
                    if written <= 0:
                        raise DeploymentError("short write on state file")
                    view = view[written:]
            finally:
                view.release()
            os.fsync(fd)
        finally:
            os.close(fd)
        os.replace(str(tmp_path), str(path))
        pfd = os.open(str(parent), os.O_RDONLY | os.O_DIRECTORY)
        try:
            os.fsync(pfd)
        finally:
            os.close(pfd)
    except BaseException:
        try:
            os.unlink(str(tmp_path))
        except OSError:
            pass
        raise


def private_dir(path):
    path = pathlib.Path(path)
    try:
        st = os.lstat(str(path))
    except FileNotFoundError:
        os.mkdir(str(path), 0o700)
        st = os.lstat(str(path))
    if stat.S_ISLNK(st.st_mode):
        raise DeploymentError(f"symlink not allowed: {path}")
    if not stat.S_ISDIR(st.st_mode):
        raise DeploymentError(f"not a directory: {path}")
    if st.st_uid != os.geteuid():
        raise DeploymentError(f"wrong owner: {path}")
    if st.st_mode & 0o077:
        raise DeploymentError(f"insecure permissions: {path}")
    return path


@contextlib.contextmanager
def lock_file(path):
    path = str(path)
    fd = os.open(path, os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW, 0o600)
    try:
        st = os.fstat(fd)
        if not stat.S_ISREG(st.st_mode):
            raise DeploymentError(f"lock not regular: {path}")
        if st.st_uid != os.getuid():
            raise DeploymentError(f"lock wrong owner: {path}")
        try:
            fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except OSError as exc:
            if exc.errno in (errno.EACCES, errno.EAGAIN):
                raise DeploymentError(f"lock busy: {path}") from exc
            raise
        yield fd
    finally:
        os.close(fd)


def overlap(a, b):
    a = pathlib.Path(a)
    b = pathlib.Path(b)
    try:
        a_real = a.resolve(strict=False)
    except OSError:
        a_real = a
    try:
        b_real = b.resolve(strict=False)
    except OSError:
        b_real = b
    return a_real == b_real or a_real.is_relative_to(b_real) or b_real.is_relative_to(a_real)


_SERVICE_RE = re.compile(r"^[A-Za-z0-9_.@:-]+\.service$")


def resolve_config(request):
    cfg = request.get("config")
    if not isinstance(cfg, dict):
        raise DeploymentError("config must be a dict")
    operation_id = request.get("operation_id")
    if not isinstance(operation_id, str) or not re.fullmatch(r"[0-9a-f]{32}", operation_id):
        raise DeploymentError("operation_id must be 32 lowercase hexadecimal characters")

    readiness_timeout = request.get("readiness_timeout", 120)
    if isinstance(readiness_timeout, bool) or not isinstance(readiness_timeout, (int, float)):
        raise DeploymentError("readiness_timeout must be a number")
    if not math.isfinite(readiness_timeout) or not (1 <= readiness_timeout <= 600):
        raise DeploymentError("readiness_timeout out of range")
    install_raw = cfg.get("install_dir")
    if not isinstance(install_raw, str) or not os.path.isabs(install_raw):
        raise DeploymentError("install_dir must be absolute")
    install_dir = pathlib.Path(install_raw)
    if not install_dir.is_dir():
        raise DeploymentError("install_dir must be existing directory")
    install_dir = install_dir.resolve(strict=True)
    if install_dir.name in ("", ".", ".."):
        raise DeploymentError("invalid install_dir")

    roots_raw = cfg.get("data_roots")
    if not isinstance(roots_raw, list) or not roots_raw:
        raise DeploymentError("data_roots must be nonempty list")
    data_roots = []
    for r in roots_raw:
        if not isinstance(r, str) or not os.path.isabs(r):
            raise DeploymentError("data root must be absolute")
        p = pathlib.Path(r)
        if not p.is_dir():
            raise DeploymentError(f"data root missing: {r}")
        data_roots.append(p.resolve(strict=True))
    if len(set(data_roots)) != len(data_roots):
        raise DeploymentError("data_roots must be unique")

    platform = cfg.get("platform")
    if platform not in ("linux-amd64", "linux-arm64"):
        raise DeploymentError("invalid platform")

    services_raw = cfg.get("services")
    if not isinstance(services_raw, list) or not services_raw:
        raise DeploymentError("services must be nonempty list")
    services = []
    for s in services_raw:
        if not isinstance(s, str) or not _SERVICE_RE.match(s):
            raise DeploymentError(f"invalid service: {s!r}")
        if s.startswith("-"):
            raise DeploymentError(f"invalid service: {s!r}")
        services.append(s)
    if len(set(services)) != len(services):
        raise DeploymentError("services must be unique")

    scope = cfg.get("systemd_scope")
    if scope not in ("user", "system"):
        raise DeploymentError("invalid systemd_scope")

    key = cfg.get("update_public_key")
    if not isinstance(key, str):
        raise DeploymentError("update_public_key missing")
    try:
        key_bytes = base64.b64decode(key, validate=True)
    except (ValueError, base64.binascii.Error) as exc:
        raise DeploymentError("update_public_key invalid base64") from exc
    if len(key_bytes) != 32:
        raise DeploymentError("update_public_key must be 32 bytes")

    backup_dir = None
    if "backup_dir" in cfg and cfg.get("backup_dir") is not None:
        bd = cfg["backup_dir"]
        if not isinstance(bd, str) or not os.path.isabs(bd):
            raise DeploymentError("backup_dir must be absolute")
        backup_dir = pathlib.Path(bd).resolve(strict=False)

    prefix = cfg.get("backup_prefix", [])
    if prefix is None:
        prefix = []
    if not isinstance(prefix, list):
        raise DeploymentError("backup_prefix must be a list")
    for i, el in enumerate(prefix):
        if not isinstance(el, str) or not el:
            raise DeploymentError("backup_prefix elements must be nonempty strings")
        if i == 0:
            if not os.path.isabs(el):
                if "/" in el or el in ("", ".", ".."):
                    raise DeploymentError("backup_prefix[0] must be absolute path or name")
    cc = {}
    cc["install_dir"] = install_dir
    cc["data_roots"] = data_roots
    cc["platform"] = platform
    cc["services"] = services
    cc["systemd_scope"] = scope
    cc["update_public_key"] = key
    cc["backup_dir"] = backup_dir
    cc["backup_prefix"] = list(prefix)
    cc["_request"] = request

    updates = install_dir.parent / (install_dir.name + ".updates")
    deploy = install_dir.parent / (install_dir.name + ".deploy")

    for a in range(len(data_roots)):
        for b in range(a + 1, len(data_roots)):
            if overlap(data_roots[a], data_roots[b]):
                raise DeploymentError("data roots overlap")
    for r in data_roots:
        for forbidden in (install_dir, updates, deploy):
            if overlap(r, forbidden):
                raise DeploymentError("data root overlaps install path")
        if backup_dir is not None and overlap(r, backup_dir):
            raise DeploymentError("data root overlaps backup_dir")
    if backup_dir is not None:
        for forbidden in (install_dir, updates, deploy):
            if overlap(backup_dir, forbidden):
                raise DeploymentError("backup_dir overlaps install path")
    return cc


def extract_package(request, config):
    stage = None
    try:
        archive_size = request.get("archive_size")
        if not isinstance(archive_size, int) or isinstance(archive_size, bool):
            raise DeploymentError("archive_size invalid")
        if archive_size <= 0 or archive_size > 2 * 1024 * 1024 * 1024:
            raise DeploymentError("archive_size out of range")
        sha = request.get("sha256")
        if not isinstance(sha, str) or not re.fullmatch(r"[0-9a-f]{64}", sha):
            raise DeploymentError("sha256 invalid")
        rev = request.get("revision")
        if not isinstance(rev, str) or not re.fullmatch(r"[0-9a-f]{40}", rev):
            raise DeploymentError("revision invalid")
        archive_path = request.get("archive")
        if not isinstance(archive_path, str) or not os.path.isabs(archive_path):
            raise DeploymentError("archive_path must be absolute")

        install_dir = config["install_dir"]
        stage = pathlib.Path(
            tempfile.mkdtemp(
                prefix="." + install_dir.name + ".deploy-stage-",
                dir=str(install_dir.parent),
            )
        )
        os.chmod(str(stage), 0o700)
        for r in config["data_roots"]:
            if overlap(stage, r):
                raise DeploymentError("stage overlaps data root")
        if config["backup_dir"] is not None and overlap(stage, config["backup_dir"]):
            raise DeploymentError("stage overlaps backup_dir")

        fd = os.open(str(archive_path), os.O_RDONLY | os.O_NOFOLLOW)
        try:
            st = os.fstat(fd)
            if not stat.S_ISREG(st.st_mode):
                raise DeploymentError("archive not regular file")
            if st.st_size != archive_size:
                raise DeploymentError("archive size mismatch")
            hasher = hashlib.sha256()
            remaining = archive_size
            while remaining > 0:
                chunk = os.read(fd, min(1024 * 1024, remaining))
                if not chunk:
                    raise DeploymentError("short read on archive")
                hasher.update(chunk)
                remaining -= len(chunk)
            if hasher.hexdigest() != sha:
                raise DeploymentError("archive sha256 mismatch")
            os.lseek(fd, 0, os.SEEK_SET)
            fileobj = os.fdopen(os.dup(fd), "rb")
        finally:
            os.close(fd)

        with fileobj:
            try:
                archive = tarfile.open(fileobj=fileobj, mode="r:*")
            except tarfile.TarError as exc:
                raise DeploymentError(f"invalid archive: {exc}") from exc
            with archive:
                members = []
                for m in archive:
                    if len(members) >= 100000:
                        raise DeploymentError("too many archive entries")
                    members.append(m)
                total = 0
                topdir = None
                seen = set()
                for m in members:
                    name = m.name
                    if not name or name.startswith("/") or "\\" in name:
                        raise DeploymentError(f"invalid member name: {name!r}")
                    parts = name.split("/")
                    if any(p in ("..", "") and i != len(parts) - 1 for i, p in enumerate(parts)):
                        raise DeploymentError(f"invalid member path: {name!r}")
                    if ".." in parts:
                        raise DeploymentError(f"invalid member path: {name!r}")
                    norm = posixpath.normpath(name)
                    if norm in seen:
                        raise DeploymentError(f"duplicate member: {norm}")
                    seen.add(norm)
                    if topdir is None:
                        topdir = parts[0]
                    elif parts[0] != topdir:
                        raise DeploymentError("multiple top directories")
                    if m.isdir():
                        pass
                    elif m.issym():
                        if posixpath.isabs(m.linkname):
                            raise DeploymentError("absolute symlink")
                        base = posixpath.dirname(name)
                        target = posixpath.normpath(posixpath.join(base, m.linkname))
                        if target == ".." or target.startswith("../"):
                            raise DeploymentError("symlink escapes top dir")
                        if not (target == topdir or target.startswith(topdir + "/")):
                            raise DeploymentError("symlink escapes top dir")
                    elif m.isreg():
                        if m.size > 1024 * 1024 * 1024:
                            raise DeploymentError("member too large")
                        total += m.size
                        if total > 2 * 1024 * 1024 * 1024:
                            raise DeploymentError("expanded size too large")
                    else:
                        raise DeploymentError(f"unsupported member type: {name}")
                if topdir is None:
                    raise DeploymentError("empty archive")
                if posixpath.normpath(topdir) in ("", "."):
                    raise DeploymentError("invalid topdir")
                for m in members:
                    if m.name == topdir and m.issym():
                        raise DeploymentError("topdir cannot be symlink")
                symlink_paths = set()
                for m in members:
                    if m.issym():
                        symlink_paths.add(posixpath.normpath(m.name))
                if symlink_paths:
                    for m in members:
                        if m.isreg() and m.size < 0:
                            raise DeploymentError(f"negative member size: {m.name!r}")
                        parent = posixpath.dirname(posixpath.normpath(m.name))
                        while parent and parent not in (".", ""):
                            if parent in symlink_paths:
                                raise DeploymentError(f"member beneath symlink: {m.name!r}")
                            parent = posixpath.dirname(parent)
                    for m in members:
                        if not m.isreg() and m.size < 0:
                            raise DeploymentError(f"negative member size: {m.name!r}")
                else:
                    for m in members:
                        if m.isreg() and m.size < 0:
                            raise DeploymentError(f"negative member size: {m.name!r}")
                try:
                    archive.extractall(str(stage), filter="data")
                except (tarfile.TarError, OSError) as exc:
                    raise DeploymentError(f"extract failed: {exc}") from exc

        candidate = stage / topdir
        if not candidate.is_dir():
            raise DeploymentError("topdir missing after extract")
        bin_path = candidate / "bin" / "pot"
        try:
            bst = os.lstat(str(bin_path))
        except OSError as exc:
            raise DeploymentError("bin/pot missing") from exc
        if not stat.S_ISREG(bst.st_mode):
            raise DeploymentError("bin/pot not regular")
        if not (bst.st_mode & 0o111):
            raise DeploymentError("bin/pot not executable")

        res = candidate / "Resources"
        rj = res / "update.json"
        for p in (res,):
            st2 = os.lstat(str(p))
            if not stat.S_ISDIR(st2.st_mode):
                raise DeploymentError(f"{p} not directory")
        st3 = os.lstat(str(rj))
        if not stat.S_ISREG(st3.st_mode):
            raise DeploymentError("update.json not regular")
        metadata = read_json(rj)
        if not isinstance(metadata, dict):
            raise DeploymentError("metadata not a dict")
        if metadata.get("kind") != "cli":
            raise DeploymentError("metadata kind mismatch")
        if metadata.get("protocol") != 2:
            raise DeploymentError("metadata protocol mismatch")
        if metadata.get("platform") != config["platform"]:
            raise DeploymentError("metadata platform mismatch")
        mv = metadata.get("version")
        ms = metadata.get("storage_format")
        if not isinstance(mv, str) or not mv:
            raise DeploymentError("metadata version missing")
        if not isinstance(ms, str) or not ms:
            raise DeploymentError("metadata storage_format missing")

        expected = f"pot {mv} ({rev})"
        actual = run([str(bin_path), "--version"]).strip()
        if actual != expected:
            raise DeploymentError(f"version mismatch: expected {expected!r} got {actual!r}")
        return stage, candidate, metadata
    except BaseException:
        if stage is not None:
            shutil.rmtree(str(stage), ignore_errors=True)
        raise


def systemctl(config, *args):
    argv = ["systemctl"]
    if config.get("systemd_scope") == "user":
        argv.append("--user")
    argv.extend(list(args))
    return run(argv, timeout=60)


def service_info(config, unit):
    out = systemctl(config, "show", "--property=ActiveState,SubState,MainPID", "--", unit)
    fields = {}
    for line in out.splitlines():
        if "=" in line:
            k, v = line.split("=", 1)
            fields[k.strip()] = v.strip()
    for k in ("ActiveState", "SubState", "MainPID"):
        if k not in fields:
            raise DeploymentError(f"systemctl show missing {k} for {unit}")
    try:
        pid = int(fields["MainPID"])
    except ValueError:
        raise DeploymentError(f"invalid MainPID for {unit}: {fields['MainPID']!r}")
    return {"active": fields["ActiveState"], "sub": fields["SubState"], "pid": pid}


def executable_matches(pid, binary):
    try:
        exe_link = os.readlink(f"/proc/{int(pid)}/exe")
    except OSError:
        return False
    if exe_link.endswith(" (deleted)"):
        return False
    try:
        a = os.stat(f"/proc/{int(pid)}/exe")
        b = os.stat(str(binary))
    except OSError:
        return False
    return a.st_dev == b.st_dev and a.st_ino == b.st_ino


class _UnixHTTPConnection(http.client.HTTPConnection):
    def __init__(self, path, timeout=3):
        super().__init__("localhost", timeout=timeout)
        self._unix_path = path

    def connect(self):
        s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        s.settimeout(self.timeout)
        try:
            s.connect(self._unix_path)
        except BaseException:
            s.close()
            raise
        self.sock = s


def unix_status(path):
    conn = _UnixHTTPConnection(str(path))
    try:
        conn.request("GET", "/status")
        resp = conn.getresponse()
        if resp.status != 200:
            raise DeploymentError(f"status non-200 from {path}: {resp.status}")
        data = resp.read(1024 * 1024 + 1)
        if len(data) > 1024 * 1024:
            raise DeploymentError(f"status payload too large from {path}")
        try:
            obj = json.loads(data.decode("utf-8"))
        except (ValueError, UnicodeDecodeError) as exc:
            raise DeploymentError(f"bad status JSON from {path}: {exc}")
        if not isinstance(obj, dict):
            raise DeploymentError(f"status not object from {path}")
        return obj
    finally:
        try:
            conn.close()
        except Exception:
            pass


_PID_RE = re.compile(r"^host-([1-9][0-9]*)\.json$")


def participants(config):
    install = pathlib.Path(config["install_dir"])
    updates = install.parent / (install.name + ".updates")
    result = {}
    if not updates.exists():
        return result
    names = []
    for entry in os.listdir(str(updates)):
        if entry.startswith("host-") and entry.endswith(".json"):
            if not _PID_RE.match(entry):
                raise DeploymentError(f"malformed registration filename: {entry}")
            names.append(entry)
    names.sort()
    for name in names:
        pid = int(_PID_RE.match(name).group(1))
        reg = read_json(str(updates / name))
        if not isinstance(reg, dict):
            raise DeploymentError(f"registration not object: {name}")
        sock = reg.get("socket")
        if not isinstance(sock, str) or not os.path.isabs(sock):
            raise DeploymentError(f"bad socket in registration {name}")
        try:
            status = unix_status(sock)
        except (FileNotFoundError, ConnectionRefusedError):
            continue
        root = status.get("root")
        if not isinstance(root, str) or not root or not os.path.isabs(root):
            raise DeploymentError(f"bad live root from {pid}")
        try:
            resolved = pathlib.Path(root).resolve(strict=True)
        except OSError as exc:
            raise DeploymentError(f"bad live root from {pid}: {exc}")
        if resolved not in config["data_roots"]:
            raise DeploymentError(f"unknown root from {pid}: {root}")
        if status.get("socket") != sock:
            raise DeploymentError(f"socket mismatch for {pid}")
        reg_root = reg.get("root")
        if not isinstance(reg_root, str) or not reg_root or not os.path.isabs(reg_root):
            raise DeploymentError(f"bad registration root for {pid}")
        try:
            reg_resolved = pathlib.Path(reg_root).resolve(strict=True)
        except OSError as exc:
            raise DeploymentError(f"bad registration root for {pid}: {exc}")
        if reg_resolved != resolved:
            raise DeploymentError(f"root mismatch for {pid}")
        if status.get("ready") is not True:
            raise DeploymentError(f"not ready: {pid}")
        if status.get("unsupported") is True:
            raise DeploymentError(f"unsupported: {pid}")
        if status.get("protocol") != 2:
            raise DeploymentError(f"bad protocol: {pid}")
        boot_id = status.get("boot_id")
        version = status.get("version")
        if not isinstance(boot_id, str) or not boot_id:
            raise DeploymentError(f"missing boot_id: {pid}")
        if not isinstance(version, str) or not version:
            raise DeploymentError(f"missing version: {pid}")
        work = status.get("work")
        if not isinstance(work, dict):
            raise DeploymentError(f"missing work: {pid}")
        for k in ("tasks", "replies", "sessions"):
            v = work.get(k)
            if not isinstance(v, int) or isinstance(v, bool) or v < 0:
                raise DeploymentError(f"bad work.{k}: {pid}")
        result[pid] = status
    return result


def inspect_services(config, metadata, previous=None, allow_work=False):
    units = list(config["services"])
    info = {}
    pids = []
    roots = set()
    version = metadata["version"]
    binary = pathlib.Path(config["install_dir"]) / "bin" / "pot"
    for unit in units:
        s = service_info(config, unit)
        if s["active"] != "active":
            raise DeploymentError(f"{unit} not active: {s['active']}/{s['sub']}")
        if s["pid"] <= 0:
            raise DeploymentError(f"{unit} no pid")
        if not executable_matches(s["pid"], binary):
            raise DeploymentError(f"{unit} pid {s['pid']} executable mismatch")
        pids.append(s["pid"])
        info[unit] = s
    if len(set(pids)) != len(pids):
        raise DeploymentError("duplicate service pids")
    live = participants(config)
    if set(live.keys()) != set(pids):
        raise DeploymentError(f"participant pid set mismatch: {sorted(live)} vs {sorted(pids)}")
    configured_roots = {os.path.realpath(str(r)) for r in config["data_roots"]}
    result = {}
    for unit in units:
        pid = info[unit]["pid"]
        st = live[pid]
        if not pathlib.Path(st["root"]).is_absolute():
            raise DeploymentError(f"non-absolute root: {pid}")
        roots.add(os.path.realpath(st["root"]))
        if st["version"] != version:
            raise DeploymentError(f"version mismatch for {pid}: {st['version']!r} != {version!r}")
        work = st["work"]
        if not allow_work:
            for k in ("tasks", "replies", "sessions"):
                if work[k] != 0:
                    raise DeploymentError(f"work.{k} nonzero for {pid}")
        if previous is not None:
            prev = previous.get(unit)
            if not isinstance(prev, dict):
                raise DeploymentError(f"missing previous for {unit}")
            prev_root = prev.get("root")
            if os.path.realpath(str(prev_root)) != os.path.realpath(st["root"]):
                raise DeploymentError(f"root changed for {unit}")
            prev_boot = prev.get("boot_id")
            if not isinstance(prev_boot, str) or not prev_boot:
                raise DeploymentError(f"previous boot_id missing for {unit}")
            if st["boot_id"] == prev_boot:
                raise DeploymentError(f"boot_id unchanged for {unit}")
        result[unit] = {
            "pid": pid,
            "root": str(st["root"]),
            "boot_id": st["boot_id"],
            "version": st["version"],
            "socket": st["socket"],
        }
    if roots != configured_roots:
        raise DeploymentError(f"live roots mismatch: {sorted(roots)} vs {sorted(configured_roots)}")
    return result


def wait_ready(config, metadata, previous, timeout):
    deadline = time.monotonic() + float(timeout)
    last = None
    while True:
        try:
            return inspect_services(config, metadata, previous=previous, allow_work=True)
        except (DeploymentError, OSError, ValueError, TypeError) as exc:
            last = exc
        if time.monotonic() >= deadline:
            raise DeploymentError(f"wait_ready timeout: {last}")
        time.sleep(0.25)


def stop_services(config):
    units = list(config["services"])
    errors = []
    for unit in units:
        try:
            systemctl(config, "stop", "--", unit)
        except Exception as exc:
            errors.append(f"stop {unit}: {exc}")
    for unit in units:
        try:
            s = service_info(config, unit)
        except Exception as exc:
            errors.append(f"info {unit}: {exc}")
            continue
        if s["pid"] != 0:
            errors.append(f"{unit} still pid {s['pid']}")
        if s["active"] not in ("inactive", "failed"):
            errors.append(f"{unit} still {s['active']}/{s['sub']}")
    if errors:
        raise DeploymentError("stop_services: " + "; ".join(errors))


def start_services(config):
    units = list(reversed(list(config["services"])))
    for unit in units:
        systemctl(config, "start", "--", unit)


def backup_roots(config, deployment_id):
    backup_dir = config.get("backup_dir")
    if not backup_dir:
        raise DeploymentError("backup_dir required")
    private_dir(str(backup_dir))
    dest = pathlib.Path(str(backup_dir)) / f"{deployment_id}.tar.gz"
    if dest.exists():
        raise DeploymentError(f"backup destination exists: {dest}")
    manifest_path = dest.with_name(dest.name + ".json")
    if manifest_path.exists():
        raise DeploymentError(f"manifest exists: {manifest_path}")
    roots = [str(r) for r in config["data_roots"]]
    try:
        prefix = config.get("backup_prefix") or []
        if prefix:
            argv = (
                list(prefix)
                + [
                    "tar",
                    "--create",
                    "--gzip",
                    "--file",
                    str(dest),
                    "--directory",
                    "/",
                    "--",
                ]
                + [str(r).lstrip("/") for r in roots]
            )
            run(argv, timeout=3600)
        else:
            tf = tarfile.open(str(dest), mode="x:gz", dereference=False)
            try:
                for r in roots:
                    arc = pathlib.Path(r).as_posix().lstrip("/")
                    tf.add(r, arcname=arc, recursive=True)
            finally:
                tf.close()
        try:
            fd = os.open(str(dest), os.O_RDWR)
            try:
                os.fsync(fd)
            finally:
                os.close(fd)
        except OSError as exc:
            raise DeploymentError(f"fsync failed: {exc}")
        os.chmod(str(dest), 0o600)
        size = dest.stat().st_size
        if size <= 0:
            raise DeploymentError("empty archive")
        h = hashlib.sha256()
        with open(str(dest), "rb") as f:
            for chunk in iter(lambda: f.read(1024 * 1024), b""):
                h.update(chunk)
        sha = h.hexdigest()
        try:
            with tarfile.open(str(dest), mode="r:gz") as tf:
                for _ in tf:
                    pass
        except (tarfile.TarError, OSError) as exc:
            raise DeploymentError(f"archive verify failed: {exc}")
        write_state(
            str(manifest_path),
            {
                "roots": roots,
                "sha256": sha,
                "size": size,
            },
        )
        return str(dest)
    except BaseException:
        try:
            if dest.exists():
                os.unlink(str(dest))
        except OSError:
            pass
        try:
            if manifest_path.exists():
                os.unlink(str(manifest_path))
        except OSError:
            pass
        raise


from pathlib import Path

MAX_INPUT = 1024 * 1024
PERMITTED_OWN_PHASES = {"completed", "rolled_back"}
PERMITTED_UPDATER_PHASES = {"current", "available", "failed"}


def _check_own_state(path: Path):
    if not path.exists():
        return None
    try:
        st = read_json(path)
    except Exception as e:
        raise DeploymentError(
            f"existing deploy journal {path} unreadable ({e}); manual operator inspection required"
        )
    phase = st.get("phase") if isinstance(st, dict) else None
    if phase not in PERMITTED_OWN_PHASES:
        raise DeploymentError(
            f"existing deploy journal phase={phase!r} at {path}; "
            "nonterminal state present — manual operator inspection required"
        )
    return st


def _check_updater_state(path: Path, locked: bool):
    if not path.exists():
        return
    try:
        st = read_json(path)
    except Exception as e:
        raise DeploymentError(
            f"updater state {path} unreadable ({e}); manual operator inspection required"
        )
    phase = st.get("phase") if isinstance(st, dict) else None
    if phase not in PERMITTED_UPDATER_PHASES:
        raise DeploymentError(
            f"updater state phase={phase!r} at {path}; manual operator inspection required"
        )
    if phase == "failed" and not locked:
        raise DeploymentError("updater state phase 'failed' requires update.lock held; refusing")


def _check_launch_preconditions():
    if not sys.platform.startswith("linux"):
        raise DeploymentError("deploy requires Linux")
    if sys.version_info < (3, 12):
        raise DeploymentError("deploy requires Python >= 3.12")


def _validate_current_install(config):
    install = config["install_dir"]
    update_json = install / "Resources" / "update.json"
    if not update_json.is_file():
        raise DeploymentError(f"current install missing {update_json}")
    meta = read_json(update_json)
    if meta.get("protocol") != 2 or meta.get("kind") != "cli":
        raise DeploymentError("current install update.json protocol/kind mismatch")
    plat = meta.get("platform")
    if plat != config.get("platform"):
        raise DeploymentError(
            f"current install platform {plat!r} != config {config.get('platform')!r}"
        )
    ver = meta.get("version")
    if not isinstance(ver, str) or not ver:
        raise DeploymentError("current install update.json version missing/empty")
    if not isinstance(meta.get("storage_format"), str) or not meta.get("storage_format"):
        raise DeploymentError("current install update.json storage_format missing")
    binp = install / "bin" / "pot"
    try:
        st = binp.lstat()
    except FileNotFoundError:
        raise DeploymentError(f"current install {binp} missing")
    import stat as _stat

    if not _stat.S_ISREG(st.st_mode) or binp.is_symlink():
        raise DeploymentError(f"current {binp} must be regular non-symlink executable")
    if not os.access(binp, os.X_OK):
        raise DeploymentError(f"current {binp} not executable")
    return meta


def prepare_services(config, hosts):
    for unit in config["services"]:
        conn = _UnixHTTPConnection(hosts[unit]["socket"], timeout=120)
        try:
            conn.request("POST", "/prepare")
            resp = conn.getresponse()
            body = resp.read(1024 * 1024 + 1)
            if len(body) > 1024 * 1024:
                raise DeploymentError("prepare response too large for %s" % unit)
            if resp.status != 200:
                raise DeploymentError("prepare returned %s for %s" % (resp.status, unit))
            try:
                ack = json.loads(body.decode("utf-8"))
            except (ValueError, UnicodeDecodeError) as e:
                raise DeploymentError("malformed prepare ack for %s: %s" % (unit, e))
            if not isinstance(ack, dict) or ack.get("ready") is not True:
                raise DeploymentError("prepare not ready for %s: %r" % (unit, ack))
        finally:
            conn.close()


def _deploy_transaction(
    request, config, current_meta, metadata, stage, candidate, initial_hosts, journal, state_path
):
    install = config["install_dir"]
    stopped_locks = contextlib.ExitStack()
    try:
        try:
            journal["phase"] = "stopping"
            write_state(state_path, journal)
            prepare_services(config, initial_hosts)
            stop_services(config)
            stopped_locks.enter_context(
                lock_file(Path(str(install) + ".updates") / "installation.lock")
            )
            for root in sorted(config["data_roots"]):
                stopped_locks.enter_context(lock_file(Path(root) / "runtime.lock"))
                stopped_locks.enter_context(lock_file(Path(root) / "web-ui-server.lock"))
            if journal.get("storage_changed"):
                journal["backup"] = backup_roots(config, journal["id"])
                journal["phase"] = "backed_up"
                write_state(state_path, journal)
            journal["phase"] = "replacing"
            write_state(state_path, journal)
            os.rename(install, journal["old_package"])
            os.rename(candidate, install)
            parent_fd = os.open(str(install.parent), os.O_RDONLY | os.O_DIRECTORY)
            try:
                os.fsync(parent_fd)
            finally:
                os.close(parent_fd)
            journal["phase"] = "starting"
            journal["first_start_attempted"] = True
            write_state(state_path, journal)
            stopped_locks.close()
            start_services(config)
            hosts = wait_ready(
                config, metadata, initial_hosts, timeout=request.get("readiness_timeout", 120)
            )
            journal["phase"] = "completed"
            journal["hosts"] = hosts
            journal["time"] = time.time()
            write_state(state_path, journal)
            return {
                "ok": True,
                "revision": request["revision"],
                "installation": str(install),
                "backup": journal.get("backup"),
                "old_package": journal["old_package"],
                "services": hosts,
            }
        except BaseException as exc:
            err = str(exc)
            if journal.get("first_start_attempted"):
                stopped_locks.close()
                cleanup_error = None
                try:
                    stop_services(config)
                except BaseException as cexc:
                    cleanup_error = str(cexc)
                journal["phase"] = "blocked"
                journal["error"] = err
                if cleanup_error is not None:
                    journal["cleanup_error"] = cleanup_error
                try:
                    write_state(state_path, journal)
                except BaseException as wexc:
                    raise DeploymentError(
                        f"manual inspection required; retained target; "
                        f"state write failed after: {err}; state error: {wexc}"
                    )
                raise DeploymentError(f"manual inspection required; retained target; error: {err}")
            stop_error = None
            try:
                stop_services(config)
            except BaseException as sexc:
                stop_error = str(sexc)
            if stop_error is not None:
                journal["phase"] = "blocked"
                journal["error"] = err
                journal["recovery_error"] = stop_error
                try:
                    write_state(state_path, journal)
                except BaseException as wexc:
                    raise DeploymentError(
                        f"manual inspection required; stop failed: {stop_error}; "
                        f"original: {err}; state error: {wexc}"
                    )
                raise DeploymentError(
                    f"manual inspection required; stop failed: {stop_error}; original: {err}"
                )
            recovery_error = None
            try:
                old = Path(journal["old_package"])
                if old.exists():
                    if install.exists():
                        os.rename(install, stage / "failed-package")
                    os.rename(old, install)
                    parent_fd = os.open(str(install.parent), os.O_RDONLY | os.O_DIRECTORY)
                    try:
                        os.fsync(parent_fd)
                    finally:
                        os.close(parent_fd)
                stopped_locks.close()
                start_services(config)
                wait_ready(
                    config, current_meta, None, timeout=request.get("readiness_timeout", 120)
                )
            except BaseException as rexc:
                recovery_error = str(rexc)
            if recovery_error is not None:
                try:
                    stop_services(config)
                except BaseException:
                    pass
                journal["phase"] = "blocked"
                journal["error"] = err
                journal["recovery_error"] = recovery_error
                try:
                    write_state(state_path, journal)
                except BaseException as wexc:
                    raise DeploymentError(
                        f"manual inspection required; recovery failed: "
                        f"{recovery_error}; original: {err}; state error: {wexc}"
                    )
                raise DeploymentError(
                    f"manual inspection required; recovery failed: "
                    f"{recovery_error}; original: {err}"
                )
            journal["phase"] = "rolled_back"
            journal["error"] = err
            try:
                write_state(state_path, journal)
            except BaseException as wexc:
                try:
                    stop_services(config)
                except BaseException:
                    pass
                journal["phase"] = "blocked"
                journal["error"] = err
                journal["recovery_error"] = str(wexc)
                try:
                    write_state(state_path, journal)
                except BaseException as wexc2:
                    raise DeploymentError(
                        f"manual inspection required; original: {err}; "
                        f"state: {wexc}; additionally: {wexc2}"
                    )
                raise DeploymentError(f"manual inspection required; original: {err}; state: {wexc}")
            raise DeploymentError(f"deployment failed and rolled back: {err}")
    finally:
        stopped_locks.close()


def deploy(request):
    from contextlib import ExitStack

    _check_launch_preconditions()
    config = resolve_config(request)
    install = Path(config["install_dir"])

    deploy_dir = private_dir(install.parent / (install.name + ".deploy"))
    updates_dir = private_dir(install.parent / (install.name + ".updates"))

    stage = None
    journal = None
    with ExitStack() as stack:
        stack.enter_context(lock_file(deploy_dir / "deploy.lock"))
        stack.enter_context(lock_file(updates_dir / "update.lock"))
        _check_own_state(deploy_dir / "state.json")
        _check_updater_state(updates_dir / "state.json", locked=True)

        current_meta = _validate_current_install(config)

        try:
            (stage, candidate, metadata) = extract_package(request, config)

            storage_changed = metadata["storage_format"] != current_meta["storage_format"]
            backup_dir = config.get("backup_dir")
            if storage_changed and not backup_dir:
                raise DeploymentError("storage format changed but backup_dir is not configured")

            deploy_id = uuid.uuid4().hex
            old_package = install.parent / ("." + install.name + ".deploy-old-" + deploy_id)
            if old_package.exists() or old_package.is_symlink():
                raise DeploymentError("deploy-old path already exists: %s" % (old_package,))

            roots = [Path(r) for r in config["data_roots"]]
            if backup_dir:
                roots.append(Path(backup_dir))
            for root in roots:
                if overlap(old_package, root) or overlap(stage, root):
                    raise DeploymentError("deploy path overlaps configured root: %s" % (root,))

            inspect_services(config, current_meta, allow_work=True)
            initial_hosts = inspect_services(config, current_meta, allow_work=True)

            journal = {
                "phase": "stopping",
                "id": deploy_id,
                "operation_id": request["operation_id"],
                "install": str(install),
                "old_package": str(old_package),
                "stage": str(stage),
                "revision": request["revision"],
                "archive_sha256": request["sha256"],
                "storage_changed": storage_changed,
                "backup": None,
                "first_start_attempted": False,
            }

            return _deploy_transaction(
                request,
                config,
                current_meta,
                metadata,
                stage,
                candidate,
                initial_hosts,
                journal,
                deploy_dir / "state.json",
            )
        finally:
            if stage is not None and stage.exists():
                if journal is None or journal.get("phase") in (
                    "completed",
                    "rolled_back",
                ):
                    import shutil

                    shutil.rmtree(stage, ignore_errors=True)


def main():
    try:
        raw = sys.stdin.buffer.read(MAX_INPUT + 1)
    except BaseException as e:
        print(json.dumps({"ok": False, "error": f"stdin read failed: {e}"}))
        return 1
    if len(raw) > MAX_INPUT:
        print(json.dumps({"ok": False, "error": "input exceeds 1MiB limit"}))
        return 1
    try:
        text = raw.decode("utf-8")
    except UnicodeDecodeError:
        print(json.dumps({"ok": False, "error": "input not UTF-8"}))
        return 1
    try:
        req = json.loads(text)
    except json.JSONDecodeError as e:
        print(json.dumps({"ok": False, "error": f"invalid JSON: {e}"}))
        return 1
    if not isinstance(req, dict):
        print(json.dumps({"ok": False, "error": "request must be JSON object"}))
        return 1
    try:
        result = deploy(req)
    except BaseException as e:
        print(json.dumps({"ok": False, "error": str(e)}))
        return 1
    print(json.dumps(result))
    return 0


if __name__ == "__main__":
    sys.exit(main())
