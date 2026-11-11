// Package config loads runtime configuration from the environment.
package config

import "os"

type Config struct {
	Port           string
	DataDir        string
	GoogleBooksKey string
}

func Load() Config {
	return Config{
		Port:           env("PORT", "8080"),
		DataDir:        env("DATA_DIR", "/data"),
		GoogleBooksKey: env("GOOGLE_BOOKS_API_KEY", ""),
	}
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
