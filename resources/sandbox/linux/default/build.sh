#!/bin/sh
set -eu
exec >/build/build.log 2>&1
export POT_IMAGE_ASSETS=/build/assets
/bin/sh /build/assets/install.sh
LC_ALL=C dpkg-query -W -f '${binary:Package}\t${Version}\n' | LC_ALL=C sort >/build/installed-packages.txt
python3 - <<'PYTHON'
import json,pathlib,subprocess
rows=subprocess.check_output(['dpkg-query','-W','-f','${db:Status-Status}\t${binary:Package}\t${Version}\t${Installed-Size}\n'],text=True)
packages={}
for row in rows.splitlines():
    status,name,version,kib=row.split('\t')
    if status == 'installed':
        packages[name]={'version':version,'installed_bytes':int(kib or '0')*1024}
pathlib.Path('/build/package-sizes.json').write_text(json.dumps(packages,indent=2,sort_keys=True)+'\n')
PYTHON
# Preserve Debian usrmerge symlinks. Never dereference /bin, /lib or /sbin,
# and never export the live /build, cache, kernel mounts or runtime sockets.
set --
for entry in bin boot etc home lib lib64 media mnt opt root sbin srv tmp usr var data; do
  if [ -e "/$entry" ] || [ -L "/$entry" ]; then
    set -- "$@" "$entry"
  fi
done
tar -C / -cf /build/rootfs.tar -- "$@"
