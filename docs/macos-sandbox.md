# macOS Sandbox enrollment and execution

Native macOS VMs use a complete owner-prepared instance. Linux remains the default
Sandbox; automatic OS installation is not provided.

Runtime registers `macos.apple-virtualization` alongside the shared Podman
provider. `PotMacOSHelper` is a separately signed Swift executable using public
Virtualization.framework for native macOS VMs. Linux containers use the dedicated
Podman machine described in [Sandbox providers](sandbox-providers.md).
Registration does not boot or download anything. Missing optional files show an
unavailable macOS definition while leaving chat and Linux available.

The Provider requires the `pot-macos-executor-v3` bootstrap and validates the
prepared manifest before execution.

The first slice supports command/stdin/patch execution as a standard guest account, with one execution owner per VM. Managed sessions survive normal turns within the same Agent epoch and expire on inactivity or explicit cancellation/closure. The VM stays running after normal turn completion or cancellation; files, applications and background state remain persistent. Scope closure expires managed command sessions and terminates their runner/process group. It does not reset the sandbox or prove that detached/launchd work is gone. Manager accepts this explicit `session-closure` policy separately from Linux's `process-containment` policy. Unknown scope closure still quarantines ownership until recovery; no automatic VM-stop fallback is used.

Only explicit lifecycle stop/delete and orderly backend shutdown stop the owned VM. The helper then requests graceful shutdown, waits up to ten seconds and awaits forced VZ stop if necessary. This lifetime is tied to the backend process; persistence does not imply that the VM remains running after quitting Pot.

The Provider owns its child helper and private socket; it never adopts a stale socket or claims that an unowned helper has stopped. Orderly backend shutdown confirms stop of owned macOS helpers, including idle VMs started explicitly. After a crash, the owner must prove the old helper/VM has exited before removing its stale socket and requesting recovery. VM generation is persisted by Helper before boot; Manager maintains a separate durable epoch and advances it on restart/observed generation change. Unknown receipts never replay.

## Prepared bundle contract

Use a complete, stopped, owner-prepared **Pot-format raw-disk instance**, not IPSW or a Tart/Lume bundle copied without conversion. Original hardware model, machine identifier, auxiliary storage and disk belong together. This is an instance enrollment/ownership transfer, not a qualified golden-image clone. Do not run the source and enrolled copy simultaneously.

```text
prepared-instance/           # owner-only directory, mode 0700
  manifest.json              # all files below mode 0600, regular files, same owner
  hardware-model             # VZMacHardwareModel.dataRepresentation
  machine-identifier         # original VZMacMachineIdentifier.dataRepresentation
  auxiliary-storage          # original VZMacAuxiliaryStorage file
  disk.img                   # installed writable raw macOS disk, 64+ GiB
  guest-ca.pem               # dedicated per-instance broker CA certificate
  executor-token             # per-instance bearer credential
```

`manifest.json` schema (no credentials or model-selected paths):

```json
{
  "schema_version": 1,
  "provider": "macos.apple-virtualization",
  "guest_build": "REPLACE_WITH_INSTALLED_BUILD",
  "xcode_version": "REPLACE_WITH_INSTALLED_XCODE_VERSION",
  "bootstrap": "pot-macos-executor-v3",
  "guest_ip": "192.168.64.3",
  "mac_address": "02:00:00:00:00:01"
}
```

Use an instance-unique, locally administered unicast MAC. The private IP above is an example, not a guaranteed VZ NAT address. Configure/verify the guest's actual private address during owner preparation. This first slice pins that address in enrollment; it does not implement DHCP discovery. If the address changes, update enrollment/certificate identity through the owner preparation process. Broker TLS trusts only the enrolled CA and validates the IP certificate SAN; redirects and proxy inheritance are disabled. Authentication is TLS plus a private bearer token, not mTLS. Guest ingress is authenticated; NAT is not a qualified egress firewall or protection from all Host services.

No installer/first-run automation is delivered. Prepare the complete platform with Apple's VZ installation flow, finish macOS setup, install full Xcode and run its first-launch/license workflow as owner. Create a dedicated standard account with **no administrator membership or sudo grants**. Signing keys, developer login and distribution permissions stay outside base images. Command builds do not provide GUI login/TCC/UI-test qualification.

## Bootstrap and enroll

Build the Host components with `mise run check:swift` and the native runner with `mise run build:go`. Packaging signs the separate Helper with `helper/virtualization.entitlements`, places it beside the Host executable and includes native runner/bootstrap assets in `Resources/macos-bootstrap/`. Custom installations select the Helper directory through `serve -helpers` and runtime assets through `-resources`. These paths are owner configuration, never Agent arguments.

1. Generate per-instance assets on Host:

   ```sh
   mise exec -- python scripts/macos-enrollment --guest-ip ACTUAL_GUEST_IP --output /PRIVATE/ENROLLMENT
   ```

   This generates a dedicated CA, guest certificate/key and token; it deletes the temporary CA signing key. It neither starts a VM nor accepts OS/Xcode terms.

