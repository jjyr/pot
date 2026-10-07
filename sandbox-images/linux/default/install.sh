#!/bin/sh
set -eu
export LANG=C.UTF-8 LC_ALL=C.UTF-8
mkdir -p /cache/apk /cache/npm
if [ "${POT_IMAGE_PACKAGES_INSTALLED:-}" != 1 ]; then
  # Each lockfile entry is intentionally passed as a separate package argument.
  # shellcheck disable=SC2046
  apk --cache-dir /cache/apk add $(cat "${POT_IMAGE_ASSETS:-/build/assets}/packages.lock")
fi
adduser -D -H -h /data/engine-home potdesktop
adduser -D -H -h /data/task-home pottask
mkdir -p /opt/pot /opt/pi /opt/pot/novnc /data
cp "${POT_IMAGE_ASSETS:-/build/assets}"/* /opt/pot/
tar xzf /opt/pot/novnc.tar.gz -C /opt/pot/novnc --strip-components=1
rm /opt/pot/novnc.tar.gz
chmod -R a+rX /opt/pot
chmod +x /opt/pot/open-browser.sh /opt/pot/pot-tool-executor
ln -sf /opt/pot/open-browser.sh /usr/local/bin/pot-browser
fc-cache -f
