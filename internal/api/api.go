package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/baiyideheimodao/super-agent-orchestrator/internal/config"
	"github.com/baiyideheimodao/super-agent-orchestrator/internal/orchestrator"
	"github.com/baiyideheimodao/super-agent-orchestrator/internal/stream"
)

// Server owns all HTTP routes. Feature-specific fields are added by the
// orchestrator and streaming layers without changing the health contract.
type Server struct {
	cfg       config.Config
	mux       *http.ServeMux
	startedAt time.Time
	log       *slog.Logger
	hub       *stream.Hub
	manager   *orchestrator.Manager
}

func New(cfg config.Config, logger *slog.Logger) (*Server, error) {
	if logger == nil {
		logger = slog.Default()
	}
	hub := stream.NewHub()
	manager, err := orchestrator.NewManager(cfg, hub, logger)
	if err != nil {
		return nil, fmt.Errorf("create orchestrator: %w", err)
	}
	s := &Server{
		cfg:       cfg,
		mux:       http.NewServeMux(),
		startedAt: time.Now().UTC(),
		log:       logger,
		hub:       hub,
		manager:   manager,
	}
	s.routes()
	return s, nil
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /health", s.handleHealth)
	s.mux.HandleFunc("GET /api/health", s.handleHealth)
	s.mux.HandleFunc("GET /api/stream", s.handleStream)
	s.mux.HandleFunc("GET /api/config", s.handleConfig)
	s.mux.HandleFunc("POST /api/runs", s.handleCreateRun)
	s.mux.HandleFunc("GET /api/runs", s.handleListRuns)
	s.mux.HandleFunc("GET /api/runs/{id}", s.handleGetRun)
	s.mux.HandleFunc("GET /api/runs/{id}/agents/{agentID}", s.handleGetAgent)
	s.mux.HandleFunc("PATCH /api/runs/{id}/settings", s.handleUpdateSettings)
	s.mux.HandleFunc("POST /api/runs/{id}/fail-next", s.handleFailNext)
	s.mux.HandleFunc("POST /api/runs/{id}/cancel", s.handleCancel)
	s.mux.HandleFunc("GET /", s.handleRoot)
}

// Start begins the global one-second SSE ticker. It is safe to call once per
// process; the supplied context controls shutdown.
func (s *Server) Start(ctx context.Context) {
	s.hub.Start(ctx, time.Second)
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Last-Event-ID")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	s.mux.ServeHTTP(w, r)
	s.log.Debug("request", "method", r.Method, "path", r.URL.Path, "duration", time.Since(start).String())
}

func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	afterSeq := int64(0)
	if raw := r.Header.Get("Last-Event-ID"); raw != "" {
		if parsed, err := strconv.ParseInt(raw, 10, 64); err == nil && parsed >= 0 {
			afterSeq = parsed
		}
	}

	ch, replay, unsubscribe := s.hub.Subscribe(afterSeq)
	defer unsubscribe()

	fmt.Fprint(w, "retry: 2000\n\n")
	writeRawEvent(w, "", "hello", map[string]any{
		"seq":             s.hub.CurrentSeq(),
		"interval_ms":     1000,
		"replayed_events": len(replay),
	})
	flusher.Flush()

	for _, event := range replay {
		if err := writeStreamEvent(w, event); err != nil {
			return
		}
	}
	flusher.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case event, open := <-ch:
			if !open {
				return
			}
			if err := writeStreamEvent(w, event); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func writeStreamEvent(w http.ResponseWriter, event stream.Event) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", event.Seq, event.Type, payload)
	return err
}

func writeRawEvent(w http.ResponseWriter, id, eventType string, payload any) {
	body, _ := json.Marshal(payload)
	if id != "" {
		fmt.Fprintf(w, "id: %s\n", id)
	}
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, body)
}

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	index := filepath.Join(s.cfg.WebDir, "index.html")
	if r.URL.Path == "/" {
		if s.cfg.WebDir != "" {
			if info, err := os.Stat(index); err == nil && !info.IsDir() {
				http.ServeFile(w, r, index)
				return
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"service": "super-agent-orchestrator",
			"health":  "/health",
			"version": s.cfg.Version,
		})
		return
	}
	if s.cfg.WebDir != "" {
		clean := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		full := filepath.Join(s.cfg.WebDir, filepath.FromSlash(clean))
		if info, err := os.Stat(full); err == nil && !info.IsDir() {
			if strings.HasPrefix(r.URL.Path, "/assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			http.ServeFile(w, r, full)
			return
		}
	}
	http.NotFound(w, r)
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
