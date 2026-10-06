#!/bin/sh
set -eu
exec >/build/build.log 2>&1
export LANG=C.UTF-8 LC_ALL=C.UTF-8
mkdir -p /cache/apk /cache/npm
# Each lockfile entry is intentionally passed as a separate package argument.
# shellcheck disable=SC2046
apk --cache-dir /cache/apk add $(cat /build/assets/packages.lock)
adduser -D -H -h /data/engine-home potdesktop
adduser -D -H -h /data/task-home pottask
mkdir -p /opt/pot /opt/pi /opt/pot/novnc /data
cp /build/assets/* /opt/pot/
tar xzf /opt/pot/novnc.tar.gz -C /opt/pot/novnc --strip-components=1
rm /opt/pot/novnc.tar.gz
chmod -R a+rX /opt/pot
chmod +x /opt/pot/open-browser.sh /opt/pot/pot-tool-executor
ln -sf /opt/pot/open-browser.sh /usr/local/bin/pot-browser
fc-cache -f
apk info -v | sort >/build/installed-packages.txt
python3 - <<'PY'
import json,pathlib
blocks=pathlib.Path('/lib/apk/db/installed').read_text().split('\n\n')
packages=[dict(l.split(':',1) for l in b.splitlines() if ':' in l) for b in blocks]
pathlib.Path('/build/package-sizes.json').write_text(json.dumps({p['P']:{'version':p['V'],'installed_bytes':int(p.get('I',0)),'archive_bytes':int(p.get('S',0))} for p in packages if 'P' in p},indent=2))
PY
# Export a clean root only. No host credentials, user sessions or mounted /build.
tar -C / -cf /build/rootfs.tar bin etc home lib media mnt opt root sbin srv tmp usr var data
