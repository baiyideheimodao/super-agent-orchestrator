package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/baiyideheimodao/super-agent-orchestrator/internal/orchestrator"
)

func postJSON(t *testing.T, client *http.Client, url string, payload any) *http.Response {
	t.Helper()
	raw, _ := json.Marshal(payload)
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestRunLifecycleAPI(t *testing.T) {
	s := newTestServer(t)
	ts := httptest.NewServer(s)
	defer ts.Close()

	settings := orchestrator.Settings{
		MaxTokens: 60000, MaxDurationSec: 60, MaxDepth: 2,
		MaxConcurrency: 4, MaxAgents: 20, AgentMaxSteps: 8,
	}
	resp := postJSON(t, ts.Client(), ts.URL+"/api/runs", map[string]any{
		"goal": "策划一部 10 集悬疑短剧", "settings": settings,
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body = %s", resp.StatusCode, body)
	}
	var created orchestrator.RunView
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || created.RootAgentID == "" {
		t.Fatalf("bad created run: %+v", created)
	}

	deadline := time.Now().Add(5 * time.Second)
	var run orchestrator.RunView
	for time.Now().Before(deadline) {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/runs/"+created.ID, nil)
		getResp, err := ts.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = json.NewDecoder(getResp.Body).Decode(&run)
		getResp.Body.Close()
		if run.Status == "completed" || run.Status == "failed" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if run.Status != "completed" {
		t.Fatalf("run status = %s, error=%s", run.Status, run.Error)
	}
	if len(run.Agents) < 8 || run.FinalAnswer == "" {
		t.Fatalf("incomplete run: agents=%d final=%q", len(run.Agents), run.FinalAnswer)
	}

	var child orchestrator.AgentView
	for _, agent := range run.Agents {
		if !agent.IsRoot {
			child = agent
			break
		}
	}
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/runs/"+created.ID+"/agents/"+child.ID, nil)
	agentResp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer agentResp.Body.Close()
	if agentResp.StatusCode != http.StatusOK {
		t.Fatalf("agent status = %d", agentResp.StatusCode)
	}
	var detail orchestrator.AgentDetail
	if err := json.NewDecoder(agentResp.Body).Decode(&detail); err != nil {
		t.Fatal(err)
	}
	if len(detail.Messages) == 0 || detail.Context.Goal == "" {
		t.Fatalf("agent detail lacks isolated context: %+v", detail)
	}
	for _, message := range detail.Messages {
		if strings.Contains(message.Content, "总任务：") {
			t.Fatal("child leaked parent conversation")
		}
	}
}

func TestRunValidationErrors(t *testing.T) {
	s := newTestServer(t)
	ts := httptest.NewServer(s)
	defer ts.Close()

	resp := postJSON(t, ts.Client(), ts.URL+"/api/runs", map[string]any{"goal": "x"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/runs/not-real", nil)
	getResp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer getResp.Body.Close()
	if getResp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing run status = %d, want 404", getResp.StatusCode)
	}
}

func TestConfigEndpointDoesNotLeakKeys(t *testing.T) {
	s := newTestServer(t)
	ts := httptest.NewServer(s)
	defer ts.Close()
	resp, err := ts.Client().Get(ts.URL + "/api/config")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if bytes.Contains(body, []byte("api_key")) || bytes.Contains(body, []byte("API_KEY")) {
		t.Fatalf("config leaked key material: %s", body)
	}
	if !bytes.Contains(body, []byte("default_settings")) {
		t.Fatalf("config missing defaults: %s", body)
	}
}

func TestSSEHistoryContainsRunEvents(t *testing.T) {
	s := newTestServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)
	ts := httptest.NewServer(s)
	defer ts.Close()

	settings := orchestrator.Settings{
		MaxTokens: 60000, MaxDurationSec: 60, MaxDepth: 1,
		MaxConcurrency: 2, MaxAgents: 5, AgentMaxSteps: 4,
	}
	resp := postJSON(t, ts.Client(), ts.URL+"/api/runs", map[string]any{
		"goal": "测试 SSE 事件", "settings": settings,
	})
	resp.Body.Close()

	reqCtx, reqCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer reqCancel()
	req, _ := http.NewRequestWithContext(reqCtx, http.MethodGet, ts.URL+"/api/stream", nil)
	streamResp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer streamResp.Body.Close()

	scanner := bufio.NewScanner(streamResp.Body)
	found := false
	for scanner.Scan() {
		if strings.Contains(scanner.Text(), "event: run.created") {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("run.created event not replayed on SSE")
	}
}
