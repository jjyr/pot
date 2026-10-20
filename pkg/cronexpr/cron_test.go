package cronexpr

import "testing"

func TestSystemTimezone(t *testing.T) {
	for _, value := range []string{"Europe/Berlin", "America/New_York", "UTC", ":/usr/share/zoneinfo/Asia/Tokyo"} {
		t.Setenv("TZ", value)
		zone, err := SystemTimezone()
		if err != nil {
			t.Fatal(value, err)
		}
		if _, err = Location(zone); err != nil {
			t.Fatal(zone, err)
		}
	}
	for _, value := range []string{"", "Local", "UTC+8", "/missing/zone", "Invalid/Zone"} {
		t.Setenv("TZ", value)
		if zone, err := SystemTimezone(); err == nil {
			t.Fatal("unresolved TZ accepted", value, zone)
		}
	}
}
func TestParserBoundaries(t *testing.T) {
	for _, value := range []string{"* * * * *", "*/15 8-20 * JAN,MAR MON-FRI", "0,30 0,12 1-15/2 * 0,6"} {
		if _, err := Parse(value); err != nil {
			t.Fatal(value, err)
		}
	}
	for _, value := range []string{"", "0 * * * * *", "@daily", "CRON_TZ=UTC 0 8 * * *", "0 8 ? * *", "0 0 L * *", "0 0 * * 7", "0 0 * * MON#2", "60 * * * *", "*/0 * * * *"} {
		if _, err := Parse(value); err == nil {
			t.Fatal("accepted", value)
		}
	}
	for _, zone := range []string{"", "Local", "+08:00", "../UTC"} {
		if _, err := Location(zone); err == nil {
			t.Fatal(zone)
		}
	}
}
