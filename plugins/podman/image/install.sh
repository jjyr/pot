#!/bin/sh
set -eu
export LANG=C.UTF-8 LC_ALL=C.UTF-8 DEBIAN_FRONTEND=noninteractive
assets=${POT_IMAGE_ASSETS:-/build/assets}
# The digest-pinned Debian base and signed snapshot are shared by both builders.
# shellcheck disable=SC1091
. /etc/os-release
test "$ID" = debian && test "$VERSION_CODENAME" = trixie
snapshot=$(sed -n 's/.*"snapshot"[[:space:]]*:[[:space:]]*"\([0-9]*T[0-9]*Z\)".*/\1/p' "$assets/base-image.json")
printf '%s\n' "$snapshot" | grep -Eq '^[0-9]{8}T[0-9]{6}Z$'
rm -f /etc/apt/sources.list /etc/apt/sources.list.d/*.sources /etc/apt/sources.list.d/*.list
sources() {
  printf 'deb [check-valid-until=no signed-by=/usr/share/keyrings/debian-archive-keyring.gpg] %s://snapshot.debian.org/archive/debian/%s/ trixie main\n' "$1" "$snapshot"
  printf 'deb [check-valid-until=no signed-by=/usr/share/keyrings/debian-archive-keyring.gpg] %s://snapshot.debian.org/archive/debian-security/%s/ trixie-security main\n' "$1" "$snapshot"
}
# Bootstrap the CA bundle through signature-verified Debian metadata; subsequent
# requests use HTTPS. Expired snapshot dates do not disable signature checking.
sources http >/etc/apt/sources.list.d/pot-snapshot.list
printf '#!/bin/sh\nexit 101\n' >/usr/sbin/policy-rc.d
chmod 755 /usr/sbin/policy-rc.d
apt-get update
apt-get install -y --no-install-recommends ca-certificates
sources https >/etc/apt/sources.list.d/pot-snapshot.list
apt-get update
packages=$(sed 's/#.*//; /^[[:space:]]*$/d' "$assets/packages.lock")
# Each direct package is intentionally passed as its own argument.
# shellcheck disable=SC2086
apt-get install -y --no-install-recommends $packages
rm /usr/sbin/policy-rc.d
apt-get clean
rm -rf /var/lib/apt/lists/*

groupadd --gid 1000 potdesktop
useradd --uid 1000 --gid 1000 --no-create-home --home-dir /data/engine-home --shell /bin/bash potdesktop
mkdir -p /opt/pot /opt/pi /opt/pot/novnc /opt/pot/node /data
cp "$assets"/* /opt/pot/
for archive in node adw-gtk3 adwaita-fonts; do
  (cd "$assets" && sha256sum -c "$archive.sha256")
done
tar xzf /opt/pot/node.tar.gz -C /opt/pot/node --strip-components=1
# The executor capability port probes /usr/bin/node. Keep login-shell and npm
# entrypoints on the same exact runtime rather than a second apt Node version.
ln -sf /opt/pot/node/bin/node /usr/bin/node
for tool in node npm npx corepack; do
  if [ -e "/opt/pot/node/bin/$tool" ]; then
    ln -sf "/opt/pot/node/bin/$tool" "/usr/local/bin/$tool"
  fi
done
# Fail the image build if the GitHub plugin's target-architecture clients cannot
# start. Credentials are supplied at execution time, never during image creation.
for tool in node git gh; do
  "$tool" --version
done
mkdir -p /usr/share/themes /usr/local/share/fonts/adwaita
tar xJf /opt/pot/adw-gtk3.tar.xz -C /usr/share/themes
tar xJf /opt/pot/adwaita-fonts.tar.xz -C /usr/local/share/fonts/adwaita --strip-components=1
test -d /usr/share/themes/adw-gtk3/gtk-3.0
# Font licenses remain beside their original files in this immutable directory.
find /usr/local/share/fonts/adwaita -type f -iname '*mono*.ttf' | grep -q .
tar xzf /opt/pot/novnc.tar.gz -C /opt/pot/novnc --strip-components=1
rm /opt/pot/node.tar.gz /opt/pot/adw-gtk3.tar.xz /opt/pot/adwaita-fonts.tar.xz /opt/pot/novnc.tar.gz
chmod -R a+rX /opt/pot /usr/share/themes/adw-gtk3* /usr/local/share/fonts/adwaita
chmod +x /opt/pot/open-browser.sh /opt/pot/pot-tool-executor
ln -sf /opt/pot/open-browser.sh /usr/local/bin/pot-browser
fc-cache -f
LC_ALL=C dpkg-query -W -f='${binary:Package}\t${Version}\n' | sort >/opt/pot/installed-packages.txt

# Shared toolchain entrypoints remain discoverable through login-shell PATH resets.
for tool in rustup cargo rustc rustdoc rustfmt cargo-fmt clippy-driver cargo-clippy; do
  ln -sf "/data/development/.cargo/bin/$tool" "/usr/local/bin/$tool"
done
mkdir -p /etc/profile.d
cat >/etc/profile.d/pot-development.sh <<'PROFILE'
export CARGO_HOME=/data/development/.cargo
export RUSTUP_HOME=/data/development/.rustup
export PATH="$CARGO_HOME/bin:$PATH"
PROFILE
