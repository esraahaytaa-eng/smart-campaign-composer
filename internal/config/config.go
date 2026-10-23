// Package config reads each process's settings from environment variables.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	DatabaseURL     string
	HTTPAddr        string
	LLMMode         string // "mock" or "real" (D24)
	AnthropicAPIKey string
	AnthropicModel  string
	LLMTimeout      time.Duration // per AI call (D15)
	WebDir          string        // built frontend the api serves (T15); empty serves none

	DemoMarketerPassword string // migrate seeds the demo marketer logins with it (D10)
	ProviderDBPassword   string // migrate sets it on the mock provider's database role (D6)

	ProviderURL string // where the worker sends messages
	MaxAttempts int    // provider attempts per message before it fails (D7)

	Provider     map[string]ProviderChannel // mock provider behaviour per channel (D20)
	ProviderSeed int64                      // seeds the provider's failure injection, so runs repeat
}

// ProviderChannel is how the mock provider behaves on one channel. The rates
// are fractions of requests; the rest succeed.
type ProviderChannel struct {
	DelayMin, DelayMax time.Duration
	ErrorRate          float64 // 5xx
	RateLimitRate      float64 // 429 with Retry-After
	TimeoutRate        float64 // hang past the client's timeout
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
		WebDir:          os.Getenv("WEB_DIR"),

		DemoMarketerPassword: os.Getenv("DEMO_MARKETER_PASSWORD"),
		ProviderDBPassword:   os.Getenv("PROVIDER_DB_PASSWORD"),
	}
	if cfg.DatabaseURL == "" {
		return Config{}, errors.New("DATABASE_URL is required")
	}
	if process == "migrate" && len(cfg.DemoMarketerPassword) < 8 {
		return Config{}, errors.New("DEMO_MARKETER_PASSWORD must be at least 8 characters")
	}
	if process == "migrate" && (len(cfg.ProviderDBPassword) < 8 || strings.Trim(cfg.ProviderDBPassword, urlSafe) != "") {
		// It also goes into the provider's DATABASE_URL, so only URL-safe characters.
		return Config{}, errors.New("PROVIDER_DB_PASSWORD must be at least 8 characters of A-Z, a-z, 0-9, '.', '_', '~' or '-'")
	}
	if process == "mock-provider" {
		return loadProvider(cfg)
	}
	if process == "worker" {
		return loadWorker(cfg)
	}
	if process != "api" {
		return cfg, nil
	}
	timeout, err := time.ParseDuration(getenv("LLM_TIMEOUT", "60s"))
	if err != nil || timeout <= 0 {
		return Config{}, fmt.Errorf("LLM_TIMEOUT must be a positive duration such as 60s, got %q", os.Getenv("LLM_TIMEOUT"))
	}
	cfg.LLMTimeout = timeout
	// Without the build every page would be a 404 after a normal-looking start.
	if cfg.WebDir != "" {
		if _, err := os.Stat(filepath.Join(cfg.WebDir, "index.html")); err != nil {
			return Config{}, fmt.Errorf("WEB_DIR %q has no index.html: build the frontend or leave WEB_DIR empty", cfg.WebDir)
		}
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

const (
	maxProviderDelayMS = 30_000
	urlSafe            = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789._~-"
)

// loadProvider reads SMS_* and EMAIL_* behaviour with the D20 defaults.
func loadProvider(cfg Config) (Config, error) {
	seed, err := strconv.ParseInt(getenv("PROVIDER_SEED", "1"), 10, 64)
	if err != nil {
		return Config{}, fmt.Errorf("PROVIDER_SEED must be an integer: %w", err)
	}
	cfg.ProviderSeed = seed
	cfg.Provider = map[string]ProviderChannel{}
	for _, ch := range []string{"sms", "email"} {
		p := strings.ToUpper(ch) + "_"
		var c ProviderChannel
		var errs []error
		ms := func(key, def string) time.Duration {
			n, err := strconv.Atoi(getenv(p+key, def))
			// Below the provider's 1-minute stale-key takeover, or a slow request
			// and its retry could both be processed under one key.
			if err != nil || n < 0 || n > maxProviderDelayMS {
				errs = append(errs, fmt.Errorf("%s%s must be a whole number of milliseconds from 0 to %d", p, key, maxProviderDelayMS))
			}
			return time.Duration(n) * time.Millisecond
		}
		rate := func(key, def string) float64 {
			f, err := strconv.ParseFloat(getenv(p+key, def), 64)
			if err != nil || f < 0 || f > 1 {
				errs = append(errs, fmt.Errorf("%s%s must be a fraction between 0 and 1", p, key))
			}
			return f
		}
		c.DelayMin, c.DelayMax = ms("DELAY_MS_MIN", "20"), ms("DELAY_MS_MAX", "200")
		c.ErrorRate, c.RateLimitRate, c.TimeoutRate = rate("ERROR_RATE", "0.05"), rate("RATE_LIMIT_RATE", "0.03"), rate("TIMEOUT_RATE", "0.01")
		if len(errs) > 0 {
			return Config{}, errs[0]
		}
		if c.DelayMin > c.DelayMax {
			return Config{}, fmt.Errorf("%sDELAY_MS_MIN must not exceed %sDELAY_MS_MAX", p, p)
		}
		if c.ErrorRate+c.RateLimitRate+c.TimeoutRate > 1 {
			return Config{}, fmt.Errorf("%sERROR_RATE, %sRATE_LIMIT_RATE and %sTIMEOUT_RATE add up to more than 1", p, p, p)
		}
		cfg.Provider[ch] = c
	}
	return cfg, nil
}

func loadWorker(cfg Config) (Config, error) {
	u, err := url.Parse(getenv("PROVIDER_URL", "http://mock-provider:8080"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return Config{}, fmt.Errorf("PROVIDER_URL must be an http(s) URL such as http://mock-provider:8080, got %q", os.Getenv("PROVIDER_URL"))
	}
	cfg.ProviderURL = strings.TrimRight(u.String(), "/")
	n, err := strconv.Atoi(getenv("MAX_ATTEMPTS", "5"))
	if err != nil || n < 1 || n > 10 {
		return Config{}, errors.New("MAX_ATTEMPTS must be a whole number from 1 to 10")
	}
	cfg.MaxAttempts = n
	return cfg, nil
}
