package config

import (
	"strings"
	"testing"
)

func setEnv(t *testing.T, env map[string]string) {
	t.Helper()
	for _, k := range []string{"DATABASE_URL", "HTTP_ADDR", "LLM_MODE", "ANTHROPIC_API_KEY", "ANTHROPIC_MODEL"} {
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
	if cfg.LLMMode != "mock" || cfg.AnthropicModel != "claude-opus-5-5" || cfg.HTTPAddr != ":8080" {
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
	setEnv(t, map[string]string{"DATABASE_URL": "postgres://db", "LLM_MODE": "real"})

	if _, err := Load("migrate"); err != nil {
		t.Fatalf("migrate should not need ANTHROPIC_API_KEY: %v", err)
	}
}
