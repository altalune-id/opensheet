package db

import (
	"strings"
	"testing"
)

func TestForceUTCSession_ReplacesEveryTimeZoneSpelling(t *testing.T) {
	for _, dsn := range []string{
		"postgres://u:p@localhost/db?timezone=Asia/Jakarta",
		"postgres://u:p@localhost/db?TimeZone=Asia/Jakarta",
		"postgres://u:p@localhost/db?TIMEZONE=Asia/Jakarta",
		"postgres://u:p@localhost/db",
	} {
		connCfg, err := pgConnConfig(dsn)
		if err != nil {
			t.Fatalf("%s: %v", dsn, err)
		}
		var zones []string
		for k, v := range connCfg.RuntimeParams {
			if strings.EqualFold(k, "timezone") {
				zones = append(zones, k+"="+v)
			}
		}
		if len(zones) != 1 || zones[0] != "timezone=UTC" {
			t.Errorf("%s: session zone params = %v, want [timezone=UTC]", dsn, zones)
		}
	}
}
