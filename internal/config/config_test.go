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

func TestLoadTMDBKey(t *testing.T) {
	t.Setenv("TMDB_API_KEY", "tmdb")
	if c := Load(); c.TMDBKey != "tmdb" {
		t.Fatalf("got %+v", c)
	}
}

func TestLoadGameSourceKeys(t *testing.T) {
	t.Setenv("IGDB_CLIENT_ID", "igdb-id")
	t.Setenv("IGDB_CLIENT_SECRET", "igdb-secret")
	t.Setenv("STEAM_API_KEY", "steam-key")
	c := Load()
	if c.IGDBClientID != "igdb-id" || c.IGDBClientSecret != "igdb-secret" || c.SteamAPIKey != "steam-key" {
		t.Fatalf("got %+v", c)
	}
}

func TestLoadGameSourceKeysDefaultToEmpty(t *testing.T) {
	t.Setenv("IGDB_CLIENT_ID", "")
	t.Setenv("IGDB_CLIENT_SECRET", "")
	t.Setenv("STEAM_API_KEY", "")
	c := Load()
	if c.IGDBClientID != "" || c.IGDBClientSecret != "" || c.SteamAPIKey != "" {
		t.Fatalf("got %+v", c)
	}
}
