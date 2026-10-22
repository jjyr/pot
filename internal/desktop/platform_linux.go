//go:build linux && desktop

package desktop

import (
	"github.com/godbus/dbus/v5"
	"github.com/wailsapp/wails/v3/pkg/application"
)

func localDeviceName() string { return "Pot" }

// GTK uses native window decorations and the shared UI's navigation. The macOS
// application menu roles are not a GTK menubar.
func configureMenu(*application.App) {}

// Desktop environments such as GNOME need not expose a StatusNotifier tray.
// Closing the main window therefore quits instead of stranding a hidden Runtime.
func closeMain(app *application.App, _ *application.WebviewWindow, event *application.WindowEvent) {
	event.Cancel()
	app.Quit()
}

// Freedesktop notifications have no macOS-style authorization prompt. A session
// notification daemon must actually own the bus name before claiming support.
func notificationAuthorization() string {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return "unavailable"
	}
	defer conn.Close()
	var present bool
	err = conn.BusObject().Call("org.freedesktop.DBus.NameHasOwner", 0, "org.freedesktop.Notifications").Store(&present)
	if err != nil || !present {
		return "unavailable"
	}
	return "granted"
}
