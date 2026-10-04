#!/bin/sh
set -eu
umask 077
exec > /data/desktop.log 2>&1
mkdir -p /data/apk-cache
apk --cache-dir /data/apk-cache add $(cat /data/packages.lock)
apk info -v > /data/packages.txt
adduser -D -h /data/engine-home potdesktop
mkdir -p /data/engine-home /data/product /data/workspace
chown -R potdesktop /data/engine-home /data/product /data/workspace
mkdir -p /data/profile /data/logs /data/novnc
chown -R potdesktop /data/profile /data/logs
chmod +x /data/open-browser.sh
ln -sf /data/open-browser.sh /usr/local/bin/pot-browser
mkdir -p /data/engine-home/.fluxbox
cat > /data/engine-home/.fluxbox/menu <<'MENU'
[begin] (Sandbox)
  [exec] (Chromium) {/usr/local/bin/pot-browser}
  [exec] (Terminal) {xterm}
[end]
MENU
chown -R potdesktop /data/engine-home/.fluxbox
rm -f /data/profile/browser.pid
tar xzf /data/novnc.tar.gz -C /data/novnc --strip-components=1
su potdesktop -c 'Xvnc :99 -geometry 1280x800 -depth 24 -localhost -SecurityTypes None -rfbport 5900 > /data/logs/vnc.log 2>&1 &'
sleep 2
su potdesktop -c 'DISPLAY=:99 fluxbox > /data/logs/wm.log 2>&1 &'
python3 -m http.server 8081 --bind 127.0.0.1 --directory /data > /data/logs/page.log 2>&1 &
websockify --web=/data/novnc 127.0.0.1:6080 127.0.0.1:5900 > /data/logs/websockify.log 2>&1 &
POT_BACKEND=local POT_INTEGRATED_ROOT=/data POT_BROKER_BIND=0.0.0.0 POT_UPSTREAM_WS=http://127.0.0.1:6080/websockify POT_VIEWER_HTML=/data/index.html python3 /data/broker.py > /data/logs/broker.log 2>&1 &
# Product runtime and engine run as an unprivileged guest user.
chmod +x /data/pot-runtime /data/agy
ln -sf /data/agy /usr/local/bin/agy
# Install the integrity-locked Pi distribution once per lockfile. Its cache is
# persistent, so reopening does not redownload packages or change versions.
mkdir -p /data/pi-install
cp /data/pi-package.json /data/pi-install/package.json
cp /data/pi-package-lock.json /data/pi-install/package-lock.json
pi_hash=$(sha256sum /data/pi-package-lock.json | cut -d ' ' -f 1)
if [ "$(cat /data/pi-install/installed-lock 2>/dev/null || true)" != "$pi_hash" ]; then
 npm ci --prefix /data/pi-install --cache /data/npm-cache --ignore-scripts --omit=dev --no-audit --no-fund
 echo "$pi_hash" > /data/pi-install/installed-lock
fi
chmod -R a+rX /data/pi-install
ln -sf /data/pi-install/node_modules/.bin/pi /usr/local/bin/pi
su potdesktop -c 'exec /data/pot-runtime' > /data/logs/runtime.log 2>&1 &
runtime_pid=$!
# Wait for an actual browser process exit before acknowledging clean shutdown.
shutdown() {
 trap '' TERM INT
 current_browser_pid=$(cat /data/profile/browser.pid 2>/dev/null || true)
 if [ -n "$current_browser_pid" ] && kill -0 "$current_browser_pid" 2>/dev/null; then
  python3 /data/close-browser.py || true
 fi
 for i in $(seq 1 80); do
  if [ -z "$current_browser_pid" ] || ! kill -0 "$current_browser_pid" 2>/dev/null; then
   wait "$browser_pid" || true
   touch /data/browser-stopped
   kill "$runtime_pid" 2>/dev/null || true
   exit 0
  fi
  sleep 0.1
 done
 echo 'Chromium close timed out' >&2
 exit 1
}
trap shutdown TERM INT
rm -f /data/browser-stopped
# Preserve Chromium sandbox; any failure must remain visible in logs.
su potdesktop -c 'exec /usr/local/bin/pot-browser' > /data/logs/chromium.log 2>&1 &
browser_pid=$!
# Chromium is an optional desktop application, not the guest's main service.
# Reap its initial launcher, then keep the environment alive with Runtime.
wait "$browser_pid" || true
wait "$runtime_pid"
