#!/bin/sh
# Run inside the prepared macOS guest as owner/root. Never run on the Host.
set -eu
if [ "$(id -u)" -ne 0 ] || [ "$#" -ne 2 ]; then
  echo 'Usage inside guest: sudo provision.sh STANDARD_USER ENROLLMENT_DIRECTORY' >&2
  exit 2
fi
if [ "$(/usr/sbin/sysctl -n kern.hv_vmm_present)" != "1" ]; then
  echo 'Run this bootstrap inside a macOS VM guest.' >&2
  exit 2
fi
execution_user=$1
enrollment=$2
case "$execution_user" in *[!a-zA-Z0-9_-]* | '') exit 2 ;; esac
/usr/sbin/dseditgroup -o checkmember -m "$execution_user" admin | /usr/bin/grep -q '^no '
[ "$(id -u "$execution_user")" -ge 501 ]
/usr/bin/python3 -c 'import sys; assert sys.version_info >= (3, 9)'
/usr/bin/xcodebuild -version
/usr/bin/xcrun --find swift
private=/var/db/pot-executor
install -d -m 700 "$private"
install -d -m 755 /usr/local/libexec
install -m 755 "$enrollment/pot-tool-executor" /usr/local/libexec/pot-tool-executor
install -m 644 "$(dirname "$0")/executor.py" /usr/local/libexec/pot-macos-executor.py
install -m 644 "$(dirname "$0")/permission_state.py" /usr/local/libexec/permission_state.py
for name in executor-token guest-cert.pem guest-key.pem; do
  install -m 600 "$enrollment/$name" "$private/$name"
done
/usr/bin/python3 - "$execution_user" "$private/config.json" <<'PY'
import json, sys
from pathlib import Path
Path(sys.argv[2]).write_text(json.dumps({"user": sys.argv[1], "desktop_rfb": __import__("os").environ.get("POT_DESKTOP_RFB", "disabled")}))
Path(sys.argv[2]).chmod(0o600)
PY
install -m 644 "$(dirname "$0")/org.pot.executor.plist" /Library/LaunchDaemons/org.pot.executor.plist
printf '%s\n' 'Guest enrollment installed. Remove the guest enrollment staging directory, shut down, enroll the stopped complete bundle on Host, then start via Pot.'
