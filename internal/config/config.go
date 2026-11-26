package config

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Config is the complete process configuration. All values can be supplied by
// environment variables; a local .env file is loaded first without overriding
// variables that are already present in the process environment.
type Config struct {
	Addr    string
	DataDir string
	Version string

	LLM     LLMConfig
	Runtime RuntimeConfig
}

type LLMConfig struct {
	Provider          string
	BaseURL           string
	APIKey            string
	Model             string
	MaxConcurrency    int
	Timeout           time.Duration
	InputUSDPerMTok   float64
	OutputUSDPerMTok  float64
	MaxOutputTokens   int
	MockStepLatencyMS int
}

type RuntimeConfig struct {
	MaxTokens       int
	MaxDuration     time.Duration
	MaxDepth        int
	MaxConcurrency  int
	MaxAgents       int
	AgentMaxSteps   int
	MaxContextChars int
}

func Load() (Config, error) {
	loadDotEnv(".env")

	cfg := Config{
		Addr:    env("ADDR", ":"+env("PORT", "8080")),
		DataDir: env("DATA_DIR", "data"),
		Version: env("APP_VERSION", "dev"),
		LLM: LLMConfig{
			Provider:          strings.ToLower(env("LLM_PROVIDER", "mock")),
			BaseURL:           strings.TrimRight(env("LLM_BASE_URL", "https://api.deepseek.com"), "/"),
			APIKey:            os.Getenv("LLM_API_KEY"),
			Model:             env("LLM_MODEL", "deepseek-chat"),
			MaxConcurrency:    envInt("LLM_MAX_CONCURRENCY", 4),
			Timeout:           time.Duration(envInt("LLM_TIMEOUT_SECONDS", 90)) * time.Second,
			InputUSDPerMTok:   envFloat("LLM_INPUT_USD_PER_MTOK", 0.27),
			OutputUSDPerMTok:  envFloat("LLM_OUTPUT_USD_PER_MTOK", 1.10),
			MaxOutputTokens:   envInt("LLM_MAX_OUTPUT_TOKENS", 2048),
			MockStepLatencyMS: envInt("MOCK_STEP_LATENCY_MS", 350),
		},
		Runtime: RuntimeConfig{
			MaxTokens:       envInt("RUN_MAX_TOKENS", 60000),
			MaxDuration:     time.Duration(envInt("RUN_MAX_DURATION_SECONDS", 600)) * time.Second,
			MaxDepth:        envInt("RUN_MAX_DEPTH", 2),
			MaxConcurrency:  envInt("RUN_MAX_CONCURRENCY", 4),
			MaxAgents:       envInt("RUN_MAX_AGENTS", 20),
			AgentMaxSteps:   envInt("AGENT_MAX_STEPS", 8),
			MaxContextChars: envInt("SPAWN_CONTEXT_MAX_CHARS", 6000),
		},
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	var problems []string
	if c.Addr == "" {
		problems = append(problems, "ADDR must not be empty")
	}
	switch c.LLM.Provider {
	case "mock", "openai":
	default:
		problems = append(problems, "LLM_PROVIDER must be mock or openai")
	}
	if c.LLM.MaxConcurrency < 1 {
		problems = append(problems, "LLM_MAX_CONCURRENCY must be >= 1")
	}
	if c.LLM.Timeout <= 0 {
		problems = append(problems, "LLM_TIMEOUT_SECONDS must be > 0")
	}
	if c.LLM.MaxOutputTokens < 64 {
		problems = append(problems, "LLM_MAX_OUTPUT_TOKENS must be >= 64")
	}
	if c.Runtime.MaxTokens < 100 {
		problems = append(problems, "RUN_MAX_TOKENS must be >= 100")
	}
	if c.Runtime.MaxDuration <= 0 {
		problems = append(problems, "RUN_MAX_DURATION_SECONDS must be > 0")
	}
	if c.Runtime.MaxDepth < 0 || c.Runtime.MaxDepth > 8 {
		problems = append(problems, "RUN_MAX_DEPTH must be between 0 and 8")
	}
	if c.Runtime.MaxConcurrency < 1 || c.Runtime.MaxConcurrency > 32 {
		problems = append(problems, "RUN_MAX_CONCURRENCY must be between 1 and 32")
	}
	if c.Runtime.AgentMaxSteps < 1 {
		problems = append(problems, "AGENT_MAX_STEPS must be >= 1")
	}
	if c.Runtime.MaxContextChars < 200 {
		problems = append(problems, "SPAWN_CONTEXT_MAX_CHARS must be >= 200")
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

func env(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return fallback
}

func envInt(key string, fallback int) int {
	v, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(v) == "" {
		return fallback
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return fallback
	}
	return n
}

func envFloat(key string, fallback float64) float64 {
	v, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(v) == "" {
		return fallback
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil {
		return fallback
	}
	return f
}

// loadDotEnv implements a deliberately small .env parser: KEY=VALUE, optional
// surrounding quotes, comments and blank lines. Existing environment variables
// always win so deployment configuration is never silently overwritten.
func loadDotEnv(path string) {
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		if key == "" {
			continue
		}
		if len(val) >= 2 {
			if (val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'') {
				val = val[1 : len(val)-1]
			}
		}
		if _, exists := os.LookupEnv(key); !exists {
			_ = os.Setenv(key, val)
		}
	}
}

func (c Config) String() string {
	return fmt.Sprintf("addr=%s provider=%s model=%s data=%s", c.Addr, c.LLM.Provider, c.LLM.Model, c.DataDir)
}
