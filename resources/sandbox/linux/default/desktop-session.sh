#!/bin/sh
# A minimal Xfce session: a launcher panel, no login manager, screensaver or compositor.
set -eu
export DISPLAY=:99 LANG=C.UTF-8 LC_ALL=C.UTF-8
export XDG_CURRENT_DESKTOP=XFCE XDG_SESSION_TYPE=x11
export XDG_RUNTIME_DIR=/tmp/pot-runtime-user
mkdir -p "$XDG_RUNTIME_DIR"
chmod 700 "$XDG_RUNTIME_DIR"
setprop() { xfconf-query -c "$1" -p "$2" -s "$4" 2>/dev/null || xfconf-query -c "$1" -p "$2" -n -t "$3" -s "$4"; }
if [ ! -f "$HOME/.pot-xfce-version-1" ]; then
  setprop xsettings /Net/ThemeName string adw-gtk3
  setprop xsettings /Net/IconThemeName string Adwaita
  setprop xsettings /Gtk/FontName string 'Noto Sans 11'
  setprop xsettings /Gtk/CursorThemeName string Adwaita
  setprop xsettings /Gtk/CursorThemeSize int 24
  setprop xsettings /Xft/DPI int 96
  setprop xsettings /Xft/Antialias int 1
  setprop xsettings /Xft/HintStyle string hintslight
  setprop xsettings /Xft/RGBA string none
  setprop xfwm4 /general/theme string Default
  setprop xfwm4 /general/title_font string 'Noto Sans Medium 11'
  setprop xfwm4 /general/button_layout string '|HMC'
  setprop xfwm4 /general/use_compositing bool false
  setprop xfwm4 /general/workspace_count int 1
  setprop xfwm4 /general/wrap_workspaces bool false
  setprop xfce4-desktop /desktop-icons/style int 0
  setprop xfce4-desktop /desktop-menu/show bool true
  setprop xfce4-desktop /windowlist-menu/show bool false
  touch "$HOME/.pot-xfce-version-1"
fi
xfsettingsd --sm-client-disable &
xfwm4 --compositor=off --sm-client-disable &
wm=$!
xfdesktop --disable-wm-check &
xfce4-panel --disable-wm-check &
# xfdesktop uses defaults until a monitor property is explicitly created.
paths=$(xrandr --query | awk '$2 == "connected" {print "/backdrop/screen0/monitor" $1 "/workspace0/last-image"}')
for path in $paths; do
  setprop xfce4-desktop "$path" string /data/pot-wallpaper.svg
  setprop xfce4-desktop "${path%/last-image}/image-style" int 5
  setprop xfce4-desktop "${path%/last-image}/backdrop-cycle-enable" bool false
done
wait "$wm"
