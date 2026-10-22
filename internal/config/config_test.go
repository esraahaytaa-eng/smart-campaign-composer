package config

import (
	"strings"
	"testing"
	"time"
)

func setEnv(t *testing.T, env map[string]string) {
	t.Helper()
	keys := []string{"DATABASE_URL", "HTTP_ADDR", "LLM_MODE", "ANTHROPIC_API_KEY", "ANTHROPIC_MODEL", "DEMO_MARKETER_PASSWORD",
		"LLM_TIMEOUT", "PROVIDER_DB_PASSWORD", "PROVIDER_SEED", "PROVIDER_URL", "MAX_ATTEMPTS"}
	for _, ch := range []string{"SMS", "EMAIL"} {
		for _, suffix := range []string{"_DELAY_MS_MIN", "_DELAY_MS_MAX", "_ERROR_RATE", "_RATE_LIMIT_RATE", "_TIMEOUT_RATE"} {
			keys = append(keys, ch+suffix)
		}
	}
	for _, k := range keys {
		t.Setenv(k, env[k])
	}
}

func TestLoadConfig_RealModeWithoutKeyFails(t *testing.T) {
	setEnv(t, map[string]string{"DATABASE_URL": "postgres://db", "LLM_MODE": "real"})

	_, err := Load("api")
	if err == nil || !strings.Contains(err.Error(), "ANTHROPIC_API_KEY") {
		t.Fatalf("want error naming ANTHROPIC_API_KEY, got %v", err)
	}
}

func TestLoadConfig_Defaults(t *testing.T) {
	setEnv(t, map[string]string{"DATABASE_URL": "postgres://db"})

	cfg, err := Load("api")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LLMMode != "mock" || cfg.AnthropicModel != "claude-opus-5-5" || cfg.HTTPAddr != ":8080" || cfg.LLMTimeout != 60*time.Second {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
}

func TestLoadConfig_RealModeWithKey(t *testing.T) {
	setEnv(t, map[string]string{"DATABASE_URL": "postgres://db", "LLM_MODE": "real", "ANTHROPIC_API_KEY": "k", "ANTHROPIC_MODEL": "m"})

	cfg, err := Load("api")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LLMMode != "real" || cfg.AnthropicAPIKey != "k" || cfg.AnthropicModel != "m" {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}

func TestLoadConfig_Errors(t *testing.T) {
	cases := []struct {
		name    string
		process string
		env     map[string]string
		want    string
	}{
		{"missing database url", "api", map[string]string{}, "DATABASE_URL"},
		{"migrate needs database url", "migrate", map[string]string{}, "DATABASE_URL"},
		{"migrate needs marketer password", "migrate", map[string]string{"DATABASE_URL": "postgres://db"}, "DEMO_MARKETER_PASSWORD"},
		{"marketer password too short", "migrate", map[string]string{"DATABASE_URL": "postgres://db", "DEMO_MARKETER_PASSWORD": "short"}, "DEMO_MARKETER_PASSWORD"},
		{"migrate needs provider password", "migrate", map[string]string{"DATABASE_URL": "postgres://db", "DEMO_MARKETER_PASSWORD": "demo-password"}, "PROVIDER_DB_PASSWORD"},
		{"provider rate over 1", "mock-provider", map[string]string{"DATABASE_URL": "postgres://db", "SMS_ERROR_RATE": "2"}, "SMS_ERROR_RATE"},
		{"provider rate negative", "mock-provider", map[string]string{"DATABASE_URL": "postgres://db", "EMAIL_TIMEOUT_RATE": "-0.1"}, "EMAIL_TIMEOUT_RATE"},
		{"provider rate not a number", "mock-provider", map[string]string{"DATABASE_URL": "postgres://db", "SMS_RATE_LIMIT_RATE": "often"}, "SMS_RATE_LIMIT_RATE"},
		{"provider rates over 1 together", "mock-provider", map[string]string{"DATABASE_URL": "postgres://db", "SMS_ERROR_RATE": "0.6", "SMS_RATE_LIMIT_RATE": "0.5"}, "SMS"},
		{"provider delay min over max", "mock-provider", map[string]string{"DATABASE_URL": "postgres://db", "EMAIL_DELAY_MS_MIN": "300", "EMAIL_DELAY_MS_MAX": "100"}, "EMAIL_DELAY_MS"},
		{"provider delay negative", "mock-provider", map[string]string{"DATABASE_URL": "postgres://db", "SMS_DELAY_MS_MIN": "-5"}, "SMS_DELAY_MS_MIN"},
		{"worker attempts zero", "worker", map[string]string{"DATABASE_URL": "postgres://db", "MAX_ATTEMPTS": "0"}, "MAX_ATTEMPTS"},
		{"worker attempts too many", "worker", map[string]string{"DATABASE_URL": "postgres://db", "MAX_ATTEMPTS": "11"}, "MAX_ATTEMPTS"},
		{"worker bad provider url", "worker", map[string]string{"DATABASE_URL": "postgres://db", "PROVIDER_URL": "mock-provider:8080"}, "PROVIDER_URL"},
		{"provider seed", "mock-provider", map[string]string{"DATABASE_URL": "postgres://db", "PROVIDER_SEED": "x"}, "PROVIDER_SEED"},
		{"bad llm timeout", "api", map[string]string{"DATABASE_URL": "postgres://db", "LLM_TIMEOUT": "soon"}, "LLM_TIMEOUT"},
		{"zero llm timeout", "api", map[string]string{"DATABASE_URL": "postgres://db", "LLM_TIMEOUT": "0s"}, "LLM_TIMEOUT"},
		{"unknown llm mode", "api", map[string]string{"DATABASE_URL": "postgres://db", "LLM_MODE": "fake"}, "LLM_MODE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setEnv(t, tc.env)
			if _, err := Load(tc.process); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error naming %s, got %v", tc.want, err)
			}
		})
	}
}

