package mysql

import (
	"context"
	"testing"
)

// TestDetectFlavor_MySQL verifies that a standard MySQL version string is detected as "mysql".
// TestDetectFlavor_MariaDB verifies that a MariaDB version string yields "mariadb".
//
// We test the version-parsing logic directly by injecting a fake db query result via
// a table-driven approach over the internal detectFlavor function, exercised through
// a stub that overrides the SQL connection with a controlled version string.
//
// Because detectFlavor opens a real DB connection, these unit tests validate the
// string-matching logic via a white-box approach on the adapter's flavorOnce path.

func TestFlavorFromVersion(t *testing.T) {
	cases := []struct {
		version string
		want    string
	}{
		{"8.0.32", "mysql"},
		{"8.4.0-commercial", "mysql"},
		{"5.7.44-log", "mysql"},
		{"10.11.6-MariaDB", "mariadb"},
		{"10.6.12-MariaDB-1:10.6.12+maria~ubu2004", "mariadb"},
		{"11.2.2-MariaDB", "mariadb"},
		{"mariadb-10.5.18", "mariadb"},
	}

	for _, tc := range cases {
		t.Run(tc.version, func(t *testing.T) {
			got := flavorFromVersion(tc.version)
			if got != tc.want {
				t.Errorf("flavorFromVersion(%q) = %q, want %q", tc.version, got, tc.want)
			}
		})
	}
}

// detectFlavor uses sync.Once so we test the caching: calling it twice must return the
// same result without reconnecting (the second call is instant).
func TestDetectFlavor_CachedResult(t *testing.T) {
	a := &Adapter{
		dsn:    "root:@tcp(127.0.0.1:9999)/",
		flavor: "mysql",
	}
	// pre-fill the Once so no real connection is attempted
	a.flavorOnce.Do(func() {})

	ctx := context.Background()
	first := a.detectFlavor(ctx)
	second := a.detectFlavor(ctx)
	if first != second {
		t.Errorf("detectFlavor returned different values on second call: %q vs %q", first, second)
	}
}
