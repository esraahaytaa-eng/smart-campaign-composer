// Package config reads each process's settings from environment variables.
package config

import (
	"errors"
	"fmt"
	"os"
)

type Config struct {
	DatabaseURL     string
	HTTPAddr        string
	LLMMode         string // "mock" or "real" (D24)
	AnthropicAPIKey string
	AnthropicModel  string
}

// Load reads the configuration for the given process (migrate, api, ...).
// Only the api uses the LLM, so only the api validates its settings.
func Load(process string) (Config, error) {
	cfg := Config{
		DatabaseURL:     os.Getenv("DATABASE_URL"),
		HTTPAddr:        getenv("HTTP_ADDR", ":8080"),
		LLMMode:         getenv("LLM_MODE", "mock"),
		AnthropicAPIKey: os.Getenv("ANTHROPIC_API_KEY"),
		AnthropicModel:  getenv("ANTHROPIC_MODEL", "claude-opus-5-5"),
	}
	if cfg.DatabaseURL == "" {
		return Config{}, errors.New("DATABASE_URL is required")
	}
	if process != "api" {
		return cfg, nil
	}
	switch cfg.LLMMode {
	case "mock":
	case "real":
		if cfg.AnthropicAPIKey == "" {
			return Config{}, errors.New("LLM_MODE=real requires ANTHROPIC_API_KEY to be set")
		}
	default:
		return Config{}, fmt.Errorf("LLM_MODE must be mock or real, got %q", cfg.LLMMode)
	}
	return cfg, nil
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
