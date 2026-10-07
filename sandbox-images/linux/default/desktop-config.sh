#!/bin/sh
# Versioned Pot desktop defaults; preserve subsequent user customization.
set -eu
home=/data/engine-home
revision=1
if [ "$(cat "$home/.pot-desktop-version" 2>/dev/null || true)" != "$revision" ]; then
  mkdir -p "$home/.config/fontconfig" "$home/.config/gtk-3.0" "$home/.config/xfce4/terminal" "$home/.config/menus" "$home/.local/share/applications" "$home/Desktop"
  cat >"$home/.config/fontconfig/fonts.conf" <<'XML'
<?xml version="1.0"?>
<!DOCTYPE fontconfig SYSTEM "urn:fontconfig:fonts.dtd">
<fontconfig>
  <alias><family>sans-serif</family><prefer><family>Noto Sans</family><family>Noto Sans CJK SC</family><family>Noto Sans CJK TC</family><family>Noto Sans CJK JP</family><family>Noto Sans CJK KR</family><family>Noto Color Emoji</family></prefer></alias>
  <alias><family>monospace</family><prefer><family>Adwaita Mono</family><family>Noto Sans Mono CJK SC</family><family>Noto Sans Mono CJK TC</family><family>Noto Sans Mono CJK JP</family><family>Noto Sans Mono CJK KR</family><family>Noto Color Emoji</family></prefer></alias>
  <match target="pattern"><test name="lang" compare="contains"><string>ja</string></test><edit name="family" mode="prepend"><string>Noto Sans CJK JP</string></edit></match>
  <match target="pattern"><test name="lang" compare="contains"><string>ko</string></test><edit name="family" mode="prepend"><string>Noto Sans CJK KR</string></edit></match>
  <match target="pattern"><test name="lang" compare="contains"><string>zh-tw</string></test><edit name="family" mode="prepend"><string>Noto Sans CJK TC</string></edit></match>
  <match target="font"><edit name="antialias" mode="assign"><bool>true</bool></edit><edit name="hintstyle" mode="assign"><const>hintslight</const></edit><edit name="rgba" mode="assign"><const>none</const></edit></match>
</fontconfig>
XML
  cat >"$home/.config/gtk-3.0/settings.ini" <<'INI'
[Settings]
gtk-theme-name=adw-gtk3
gtk-icon-theme-name=Adwaita
gtk-font-name=Noto Sans 11
gtk-cursor-theme-name=Adwaita
gtk-cursor-theme-size=24
gtk-application-prefer-dark-theme=false
gtk-enable-animations=false
INI
  cat >"$home/.config/xfce4/terminal/terminalrc" <<'INI'
[Configuration]
FontName=Adwaita Mono 12
FontUseSystem=FALSE
ColorForeground=#30343b
ColorBackground=#faf9f6
ColorCursor=#079fee
ColorSelectionBackground=#dcecf9
ColorSelectionUseDefault=FALSE
BackgroundMode=TERMINAL_BACKGROUND_SOLID
MiscMenubarDefault=FALSE
MiscToolbarDefault=FALSE
MiscBell=FALSE
MiscAlwaysShowTabs=FALSE
ScrollingBar=TERMINAL_SCROLLBAR_NONE
INI
  for app in browser files terminal; do
    case "$app" in
      browser)
        name=Browser
        command=/usr/local/bin/pot-browser
        icon=web-browser
        ;;
      files)
        name=Files
        command='thunar /data/workspace'
        icon=system-file-manager
        ;;
      terminal)
        name=Terminal
        command='xfce4-terminal --working-directory=/data/workspace'
        icon=utilities-terminal
        ;;
    esac
    cat >"$home/.local/share/applications/pot-$app.desktop" <<DESKTOP
[Desktop Entry]
Type=Application
Name=$name
Exec=$command
Icon=$icon
Terminal=false
Categories=Pot;
DESKTOP
  done
  cat >"$home/.config/menus/xfce-applications.menu" <<'XML'
<!DOCTYPE Menu PUBLIC "-//freedesktop//DTD Menu 1.0//EN" "http://www.freedesktop.org/standards/menu-spec/1.0/menu.dtd">
<Menu><Name>Pot</Name><DefaultAppDirs/><Include><Filename>pot-browser.desktop</Filename><Filename>pot-files.desktop</Filename><Filename>pot-terminal.desktop</Filename></Include><Layout><Filename>pot-browser.desktop</Filename><Filename>pot-files.desktop</Filename><Filename>pot-terminal.desktop</Filename></Layout></Menu>
XML
  printf '%s\n' "$revision" >"$home/.pot-desktop-version"
  chown -R potdesktop "$home/.config" "$home/.local" "$home/Desktop" "$home/.pot-desktop-version"

fi

# Add discoverable launchers to existing homes without resetting their theme.
if [ ! -f "$home/.pot-panel-version-1" ]; then
  panel="$home/.config/xfce4/panel"
  mkdir -p "$home/.config/xfce4/xfconf/xfce-perchannel-xml" "$panel"
  id=1
  for app in browser files terminal; do
    mkdir -p "$panel/launcher-$id"
    cp "$home/.local/share/applications/pot-$app.desktop" "$panel/launcher-$id/"
    id=$((id + 1))
  done
  cat >"$home/.config/xfce4/xfconf/xfce-perchannel-xml/xfce4-panel.xml" <<'XML'
<?xml version="1.0" encoding="UTF-8"?>
<channel name="xfce4-panel" version="1.0">
  <property name="configver" type="int" value="2"/>
  <property name="panels" type="array">
    <value type="int" value="1"/>
    <property name="panel-1" type="empty">
      <property name="position" type="string" value="p=10;x=0;y=0"/>
      <property name="position-locked" type="bool" value="true"/>
      <property name="size" type="uint" value="40"/>
      <property name="length" type="uint" value="100"/>
      <property name="autohide-behavior" type="uint" value="0"/>
      <property name="plugin-ids" type="array">
        <value type="int" value="1"/><value type="int" value="2"/><value type="int" value="3"/>
        <value type="int" value="4"/><value type="int" value="5"/><value type="int" value="6"/>
      </property>
    </property>
  </property>
  <property name="plugins" type="empty">
    <property name="plugin-1" type="string" value="launcher"><property name="items" type="array"><value type="string" value="pot-browser.desktop"/></property><property name="show-label" type="bool" value="true"/></property>
    <property name="plugin-2" type="string" value="launcher"><property name="items" type="array"><value type="string" value="pot-files.desktop"/></property><property name="show-label" type="bool" value="true"/></property>
    <property name="plugin-3" type="string" value="launcher"><property name="items" type="array"><value type="string" value="pot-terminal.desktop"/></property><property name="show-label" type="bool" value="true"/></property>
    <property name="plugin-4" type="string" value="separator"><property name="style" type="uint" value="0"/></property>
    <property name="plugin-5" type="string" value="tasklist"><property name="expand" type="bool" value="true"/><property name="flat-buttons" type="bool" value="true"/><property name="show-handle" type="bool" value="false"/></property>
    <property name="plugin-6" type="string" value="clock"><property name="digital-format" type="string" value="%H:%M"/></property>
  </property>
</channel>
XML
  touch "$home/.pot-panel-version-1"
  chown -R potdesktop "$panel" "$home/.config/xfce4/xfconf" "$home/.pot-panel-version-1"
fi
