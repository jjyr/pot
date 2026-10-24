package testutil

import (
	"testing"
	"time"
)

func TestTimeoutBudgets(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  time.Duration
	}{
		{"", 5 * time.Second},
		{"1", 5 * time.Second},
		{"4", 20 * time.Second},
	} {
		t.Run(tc.value, func(t *testing.T) {
			t.Setenv("POT_TEST_TIMEOUT_SCALE", tc.value)
			if got := Timeout(t, 5*time.Second); got != tc.want {
				t.Fatalf("budget = %v, want %v", got, tc.want)
			}
		})
	}
	for _, value := range []string{"0", "-1", "101", "fast", "1.5"} {
		if _, err := timeout(time.Second, value); err == nil {
			t.Errorf("accepted invalid budget scale %q", value)
		}
	}
}
