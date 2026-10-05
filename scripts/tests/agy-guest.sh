#!/bin/sh
set -eu
exec >/data/check.log 2>&1
su potdesktop -c 'python3 /check/agy-cli-smoke.py' >/data/agy-cli-smoke.json
su potdesktop -c 'python3 /check/agy-runtime.py --runtime /opt/pot/pot-runtime' >/data/agy-runtime.json
cat /data/agy-cli-smoke.json /data/agy-runtime.json
