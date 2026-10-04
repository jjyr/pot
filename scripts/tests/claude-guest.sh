#!/bin/sh
set -eu
exec >/data/check.log 2>&1
claude --version
su potdesktop -c 'python3 /check/claude-runtime.py --runtime /opt/pot/pot-runtime --claude /usr/local/bin/claude' >/data/claude-runtime.json
cat /data/claude-runtime.json
