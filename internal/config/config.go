// Package config reads each process's settings from environment variables.
package config

import (
	"errors"
	"fmt"
	"os"
	"time"
)

type Config struct {
	DatabaseURL     string
	HTTPAddr        string
	LLMMode         string // "mock" or "real" (D24)
	AnthropicAPIKey string
	AnthropicModel  string
	LLMTimeout      time.Duration // per AI call (D15)

	DemoMarketerPassword string // migrate seeds the demo marketer logins with it (D10)
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

		DemoMarketerPassword: os.Getenv("DEMO_MARKETER_PASSWORD"),
	}
	if cfg.DatabaseURL == "" {
		return Config{}, errors.New("DATABASE_URL is required")
	}
	if process == "migrate" && len(cfg.DemoMarketerPassword) < 8 {
		return Config{}, errors.New("DEMO_MARKETER_PASSWORD must be at least 8 characters")
	}
	if process != "api" {
		return cfg, nil
	}
	timeout, err := time.ParseDuration(getenv("LLM_TIMEOUT", "60s"))
	if err != nil || timeout <= 0 {
		return Config{}, fmt.Errorf("LLM_TIMEOUT must be a positive duration such as 60s, got %q", os.Getenv("LLM_TIMEOUT"))
	}
	cfg.LLMTimeout = timeout
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
