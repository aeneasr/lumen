package project

import "os"

// Config holds application configuration values.
type Config struct {
	DatabaseURL string
	Port        int
	Debug       bool
}

// LoadConfig reads configuration from environment variables.
func LoadConfig() *Config {
	return &Config{
		DatabaseURL: GetEnvWithDefault("DATABASE_URL", "postgres://localhost/app"),
		Port:        8080,
		Debug:       false,
	}
}

// GetEnvWithDefault returns an environment variable value or a default.
func GetEnvWithDefault(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}
