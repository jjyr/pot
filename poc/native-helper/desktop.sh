#!/bin/sh
set -eu
exec > /data/desktop.log 2>&1
mkdir -p /data/apk-cache
apk --cache-dir /data/apk-cache add $(cat /data/packages.lock)
apk info -v > /data/packages.txt
adduser -D potdesktop
mkdir -p /data/profile /data/logs /data/novnc
chown -R potdesktop /data/profile /data/logs
tar xzf /data/novnc.tar.gz -C /data/novnc --strip-components=1
su potdesktop -c 'Xvnc :99 -geometry 1280x800 -depth 24 -localhost -SecurityTypes None -rfbport 5900 > /data/logs/vnc.log 2>&1 &'
sleep 2
su potdesktop -c 'DISPLAY=:99 fluxbox > /data/logs/wm.log 2>&1 &'
python3 -m http.server 8081 --bind 127.0.0.1 --directory /data > /data/logs/page.log 2>&1 &
websockify --web=/data/novnc 127.0.0.1:6080 127.0.0.1:5900 > /data/logs/websockify.log 2>&1 &
POT_BACKEND=local POT_INTEGRATED_ROOT=/data POT_BROKER_BIND=0.0.0.0 POT_UPSTREAM_WS=http://127.0.0.1:6080/websockify POT_VIEWER_HTML=/data/index.html python3 /data/broker.py > /data/logs/broker.log 2>&1 &
# Preserve Chromium sandbox; any failure must remain visible in logs.
exec su potdesktop -c 'exec env DISPLAY=:99 chromium --user-data-dir=/data/profile --remote-debugging-address=127.0.0.1 --remote-debugging-port=9222 --no-first-run --disable-dev-shm-usage --disable-gpu --start-maximized http://127.0.0.1:8081/test.html' > /data/logs/chromium.log 2>&1