// The LLM settings only matter to the api; migrate must not fail on them.
func TestLoadConfig_MigrateIgnoresLLMSettings(t *testing.T) {
	setEnv(t, map[string]string{"DATABASE_URL": "postgres://db", "LLM_MODE": "real", "DEMO_MARKETER_PASSWORD": "demo-password", "PROVIDER_DB_PASSWORD": "provider-password"})

	cfg, err := Load("migrate")
	if err != nil {
		t.Fatalf("migrate should not need ANTHROPIC_API_KEY: %v", err)
	}
	if cfg.DemoMarketerPassword != "demo-password" {
		t.Fatalf("password not loaded: %+v", cfg)
	}
}

func TestLoadConfig_LLMTimeout(t *testing.T) {
	setEnv(t, map[string]string{"DATABASE_URL": "postgres://db", "LLM_TIMEOUT": "90s"})

	cfg, err := Load("api")
	if err != nil || cfg.LLMTimeout != 90*time.Second {
		t.Fatalf("want 90s, got %v (%v)", cfg.LLMTimeout, err)
	}
}

func TestLoadConfig_ProviderDefaults(t *testing.T) {
	setEnv(t, map[string]string{"DATABASE_URL": "postgres://db"})

	cfg, err := Load("mock-provider")
	if err != nil {
		t.Fatal(err)
	}
	// D20 defaults: 20–200 ms delay, 5% 5xx, 3% 429, 1% timeouts, for both channels.
	want := ProviderChannel{DelayMin: 20 * time.Millisecond, DelayMax: 200 * time.Millisecond, ErrorRate: 0.05, RateLimitRate: 0.03, TimeoutRate: 0.01}
	if cfg.Provider["sms"] != want || cfg.Provider["email"] != want || cfg.ProviderSeed != 1 {
		t.Fatalf("got %+v seed %d", cfg.Provider, cfg.ProviderSeed)
	}
}

func TestLoadConfig_ProviderOverrides(t *testing.T) {
	setEnv(t, map[string]string{"DATABASE_URL": "postgres://db", "SMS_ERROR_RATE": "0.5", "EMAIL_DELAY_MS_MIN": "0", "EMAIL_DELAY_MS_MAX": "10", "PROVIDER_SEED": "42"})

	cfg, err := Load("mock-provider")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Provider["sms"].ErrorRate != 0.5 || cfg.Provider["email"].DelayMax != 10*time.Millisecond || cfg.Provider["email"].DelayMin != 0 || cfg.ProviderSeed != 42 {
		t.Fatalf("got %+v seed %d", cfg.Provider, cfg.ProviderSeed)
	}
}

func TestLoadConfig_WorkerDefaults(t *testing.T) {
	setEnv(t, map[string]string{"DATABASE_URL": "postgres://db"})

	cfg, err := Load("worker")
	if err != nil || cfg.ProviderURL != "http://mock-provider:8080" || cfg.MaxAttempts != 5 {
		t.Fatalf("got %q, %d (%v)", cfg.ProviderURL, cfg.MaxAttempts, err)
	}
	setEnv(t, map[string]string{"DATABASE_URL": "postgres://db", "PROVIDER_URL": "http://localhost:9000/", "MAX_ATTEMPTS": "3"})
	if cfg, err = Load("worker"); err != nil || cfg.ProviderURL != "http://localhost:9000" || cfg.MaxAttempts != 3 {
		t.Fatalf("overrides: got %q, %d (%v)", cfg.ProviderURL, cfg.MaxAttempts, err)
	}
}
