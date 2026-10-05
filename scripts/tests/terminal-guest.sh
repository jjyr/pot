#!/bin/sh
set -eu
exec >/data/check.log 2>&1
su potdesktop -c 'python3 /check/terminal-runtime.py --runtime /opt/pot/pot-runtime' >/data/terminal-runtime.json
cat /data/terminal-runtime.json
