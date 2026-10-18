//go:build darwin && cgo

package desktop

/*
#cgo CFLAGS: -fblocks
#cgo LDFLAGS: -framework Foundation -framework UserNotifications
int potNotificationAuthorization(void);
*/
import "C"

// Wails exposes only an authorized boolean. Read the official status separately
// so NotDetermined and Denied have different, accurate settings presentations.
func notificationAuthorization() string {
	switch int(C.potNotificationAuthorization()) {
	case 0:
		return "default"
	case 1:
		return "denied"
	case 2, 3, 4:
		return "granted"
	default:
		return "unavailable"
	}
}
