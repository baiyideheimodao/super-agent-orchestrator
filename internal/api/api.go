package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/baiyideheimodao/super-agent-orchestrator/internal/config"
)

// Server owns all HTTP routes. Feature-specific fields are added by the
// orchestrator and streaming layers without changing the health contract.
type Server struct {
	cfg       config.Config
	mux       *http.ServeMux
	startedAt time.Time
	log       *slog.Logger
}

func New(cfg config.Config, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{
		cfg:       cfg,
		mux:       http.NewServeMux(),
		startedAt: time.Now().UTC(),
		log:       logger,
	}
	s.routes()
	return s
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /health", s.handleHealth)
	s.mux.HandleFunc("GET /api/health", s.handleHealth)
	s.mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"service": "super-agent-orchestrator",
			"health":  "/health",
			"version": s.cfg.Version,
		})
	})
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	w.Header().Set("X-Content-Type-Options", "nosniff")
	s.mux.ServeHTTP(w, r)
	s.log.Debug("request", "method", r.Method, "path", r.URL.Path, "duration", time.Since(start).String())
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":         "ok",
		"service":        "super-agent-orchestrator",
		"version":        s.cfg.Version,
		"time":           time.Now().UTC().Format(time.RFC3339Nano),
		"uptime_seconds": int(time.Since(s.startedAt).Seconds()),
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("write json", "error", err)
	}
}
