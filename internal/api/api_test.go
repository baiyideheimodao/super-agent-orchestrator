package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/baiyideheimodao/super-agent-orchestrator/internal/config"
)

func testConfig() config.Config {
	return config.Config{
		Addr:    ":0",
		Version: "test",
		LLM: config.LLMConfig{
			Provider: "mock", MaxConcurrency: 2, Timeout: 1, MaxOutputTokens: 128,
		},
		Runtime: config.RuntimeConfig{
			MaxTokens: 1000, MaxDuration: 1, MaxDepth: 2, MaxConcurrency: 2,
			MaxAgents: 10, AgentMaxSteps: 3, MaxContextChars: 500,
		},
	}
}

func TestHealth(t *testing.T) {
	s := New(testConfig(), slog.New(slog.NewTextHandler(httptest.NewRecorder(), nil)))
	for _, path := range []string{"/health", "/api/health"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want 200", path, rec.Code)
		}
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s invalid JSON: %v", path, err)
		}
		if body["status"] != "ok" || body["service"] != "super-agent-orchestrator" {
			t.Fatalf("%s unexpected body: %v", path, body)
		}
	}
}

func TestUnknownPathIs404(t *testing.T) {
	s := New(testConfig(), slog.Default())
	req := httptest.NewRequest(http.MethodGet, "/does-not-exist", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
