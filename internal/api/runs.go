package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/baiyideheimodao/super-agent-orchestrator/internal/orchestrator"
)

type createRunRequest struct {
	Goal     string                 `json:"goal"`
	Settings *orchestrator.Settings `json:"settings,omitempty"`
}

type failNextRequest struct {
	Enabled bool `json:"enabled"`
}

func (s *Server) handleConfig(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"provider":            s.manager.Provider(),
		"model":               s.cfg.LLM.Model,
		"default_settings":    s.manager.DefaultSettings(),
		"max_context_chars":   s.cfg.Runtime.MaxContextChars,
		"llm_max_concurrency": s.cfg.LLM.MaxConcurrency,
		"pricing": map[string]any{
			"input_usd_per_mtok":  s.cfg.LLM.InputUSDPerMTok,
			"output_usd_per_mtok": s.cfg.LLM.OutputUSDPerMTok,
		},
	})
}

func (s *Server) handleCreateRun(w http.ResponseWriter, r *http.Request) {
	var req createRunRequest
	if err := decodeJSON(r, &req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}
	view, err := s.manager.CreateRun(req.Goal, req.Settings)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_run", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, view)
}

func (s *Server) handleListRuns(w http.ResponseWriter, _ *http.Request) {
	runs := s.manager.ListRuns()
	if runs == nil {
		runs = []orchestrator.RunSummary{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": runs})
}

func (s *Server) handleGetRun(w http.ResponseWriter, r *http.Request) {
	view, ok := s.manager.GetRun(r.PathValue("id"))
	if !ok {
		writeAPIError(w, http.StatusNotFound, "run_not_found", "任务不存在")
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) handleGetAgent(w http.ResponseWriter, r *http.Request) {
	detail, ok := s.manager.GetAgent(r.PathValue("id"), r.PathValue("agentID"))
	if !ok {
		writeAPIError(w, http.StatusNotFound, "agent_not_found", "Agent 不存在")
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var settings orchestrator.Settings
	if err := decodeJSON(r, &settings); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}
	view, err := s.manager.UpdateSettings(r.PathValue("id"), settings)
	if err != nil {
		writeManagerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) handleFailNext(w http.ResponseWriter, r *http.Request) {
	var req failNextRequest
	if err := decodeJSON(r, &req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}
	view, err := s.manager.SetFailNext(r.PathValue("id"), req.Enabled)
	if err != nil {
		writeManagerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) handleCancel(w http.ResponseWriter, r *http.Request) {
	view, err := s.manager.Cancel(r.PathValue("id"), "user")
	if err != nil {
		writeManagerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func decodeJSON(r *http.Request, dst any) error {
	defer r.Body.Close()
	decoder := json.NewDecoder(io.LimitReader(r.Body, 2<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("request body must contain a single JSON object")
	}
	return nil
}

func writeManagerError(w http.ResponseWriter, err error) {
	message := err.Error()
	status := http.StatusBadRequest
	switch {
	case strings.Contains(message, "not found"):
		status = http.StatusNotFound
	case strings.Contains(message, "already ended"):
		status = http.StatusConflict
	}
	code := "orchestrator_error"
	if status == http.StatusNotFound {
		code = "run_not_found"
	} else if status == http.StatusConflict {
		code = "run_ended"
	}
	writeAPIError(w, status, code, message)
}

func writeAPIError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]string{"code": code, "message": message},
	})
}
