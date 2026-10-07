#!/bin/sh
set -eu
umask 077
mkdir -p /data/workspace /data/logs /run/pot-skill-sources
chown potdesktop /data/workspace
if [ ! -f /sys/fs/cgroup/cgroup.controllers ]; then
  mkdir -p /sys/fs/cgroup
  mount -t cgroup2 none /sys/fs/cgroup
fi
# Containers receive only their delegated, private cgroup subtree. Never mount
# the host hierarchy or fall back to process-group-only cancellation.
if [ "${POT_CONTAINER:-}" = podman ]; then
  mkdir /sys/fs/cgroup/pot-readiness
  test -w /sys/fs/cgroup/pot-readiness/cgroup.kill
  rmdir /sys/fs/cgroup/pot-readiness
fi
# PTY sessions need devpts before the credential-free runner drops privileges.
if [ ! -e /dev/pts/ptmx ]; then
  mkdir -p /dev/pts
  mount -t devpts -o gid=5,mode=620,ptmxmode=666 devpts /dev/pts
fi
if [ ! -e /dev/ptmx ]; then
  ln -s /dev/pts/ptmx /dev/ptmx
fi
# The guest contains no product backend, provider configuration or Agent session.
exec python3 /opt/pot/executor.py
