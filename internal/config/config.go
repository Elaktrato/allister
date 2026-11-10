// Package config loads runtime configuration from the environment.
package config

import "os"

type Config struct {
	Port    string
	DataDir string
}

func Load() Config {
	return Config{
		Port:    env("PORT", "8080"),
		DataDir: env("DATA_DIR", "/data"),
	}
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
