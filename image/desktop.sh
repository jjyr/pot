#!/bin/sh
set -eu
umask 077
export LANG=C.UTF-8 LC_ALL=C.UTF-8
exec >/data/desktop.log 2>&1
# Immutable software comes from the bundled OCI image; /data holds user state.
for name in desktop-config.sh desktop-session.sh pot-wallpaper.svg packages.lock test.html home.html driver.py broker.py index.html close-browser.py open-browser.sh; do
  ln -sf "/opt/pot/$name" "/data/$name"
done
# Older versions unpacked noVNC into /data; preserve it, but serve image assets.
mkdir -p /data/engine-home /data/workspace
chown potdesktop /data/engine-home /data/workspace
mkdir -p /data/profile /data/logs
chown potdesktop /data/profile /data/logs
sh /data/desktop-config.sh
su potdesktop -c "fc-cache -f"
rm -f /data/profile/browser.pid
su potdesktop -c 'Xvnc :99 -geometry 1280x800 -depth 24 -localhost -SecurityTypes None -rfbport 5900 > /data/logs/vnc.log 2>&1 &'
sleep 2
su potdesktop -c 'dbus-run-session -- sh /data/desktop-session.sh > /data/logs/wm.log 2>&1 &'
su potdesktop -c 'exec python3 -m http.server 8081 --bind 127.0.0.1 --directory /opt/pot' >/data/logs/page.log 2>&1 &
websockify --web=/opt/pot/novnc 127.0.0.1:6080 127.0.0.1:5900 >/data/logs/websockify.log 2>&1 &
POT_BACKEND=local POT_INTEGRATED_ROOT=/data POT_BROKER_BIND=0.0.0.0 POT_UPSTREAM_WS=http://127.0.0.1:6080/websockify POT_VIEWER_HTML=/data/index.html python3 /data/broker.py >/data/logs/broker.log 2>&1 &
# Wait for an actual browser process exit before acknowledging clean shutdown.
shutdown() {
  trap '' TERM INT
  current_browser_pid=$(cat /data/profile/browser.pid 2>/dev/null || true)
  if [ -n "$current_browser_pid" ] && kill -0 "$current_browser_pid" 2>/dev/null; then
    python3 /data/close-browser.py || true
  fi
  for _ in $(seq 1 80); do
    if [ -z "$current_browser_pid" ] || ! kill -0 "$current_browser_pid" 2>/dev/null; then
      wait "$browser_pid" || true
      touch /data/browser-stopped
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
su potdesktop -c 'exec /usr/local/bin/pot-browser' >/data/logs/chromium.log 2>&1 &
browser_pid=$!
# Chromium is an optional desktop application, not the guest's main service.
# Reap the browser launcher; the optional desktop stays alive until stopped.
wait "$browser_pid" || true
while :; do sleep 1; done
