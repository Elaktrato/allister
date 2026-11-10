package backup

import (
	"os"
	"strings"
	"testing"
)

// TestDocsDescribeEveryItemCover keeps the README and the design spec in
// line with the archive: every local item cover is included, person photos
// are not, and a restore merges.
func TestDocsDescribeEveryItemCover(t *testing.T) {
	read := func(p string) string {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	spec := read("../../docs/superpowers/specs/2026-11-09-allister-plugins-design.md")
	for _, want := range []string{"the file of every item cover stored locally", "remote URL is not retained", "Person photos are not included"} {
		if !strings.Contains(spec, want) {
			t.Errorf("spec lacks %q", want)
		}
	}
	if strings.Contains(spec, "covers uploaded by the user for manual items") {
		t.Error("spec still limits the backup to uploaded covers")
	}
	readme := read("../../README.md")
	for _, want := range []string{"Settings > Backup", "every local item cover", "nothing is overwritten or deleted", "upload the zip"} {
		if !strings.Contains(readme, want) {
			t.Errorf("README lacks %q", want)
		}
	}
	if n := strings.Count(readme, "\n"); n > 150 {
		t.Errorf("README has %d lines, keep it under about 150", n)
	}
}
