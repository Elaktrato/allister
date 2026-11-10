package web

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestHtmxChecksumMatches verifies the vendored (and embedded, so served)
// htmx file against static/htmx.min.js.sha256 (sha256sum format), and that
// its version is the one PINS.md records. Updating htmx is a deliberate
// change of all three.
func TestHtmxChecksumMatches(t *testing.T) {
	js, err := staticFS.ReadFile("static/htmx.min.js")
	if err != nil {
		t.Fatal(err)
	}
	sumFile, err := staticFS.ReadFile("static/htmx.min.js.sha256")
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(string(sumFile))
	if len(fields) != 2 || fields[1] != "htmx.min.js" || !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(fields[0]) {
		t.Fatalf("htmx.min.js.sha256 is not `<sha256>  htmx.min.js`: %q", sumFile)
	}
	sum := sha256.Sum256(js)
	if got := hex.EncodeToString(sum[:]); got != fields[0] {
		t.Errorf("htmx.min.js sha256 = %s, recorded %s", got, fields[0])
	}

	v := regexp.MustCompile(`version:"([0-9.]+)"`).FindSubmatch(js)
	if v == nil {
		t.Fatal("no version in htmx.min.js")
	}
	pins, err := os.ReadFile("../../PINS.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(pins), "htmx "+string(v[1])) {
		t.Errorf("PINS.md does not record htmx %s", v[1])
	}
}
