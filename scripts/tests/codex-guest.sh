#!/bin/sh
set -eu
exec >/data/check.log 2>&1
codex --version
su potdesktop -c 'python3 /check/codex-runtime.py --runtime /opt/pot/pot-runtime --codex /usr/local/bin/codex' >/data/codex-runtime.json
cat /data/codex-runtime.json
