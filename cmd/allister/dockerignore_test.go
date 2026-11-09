package main

import (
	"os"
	"strings"
	"testing"
)

// The owner's personal Yamtrack export must never enter the Docker build
// context (the Dockerfile does COPY . .).
func TestDockerignoreKeepsPersonalFilesOut(t *testing.T) {
	b, err := os.ReadFile("../../.dockerignore")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Fields(string(b))
	for _, want := range []string{"**/yamtrack_*.csv", "**/*:Zone.Identifier", ".superpowers", "docs", ".git"} {
		found := false
		for _, l := range lines {
			if l == want {
				found = true
			}
		}
		if !found {
			t.Errorf(".dockerignore lacks %q", want)
		}
	}
}
