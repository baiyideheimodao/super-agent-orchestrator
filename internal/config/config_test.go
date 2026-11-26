package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	for _, key := range []string{
		"ADDR", "PORT", "DATA_DIR", "APP_VERSION", "LLM_PROVIDER", "LLM_BASE_URL",
		"LLM_API_KEY", "LLM_MODEL", "LLM_MAX_CONCURRENCY", "LLM_TIMEOUT_SECONDS",
		"RUN_MAX_TOKENS", "RUN_MAX_DURATION_SECONDS", "RUN_MAX_DEPTH", "RUN_MAX_CONCURRENCY",
		"RUN_MAX_AGENTS", "AGENT_MAX_STEPS", "SPAWN_CONTEXT_MAX_CHARS",
	} {
		t.Setenv(key, "")
		os.Unsetenv(key)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Addr != ":8080" {
		t.Fatalf("Addr = %q, want :8080", cfg.Addr)
	}
	if cfg.LLM.Provider != "mock" {
		t.Fatalf("Provider = %q, want mock", cfg.LLM.Provider)
	}
	if cfg.Runtime.MaxDepth != 2 {
		t.Fatalf("MaxDepth = %d, want 2", cfg.Runtime.MaxDepth)
	}
	if cfg.LLM.Timeout != 90*time.Second {
		t.Fatalf("Timeout = %v, want 90s", cfg.LLM.Timeout)
	}
}

func TestValidateRejectsBadValues(t *testing.T) {
	cfg := Config{
		Addr: ":8080",
		LLM: LLMConfig{
			Provider: "mock", MaxConcurrency: 1, Timeout: time.Second, MaxOutputTokens: 128,
		},
		Runtime: RuntimeConfig{
			MaxTokens: 100, MaxDuration: time.Second, MaxDepth: 2, MaxConcurrency: 1,
			AgentMaxSteps: 1, MaxContextChars: 200,
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	cfg.Runtime.MaxDepth = 99
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for MaxDepth=99")
	}
}

func TestLoadDotEnvDoesNotOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	content := "LLM_MODEL=from-file\nLLM_PROVIDER=openai\nLLM_API_KEY=\"secret-value\"\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LLM_MODEL", "from-process")
	oldwd, _ := os.Getwd()
	defer os.Chdir(oldwd)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	os.Unsetenv("LLM_PROVIDER")
	os.Unsetenv("LLM_API_KEY")
	defer os.Unsetenv("LLM_PROVIDER")
	defer os.Unsetenv("LLM_API_KEY")

	loadDotEnv(".env")
	if got := os.Getenv("LLM_MODEL"); got != "from-process" {
		t.Fatalf("existing env overwritten: %q", got)
	}
	if got := os.Getenv("LLM_PROVIDER"); got != "openai" {
		t.Fatalf("LLM_PROVIDER = %q", got)
	}
	if got := os.Getenv("LLM_API_KEY"); got != "secret-value" {
		t.Fatalf("quoted value not parsed: %q", got)
	}
}
