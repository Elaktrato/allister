package version

import (
	"runtime"
	"testing"
)

// set overrides the link-time variables for one test.
func set(t *testing.T, v, c, d string) {
	t.Helper()
	ov, oc, od := Version, Commit, Date
	Version, Commit, Date = v, c, d
	t.Cleanup(func() { Version, Commit, Date = ov, oc, od })
}

func TestDefaultsWithoutLdflags(t *testing.T) {
	if Version != "dev" || Commit != "unknown" || Date != "unknown" {
		t.Fatalf("defaults: %q %q %q, want dev unknown unknown", Version, Commit, Date)
	}
	got := Get()
	if got.Version != "dev" || got.Go != runtime.Version() {
		t.Fatalf("Get() = %+v", got)
	}
}

func TestGetReportsLinkTimeValues(t *testing.T) {
	set(t, "v0.1.0", "0123456789abcdef0123456789abcdef01234567", "2026-11-10T12:00:00Z")
	got := Get()
	want := Info{Version: "v0.1.0", Commit: "0123456789abcdef0123456789abcdef01234567", Date: "2026-11-10T12:00:00Z", Go: runtime.Version()}
	if got != want {
		t.Fatalf("Get() = %+v, want %+v", got, want)
	}
}

func TestString(t *testing.T) {
	cases := []struct{ v, c, d, want string }{
		{"dev", "unknown", "unknown", "dev"},
		{"v0.1.0", "0123456789abcdef0123456789abcdef01234567", "2026-11-10T12:00:00Z", "v0.1.0 (0123456, 2026-11-10)"},
		{"v1.2.3-rc.1", "abc", "unknown", "v1.2.3-rc.1 (abc)"},
		{"v1.0.0", "unknown", "2026-01-02T03:04:05Z", "v1.0.0 (2026-01-02)"},
		// empty values (an -X with nothing after =) read as unknown
		{"", "", "", "dev"},
	}
	for _, c := range cases {
		set(t, c.v, c.c, c.d)
		if got := Get().String(); got != c.want {
			t.Errorf("%q %q %q: String() = %q, want %q", c.v, c.c, c.d, got, c.want)
		}
	}
}
