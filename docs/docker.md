# Linux server with Docker

The server image runs a real local `pot serve` Runtime and an independent
`web-ui-server` gateway. It serves the same embedded Web UI as the desktop App.
The gateway authenticates to the Runtime over its private Unix socket; browsers
use the existing access-token login and server-managed sessions.

```text
browser -> loopback port / HTTPS reverse proxy -> :17703 web-ui-server
                                                    |
                                            private Unix socket
                                                    |
                                                pot serve
                                                    |
                               Protected SRT / approved container execution

/home/pot volume: identity + databases + credentials + sessions + workspaces
/opt/pot:         immutable CLI + Node + SRT + plugins + Skills + Web UI
```

## Build and start

Use a Linux Docker Engine with Buildx and a checkout containing this Dockerfile.
Build locally; these commands do not publish an image:

```sh
docker buildx build --platform linux/amd64 --load \
  --build-arg POT_APP_VERSION=0.0.0 \
  --build-arg POT_BUILD_REVISION="$(git rev-parse HEAD)" \
  -t pot-server:local .
docker compose -f docker/compose.yaml up -d --no-build
docker compose -f docker/compose.yaml exec pot \
  pot access -browser-root /home/pot/.pot-web token
```

On an arm64 server, replace `linux/amd64` with `linux/arm64`. The Dockerfile
cross-builds the CGO-free Go CLI and packages the corresponding checksum-pinned
Node distribution. It accepts `POT_APP_VERSION`, `POT_BUILD_REVISION` and
`POT_UPDATE_PUBLIC_KEY` (the public Ed25519 update verification key, never a
private signing key). Go and build-host Node/Python image versions must match
`mise.toml`; target Node versions/checksums come from `scripts/pot_packaging.py`.
Both architectures are part of the build contract. Producing a multiarch OCI
archive locally requires a builder supporting multi-platform output:

```sh
docker buildx build --platform linux/amd64,linux/arm64 \
  --build-arg POT_APP_VERSION=0.0.0 \
  --build-arg POT_BUILD_REVISION="$(git rev-parse HEAD)" \
  --output type=oci,dest=pot-server.oci.tar .
```

The image includes Bash, CA certificates, Git, Python, ripgrep, bubblewrap, socat
and tini, plus the packaged Node/npm/npx, SRT, plugins, Skills and frontend.
It excludes compilers, Node development headers, browser binaries and Docker
clients. To use computer-use, build a derived image with Chromium and its
dependencies; the browser must still work under the selected execution policy.
Project-specific toolchains should
also be installed in a derived image or the appropriate workspace.

Open `http://localhost:17703` and enter the token locally. For a remote server,
forward the loopback listener to your computer:

```sh
ssh -N -L 17703:127.0.0.1:17703 user@server
```

The default exact public origin is `http://localhost:17703`. Use that hostname in
the browser. A different hostname, protocol or port needs a matching
`POT_WEB_ORIGIN` and container recreation. Tokens are not printed in startup logs.
Do not place them in a public URL, Compose file or image layer.

Equivalent installation without Compose:

```sh
docker volume create pot-home
docker run -d --name pot --restart unless-stopped --stop-timeout 45 \
  -p 127.0.0.1:17703:17703 \
  -e POT_WEB_ORIGIN=http://localhost:17703 \
  --mount type=volume,src=pot-home,dst=/home/pot pot-server:local
docker exec pot pot access -browser-root /home/pot/.pot-web token
```

## Isolation and supported execution modes

New nodes remain **Protected** (`execution_access: ask`). The image never changes
machine policy, retries an execution outside SRT, or grants container privileges.
All local `exec` operations, including explicitly approved host operations, run
**inside the container**, using its files, installed tools and container network.
They do not execute on the Linux server outside Docker.

Run the real packaged launcher probe on the intended host:

```sh
docker compose -f docker/compose.yaml exec pot pot-sandbox-check
```

Exit code `0` proves a workspace write, private-file denial and denial of a real
visible Unix socket. Exit code `2` means the host/container denied Linux
namespaces and no workload ran. Other errors exit `1` and require investigation;
they are not a supported namespace refusal. The probe uses synthetic temporary
files and no model or credentials.

| Host capability | Supported behavior |
|---|---|
| Unprivileged user/mount/PID namespaces and nested seccomp are available | Local Protected execution; require a passing probe on that host |
| Docker seccomp, AppArmor, user namespace policy or the host kernel blocks namespace creation | Browser login, configuration, chat without local execution and Agent Links remain available; local Protected tools fail closed |
| Owner explicitly selects Trusted in Machines or approves a permitted host exception | Execution uses container account privileges, following the existing permission model; this is not SRT qualification |