2. Privately transfer the guest certificate/key/token and native `pot-tool-executor` into that guest's enrollment staging directory. Transfer the files under `resources/sandbox/macos/xcode/` as trusted bootstrap inputs, dereferencing the shared `operation_prepare.py` symlink (`cp -L` or an equivalent staging step) so the module is transferred as a regular file. Inside the prepared guest, run:

   ```sh
   sudo ./provision.sh STANDARD_ACCOUNT /PRIVATE/GUEST_ENROLLMENT
   ```

   The broker uses Xcode's available `/usr/bin/python3` (3.9+ required), launchd and the credential-free native Go runner. It checks the account is standard and has no sudo grants. Control keys/config/SQLite stay under root-private `/var/db/pot-executor`; child environments carry no token. Remove Guest staging assets and shut the VM down before copying its complete platform bundle.

3. Place only the CA certificate and matching token in the private Host prepared bundle above. Keep guest private keys on private guest disk, never workspace or Host Skill shares. Record actual OS/Xcode versions in the manifest and keep the complete source instance stopped.

   The Go host retains the Provider, prepared-bundle validation and Swift Helper,
   but currently exposes no owner CLI/API that imports and registers a new
   prepared macOS instance. Enrollment tooling is not implemented; the bundle
   contract above must not be treated as a completed enrollment operation.

   For registered instances, raw whole-GiB disk size is fixed by the enrolled
   disk; resizing is rejected. CPU/RAM changes apply after stopping, subject to
   VZ validation. Models do not manage VM lifecycle. Automatic Tasks use the
   retained shared Linux container, independently of native macOS VM lifecycle.
   Session IDs and receipts use the common Manager contracts.

Control owns all machine grants. The guest broker exposes `/prepare` only for argument normalization and keeps no authorization file. Reprovision the broker after this incompatible protocol change.

## Workspace and capability limits

Only `root/sandboxes/ID/workspace` is writable-shared, at `/Volumes/PotWorkspace`. Project files/artifacts live under `/Volumes/PotWorkspace/workspace`; task files under `tasks/TASK_ID`. The broker mounts VirtioFS with the standard execution UID/GID. Runtime instruction/skill discovery and task artifact directories derive from the Provider's guest path. Product data, helper sockets, executor tokens and native Agent sessions are not shared. Use guest-native storage for DerivedData/caches when a build tool requires it; publish retained outputs into the workspace before closure.

A separate readonly `PotSkills` share contains a boot-time snapshot of configured builtin/global skills. Builtin names take priority; symlinks/special files are skipped. The broker presents it at each scope's `.pot/agent/skills`. Project skills remain in the project workspace. Global skill creation/editing from a macOS scope is not yet qualified; configure those skills through the owner or Linux first. Snapshot refresh occurs on the next boot.

Persistent project state is shared across sequential owners. Whole-VM stop terminates that boot, but it does not remove guest files or guarantee a clean future boot against user-installed persistence. This is not mutual isolation between tasks sharing a project VM. Fresh instances/qualified reset policies are later work.

## Optional noVNC desktop

The Provider implements the common desktop adapter. App and Linux/macOS viewers use Server's ticket-scoped noVNC gateway. Display readiness is independent of command readiness. macOS requires an owner-installed/configured VNC server inside the Guest, with **RFB 3.8 and None authentication on Guest loopback `127.0.0.1:5900` only**. Its public transport is the existing per-instance authenticated TLS broker, not a Host/browser connection to port 5900. The browser receives neither Guest address nor credentials.

This change does not install a VNC package, enable system Screen Sharing or grant TCC permissions. Apple's Screen Sharing authentication and other RFB variants are not qualified by this adapter. Select and qualify a server that actually exports the logged-in macOS desktop, supports the required protocol, and restricts its listener to loopback. A native `VZVirtualMachineView` is not a VNC server. Check the Guest's listener with `lsof -nP -iTCP:5900 -sTCP:LISTEN`; do not enroll a listener on all interfaces.

After configuring that service inside the prepared Guest, opt in when running the trusted bootstrap:

```sh
sudo env POT_DESKTOP_RFB=loopback-none-3.8 ./provision.sh STANDARD_ACCOUNT /PRIVATE/GUEST_ENROLLMENT
```

The root-private broker config records the opt-in. Re-running provisioning without it disables desktop transport. No request can choose a different destination, port or executable. `/desktop-ready` verifies configuration and loopback reachability; `/desktop-stream` upgrades an authenticated TLS connection to the private RFB stream. Without the opt-in/service, opening the desktop returns an unavailable error while command execution remains usable.

Viewing starts without input authority; control is exclusive between gateway viewers. Closing/reconnecting a desktop preserves VM, application and filesystem state. The first implementation disables macOS clipboard and browser-launch controls. A desktop ticket expires after one hour; native reconnect creates a fresh ticket. This is viewer control arbitration, not containment of Guest-local automation or other Guest VNC clients.

## Platform qualification

A prepared instance must qualify real boot/stop, VirtioFS UID mapping,
root/standard-user separation, its Xcode build/test toolchain and any configured
VNC session. Source descriptors, bundle validation and mock tests alone do not
establish these platform capabilities or crash/escape behavior.
