package webui

import "testing"

func TestMessageFileRoutesAreIDOnly(t *testing.T) {
	for _, test := range []struct {
		method, path string
		want         bool
	}{{"GET", "/api/v1/message-files/message", true}, {"POST", "/api/v1/message-files/message/ref", true}, {"GET", "/api/v1/message-files/message/ref", false}, {"POST", "/api/v1/message-files/message", false}, {"GET", "/api/v1/message-files/../etc/passwd", false}, {"GET", "/api/v1/message-files/message/extra/path", false}} {
		if got := Allowed(test.method, test.path); got != test.want {
			t.Fatal(test, got)
		}
	}
}
