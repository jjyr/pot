package desktop

import (
	"github.com/wailsapp/wails/v3/pkg/application"
	"golang.org/x/sys/windows/registry"
)

func localDeviceName() string        { return "Pot" }
func configureMenu(*application.App) {}
func closeMain(app *application.App, _ *application.WebviewWindow, event *application.WindowEvent) {
	event.Cancel()
	app.Quit()
}

// Windows has no per-app permission prompt. Report OS notification preferences
// rather than Wails' authorization methods, which unconditionally return true.
func notificationAuthorization() string {
	for _, setting := range []struct{ path, name string }{{`Software\Microsoft\Windows\CurrentVersion\PushNotifications`, "ToastEnabled"}, {`Software\Microsoft\Windows\CurrentVersion\Notifications\Settings\Pot`, "Enabled"}} {
		key, err := registry.OpenKey(registry.CURRENT_USER, setting.path, registry.QUERY_VALUE)
		if err == registry.ErrNotExist {
			continue
		}
		if err != nil {
			return "unavailable"
		}
		value, _, err := key.GetIntegerValue(setting.name)
		key.Close()
		if err == registry.ErrNotExist {
			continue
		}
		if err != nil {
			return "unavailable"
		}
		if value == 0 {
			return "denied"
		}
	}
	return "granted"
}
