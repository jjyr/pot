// Package testutil provides test harness budgets, never product timeouts.
package testutil

import (
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"
)

// Timeout scales an asynchronous test's waiting budget. CI sets
// POT_TEST_TIMEOUT_SCALE=4; local runs default to the original duration.
func Timeout(t testing.TB, local time.Duration) time.Duration {
	t.Helper()
	budget, err := timeout(local, os.Getenv("POT_TEST_TIMEOUT_SCALE"))
	if err != nil {
		t.Fatal(err)
	}
	return budget
}

func timeout(local time.Duration, value string) (time.Duration, error) {
	if value == "" {
		value = "1"
	}
	scale, err := strconv.Atoi(value)
	if err != nil || scale < 1 || scale > 100 {
		return 0, fmt.Errorf("POT_TEST_TIMEOUT_SCALE must be an integer from 1 to 100: %q", value)
	}
	return local * time.Duration(scale), nil
}
