package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/baiyideheimodao/super-agent-orchestrator/internal/config"
)

func testConfig() config.Config {
	return config.Config{
		Addr:    ":0",
		Version: "test",
		LLM: config.LLMConfig{
			Provider: "mock", Model: "mock-model", MaxConcurrency: 4, Timeout: 3 * time.Second,
			MaxOutputTokens: 2048, MockStepLatencyMS: 0,
		},
		Runtime: config.RuntimeConfig{
			MaxTokens: 60000, MaxDuration: 60 * time.Second, MaxDepth: 2, MaxConcurrency: 4,
			MaxAgents: 20, AgentMaxSteps: 8, MaxContextChars: 6000,
		},
	}
}

func newTestServer(t *testing.T) *Server {
	t.Helper()
	s, err := New(testConfig(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestHealth(t *testing.T) {
	s := newTestServer(t)
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
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/does-not-exist", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestProductionStaticServing(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!doctype html><h1>SPA</h1>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "assets", "app.js"), []byte("console.log('ok')"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := testConfig()
	cfg.WebDir = dir
	s, err := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}

	for path, want := range map[string]string{
		"/":              "SPA",
		"/assets/app.js": "console.log('ok')",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("%s status=%d body=%q", path, rec.Code, rec.Body.String())
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/missing.js", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing file status = %d", rec.Code)
	}
}
