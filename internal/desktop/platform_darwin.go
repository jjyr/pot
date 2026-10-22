package desktop

import "github.com/wailsapp/wails/v3/pkg/application"

func localDeviceName() string { return "This Mac" }

func configureMenu(app *application.App) {
	menu := app.NewMenu()
	menu.AddRole(application.AppMenu)
	menu.AddRole(application.EditMenu)
	menu.AddRole(application.WindowMenu)
	app.Menu.Set(menu)
}

func closeMain(_ *application.App, main *application.WebviewWindow, event *application.WindowEvent) {
	main.Hide()
	event.Cancel()
}
