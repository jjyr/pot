#!/bin/sh
# Real VM lifecycle workload, deliberately separate from desktop acceptance.
set -eu
echo guest-ready > /data/proof
while [ ! -e /data/crash ]; do sleep 1; done
exit 42
