#!/bin/sh
set -eu
exec >/build/build.log 2>&1
export POT_IMAGE_ASSETS=/build/assets
/bin/sh /build/assets/install.sh
apk info -v | sort >/build/installed-packages.txt
python3 - <<'PY'
import json,pathlib
blocks=pathlib.Path('/lib/apk/db/installed').read_text().split('\n\n')
packages=[dict(l.split(':',1) for l in b.splitlines() if ':' in l) for b in blocks]
pathlib.Path('/build/package-sizes.json').write_text(json.dumps({p['P']:{'version':p['V'],'installed_bytes':int(p.get('I',0)),'archive_bytes':int(p.get('S',0))} for p in packages if 'P' in p},indent=2))
PY
# Export a clean root only. No host credentials, user sessions or mounted /build.
tar -C / -cf /build/rootfs.tar bin etc home lib media mnt opt root sbin srv tmp usr var data
