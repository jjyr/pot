#!/bin/sh
# Temporary VM only; not hardened product configuration.
set -eu
apk add chromium xvfb x11vnc xauth font-noto font-noto-cjk imagemagick
adduser -D potdesktop
mkdir -p /data/desktop
chown potdesktop /data/desktop
ip -4 addr show eth0 > /data/desktop/network.txt
apk info -v > /data/desktop/packages.txt
su potdesktop -c 'Xvfb :99 -screen 0 1024x768x24 -nolisten tcp > /data/desktop/xvfb.log 2>&1 &'
sleep 2
su potdesktop -c 'DISPLAY=:99 chromium --no-sandbox --disable-dev-shm-usage --user-data-dir=/data/desktop/profile --no-first-run "data:text/html,<meta charset=utf-8><h1>Pot Linux Desktop POC</h1><p>真实 Linux 浏览器</p>" > /data/desktop/chromium.log 2>&1 &'
su potdesktop -c 'x11vnc -display :99 -rfbport 5900 -nopw -forever > /data/desktop/vnc.log 2>&1 &'
sleep 10
su potdesktop -c 'DISPLAY=:99 import -window root /data/desktop/linux-desktop.png'
echo DESKTOP_READY
sleep 300
