package config

import "testing"

func TestLoadDefaults(t *testing.T) {
	t.Setenv("PORT", "")
	t.Setenv("DATA_DIR", "")
	c := Load()
	if c.Port != "8080" || c.DataDir != "/data" {
		t.Fatalf("got %+v", c)
	}
}

func TestLoadEnv(t *testing.T) {
	t.Setenv("PORT", "9000")
	if c := Load(); c.Port != "9000" {
		t.Fatalf("got %+v", c)
	}
}

func TestLoadGoogleBooksKey(t *testing.T) {
	t.Setenv("GOOGLE_BOOKS_API_KEY", "abc")
	if c := Load(); c.GoogleBooksKey != "abc" {
		t.Fatalf("got %+v", c)
	}
}
