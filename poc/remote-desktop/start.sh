#!/bin/bash
set -euo pipefail
export DISPLAY=:99
# Exclusive ownership before clearing Chromium's hostname-bound stale locks.
exec 9>/profile/.pot-owner.lock
flock -n 9 || { echo 'Profile already owned by another POC container' >&2; exit 1; }
rm -f /profile/chromium/SingletonLock /profile/chromium/SingletonSocket /profile/chromium/SingletonCookie
mkdir -p /workspace/logs /profile/chromium
# Container restart keeps /tmp but terminates the previous private X server.
rm -f /tmp/.X99-lock /tmp/.X11-unix/X99
Xtigervnc :99 -nolisten tcp -geometry 1280x800 -depth 24 -localhost yes -SecurityTypes None -rfbport 5900 -AlwaysShared -AcceptCutText -SendCutText > /workspace/logs/vnc.log 2>&1 &
for i in {1..50}; do xdpyinfo >/dev/null 2>&1 && break; sleep .1; done
xdpyinfo >/dev/null 2>&1 || { cat /workspace/logs/vnc.log >&2; exit 1; }
fluxbox > /workspace/logs/wm.log 2>&1 &
python3 -m http.server 8081 --bind 127.0.0.1 --directory /opt/pot/assets > /workspace/logs/page.log 2>&1 &
websockify --web=/opt/novnc 6080 127.0.0.1:5900 > /workspace/logs/websockify.log 2>&1 &
chromium --user-data-dir=/profile/chromium --no-first-run --no-default-browser-check --disable-dev-shm-usage --disable-gpu --remote-debugging-address=127.0.0.1 --remote-debugging-port=9222 --window-size=1280,800 --start-maximized http://127.0.0.1:8081/test.html > /workspace/logs/chromium.log 2>&1 &
browser=$!
trap 'kill -TERM "$browser" 2>/dev/null || true; wait "$browser" || true; exit' TERM INT
wait "$browser"
