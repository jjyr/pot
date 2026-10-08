# macOS Sandbox enrollment and execution

Updated: 2026-10-07. Prepared-instance implementation, awaiting real macOS/Xcode VM qualification. Linux remains the default. Android/iOS are deferred.

Server registers `pot-sandbox-macos-apple-vm` alongside Linux; Runtime calls `pot-sandbox` without production dependencies on either Provider. `PotMacOSHelper` is a separately signed Swift executable using public Virtualization.framework. Registration does not boot or download anything. Missing optional files show an unavailable macOS definition while leaving chat and Linux available.

The `pot-macos-executor-v3` bootstrap adds authenticated session inspection. Previously enrolled v2 guests must install the current bootstrap and update their manifest before use; the Provider rejects an older manifest before execution.

The first slice supports command/stdin/patch execution as a standard guest account, with one execution owner per VM. Managed sessions survive normal turns within the same Agent epoch and expire on inactivity or explicit cancellation/closure. The VM stays running after normal turn completion or cancellation; files, applications and background state remain persistent. Scope closure expires managed command sessions and terminates their runner/process group. It does not reset the environment or prove that detached/launchd work is gone. Manager accepts this explicit `session-closure` policy separately from Linux's `process-containment` policy. Unknown scope closure still quarantines ownership until recovery; no automatic VM-stop fallback is used.

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

Build the Host components with `mise run check:swift` and the native runner with `mise exec -- cargo build --locked -p pot-tool-executor --release`. Sign Helper with `helper/virtualization.entitlements`. App bundling includes/signs the separate Helper and places the native runner/bootstrap assets in `runtime/macos-bootstrap/`; standalone installations must include `PotMacOSHelper` in runtime resources, or configure the trusted `POT_MACOS_HELPER` path. These paths are owner configuration, never Agent arguments.

1. Generate per-instance assets on Host:

   ```sh
   mise exec -- python scripts/macos-enrollment --guest-ip ACTUAL_GUEST_IP --output /PRIVATE/ENROLLMENT
   ```

   This generates a dedicated CA, guest certificate/key and token; it deletes the temporary CA signing key. It neither starts a VM nor accepts OS/Xcode terms.

2. Privately transfer the guest certificate/key/token and native `pot-tool-executor` into that guest's enrollment staging directory. Transfer the files under `sandbox-images/macos/xcode/` as trusted bootstrap inputs, dereferencing the shared `permission_state.py` symlink (`cp -L` or an equivalent staging step) so the module is transferred as a regular file. Inside the prepared guest, run:

   ```sh
   sudo ./provision.sh STANDARD_ACCOUNT /PRIVATE/GUEST_ENROLLMENT
   ```

   The broker uses Xcode's available `/usr/bin/python3` (3.9+ required), launchd and the credential-free native Rust runner. It checks the account is standard and has no sudo grants. Control keys/config/SQLite stay under root-private `/var/db/pot-executor`; child environments carry no token. Remove Guest staging assets and shut the VM down before copying its complete platform bundle.

3. Place only the CA certificate and matching token in the private Host prepared bundle above. Keep guest private keys on private guest disk, never workspace or Host skill shares. Record actual OS/Xcode versions in the manifest. Start the Pot Server/App, then enroll through the owner CLI:

   ```sh
   pot --root INSTANCE_ROOT sandbox enroll-macos mac-dev /PRIVATE/PREPARED_INSTANCE
   ```

   Enrollment copies the complete fixed-file bundle, rejects symlinks/non-private files and existing instances, and creates a macOS definition through the local management API. It requires the source to be stopped; it cannot prove the state of a third-party VM engine. A failed final API registration retains the private copy for owner inspection. Do not simply retry over an existing copy. Raw whole-GiB disk size is recorded in the instance resource config; disk resizing is rejected. CPU/RAM changes apply after stopping, subject to VZ validation.

4. Main can select `mac-dev` or bind a task explicitly to it. `manage_sandbox` accepts `provider` only at creation; it never accepts a Host bundle path. Worker binding, ownership/session IDs and receipts use the same Manager contracts as Linux.

Guest permissions are stored on guest disk and exposed readonly at execution
HOME’s `~/.pot/agent/permissions.json`; the broker owns writes through its trusted
control endpoint. Reprovisioning and rebuilding the runner are required for this
permission protocol. The macOS file-write policy has portable construction checks
but still requires live VM qualification.

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

## Validation

See [macOS implementation acceptance](validation/macos-provider-20261007.md). Automated tests cover Manager exclusive ownership, helper protocol/process ownership, bundle validation, TLS identity/authentication, no-replay receipts and workspace checks. The real compiled Helper's private IPC is tested without media. Real macOS boot/stop, VirtioFS UID mapping, root/standard-user separation, Xcode build/test/artifacts, real VNC GUI session and crash/escape behavior remain unverified until a prepared instance is available. No source descriptor or passing mock test establishes those VM capabilities.
