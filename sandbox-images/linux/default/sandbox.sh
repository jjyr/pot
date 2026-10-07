#!/bin/sh
set -eu
umask 077
mkdir -p /data/workspace /data/logs
chown potdesktop /data/workspace
if [ ! -f /sys/fs/cgroup/cgroup.controllers ]; then
  mkdir -p /sys/fs/cgroup
  mount -t cgroup2 none /sys/fs/cgroup
fi
# The guest contains no product backend, provider configuration or Agent session.
exec python3 /opt/pot/executor.py
