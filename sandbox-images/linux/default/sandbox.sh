#!/bin/sh
set -eu
umask 077
mkdir -p /data/workspace /data/logs
chown potdesktop /data/workspace
if [ ! -f /sys/fs/cgroup/cgroup.controllers ]; then
  mkdir -p /sys/fs/cgroup
  mount -t cgroup2 none /sys/fs/cgroup
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