Docker's default seccomp profile and some Ubuntu AppArmor policies block the
namespaces bubblewrap needs. Rootless/user-namespace-remapped engines can have
additional limits. Enabling Protected execution may require administrator-managed
kernel user-namespace settings, a reviewed Docker seccomp profile permitting the
required namespace/mount operations inside an unprivileged user namespace, and
an AppArmor profile that permits that namespace use. An administrator can attach
those specific profiles with Compose `security_opt` or Docker `--security-opt`,
then rerun the probe and the `--sandbox required` smoke. The exact profiles depend
on the host kernel and Docker version; this repository does not ship an untested
universal profile. Do not substitute `privileged`, `SYS_ADMIN`, unconfined
seccomp/AppArmor, the host network or a mounted Docker socket to make a test pass.
For servers that cannot supply the required isolation, use Agent Links to a node
with qualified Protected execution, or make an explicit owner policy choice.
See [Permissions](permissions.md) and [Executor](executor.md).

## Data, access and operations

The default account is UID/GID `1000:1000`. A new named volume inherits its private
home directory from the image. For a bind mount, precreate an empty server
directory owned by `1000:1000` with mode `0700` and mount it at `/home/pot`.
Account for UID mapping if using a rootless/remapped engine. The entrypoint does
not start as root or recursively change ownership of existing data. Runtime and
browser roots must remain private; do not make them world-readable to fix a
permission error.

| Container path | Durable contents |
|---|---|
| `/home/pot/.pot` | Runtime identity, databases, encrypted provider/service credentials, permissions and task state |
| `/home/pot/.pot-web` | Browser binding, access token, session signing key and revocations |
| `/home/pot/.pot/secrets/master.key` | Automatically generated Linux credential master key, created on first secret write |
| `/home/pot/workspaces` | Default assistant workspaces, independent Task directories and managed task worktrees |

Back up the **complete volume** with both services stopped. Databases and master
key must be restored together; never regenerate the key to recover an existing
database. User-selected project paths outside `/home/pot` need their own mounts
and backups. New independent Task directories use the configured Runtime workspace
`tasks/` child and survive replacement when that workspace is on the volume.
Older Tasks retain their recorded OS temporary paths, which do not survive
container replacement. Do not mount the installation assets inside a writable
workspace. See [Secrets](secrets.md) and [Task workspaces](task-workspaces.md).

For public access, put a TLS-terminating reverse proxy on the server in front of
the loopback port and set `POT_WEB_ORIGIN=https://pot.example.com`. Preserve the
original `Host` and `Origin`, forward streaming responses without buffering and
allow long-running SSE requests. Serve the gateway at the origin root, not under
a URL prefix. Only the proxy should be reachable publicly. The configured HTTPS
origin makes session cookies Secure; forwarding HTTP internally does not disable
session authentication or same-origin checks. Publishing an unencrypted listener
on a public interface is not an installation default.

```sh
POT_WEB_ORIGIN=https://pot.example.com \
  docker compose -f docker/compose.yaml up -d --no-build
docker compose -f docker/compose.yaml exec pot \
  pot access -browser-root /home/pot/.pot-web regenerate-token
docker compose -f docker/compose.yaml exec pot \
  pot access -browser-root /home/pot/.pot-web token
```

`disable`, `enable` and `status` use the same operator scope. Rotation or
disabling revokes browser sessions and streams; logout revocation persists
across container recreation. Runtime operator credentials remain private to the
node and are not exposed through the browser gateway. Only `17703/tcp` is
published; Agent Link listeners are not published by this recipe.

```sh
docker compose -f docker/compose.yaml ps
docker compose -f docker/compose.yaml logs --tail=100 pot
docker compose -f docker/compose.yaml exec pot pot status -root /home/pot/.pot
docker compose -f docker/compose.yaml restart
docker compose -f docker/compose.yaml down
```

Health checks cover the Runtime operator socket and browser gateway. tini reaps
orphan processes; the entrypoint forwards shutdown and stops both services if
either exits. Compose waits up to 45 seconds for shutdown and restarts failed
containers. Health status alone does not prove Protected execution is available.
`down` retains the named volume; `down -v` deletes it.

Upgrade by stopping the service, backing up the volume, selecting/building the
new image and recreating the container with the same volume. Keep image and
installation resources immutable; use image replacement instead of in-app binary
updates. Schema upgrades follow the existing migration rules; rolling an image
back does not reverse database migrations. See [Deployment](deployment.md).

## Isolated smoke test

After loading a native image into a local Docker Engine:

```sh
python3 scripts/tests/docker-smoke.py --image pot-server:local --sandbox required
```

This creates uniquely named disposable containers and one temporary volume, then
removes them, including on failure. It uses synthetic provider data with an
unreachable loopback endpoint, never makes a model request and never touches an
existing installation. It checks non-root startup, the frontend, token/session
authentication, origin/route restrictions, default Protected policy, real SRT
isolation, complete-volume persistence, session revocation, token rotation and
both-service shutdown. No Docker socket, host paths or elevated capabilities are
mounted or granted.

On a host known to deny namespaces, run `--sandbox unavailable` to verify that
specific fail-closed result together with the server checks. This mode requires
the known namespace refusal; it rejects broken assets and successful execution.
It does **not** qualify local Protected support. Release automation and registry
publication belong to the separate release workflow; the local build/smoke
commands here do not publish an image or promise an already published tag.
