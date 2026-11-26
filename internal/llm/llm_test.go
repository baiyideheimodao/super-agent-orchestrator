package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/baiyideheimodao/super-agent-orchestrator/internal/config"
)

func testLLMConfig() config.LLMConfig {
	return config.LLMConfig{
		Provider: "mock", Model: "mock-model", MaxConcurrency: 2, Timeout: time.Second,
		MaxOutputTokens: 512, InputUSDPerMTok: 1, OutputUSDPerMTok: 2,
	}
}

func TestMockShowrunnerSpawnsInitialWave(t *testing.T) {
	provider := NewMockProvider(0)
	req := ChatRequest{
		Model: "mock-model",
		Messages: []Message{
			{Role: "system", Content: "You are the orchestrator. [[role:showrunner]]"},
			{Role: "user", Content: "策划 10 集悬疑短剧"},
		},
		Tools: []Tool{{Type: "function", Function: FunctionDef{Name: "spawn_agent"}}, {Type: "function", Function: FunctionDef{Name: "finish"}}},
	}
	resp, err := provider.Chat(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.ToolCalls) != 2 {
		t.Fatalf("tool calls = %d, want 2", len(resp.ToolCalls))
	}
	roles := map[string]bool{}
	for _, call := range resp.ToolCalls {
		if call.Function.Name != "spawn_agent" {
			t.Fatalf("tool = %s", call.Function.Name)
		}
		var args struct {
			Role string `json:"role"`
		}
		if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
			t.Fatal(err)
		}
		roles[args.Role] = true
	}
	if !roles["story_architect"] || !roles["character_designer"] {
		t.Fatalf("roles = %v", roles)
	}
	if resp.Usage.PromptTokens == 0 || resp.Usage.CompletionTokens == 0 {
		t.Fatalf("usage = %+v", resp.Usage)
	}
}

func TestMockArchitectUsesNestedToolThenSubmits(t *testing.T) {
	provider := NewMockProvider(0)
	req := ChatRequest{
		Messages: []Message{
			{Role: "system", Content: "[[role:story_architect]]"},
			{Role: "user", Content: "设计总体结构"},
		},
		Tools: []Tool{
			{Type: "function", Function: FunctionDef{Name: "spawn_agent"}},
			{Type: "function", Function: FunctionDef{Name: "submit_result"}},
		},
	}
	first, err := provider.Chat(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.ToolCalls) != 1 || first.ToolCalls[0].Function.Name != "spawn_agent" {
		t.Fatalf("first call = %+v", first.ToolCalls)
	}

	req.Messages = append(req.Messages,
		Message{Role: "assistant", ToolCalls: first.ToolCalls},
		Message{Role: "tool", ToolCallID: first.ToolCalls[0].ID, Content: `{"ok":true,"role":"trope_researcher","summary":"three tropes"}`},
	)
	second, err := provider.Chat(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.ToolCalls) != 1 || second.ToolCalls[0].Function.Name != "submit_result" {
		t.Fatalf("second call = %+v", second.ToolCalls)
	}
	if !strings.Contains(second.ToolCalls[0].Function.Arguments, "十集") {
		t.Fatalf("artifact missing expected content: %s", second.ToolCalls[0].Function.Arguments)
	}
}

type concurrencyProvider struct {
	active atomic.Int64
	max    atomic.Int64
	delay  time.Duration
}

func (p *concurrencyProvider) Name() string { return "test" }
func (p *concurrencyProvider) Chat(ctx context.Context, _ ChatRequest) (ChatResponse, error) {
	now := p.active.Add(1)
	for {
		old := p.max.Load()
		if now <= old || p.max.CompareAndSwap(old, now) {
			break
		}
	}
	defer p.active.Add(-1)
	select {
	case <-time.After(p.delay):
	case <-ctx.Done():
		return ChatResponse{}, ctx.Err()
	}
	return ChatResponse{Usage: Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}}, nil
}

func TestClientEnforcesConcurrencyLimit(t *testing.T) {
	provider := &concurrencyProvider{delay: 40 * time.Millisecond}
	cfg := testLLMConfig()
	cfg.MaxConcurrency = 2
	var records atomic.Int64
	client := &Client{
		provider: provider, cfg: cfg, sem: make(chan struct{}, 2),
		record: func(CallRecord) { records.Add(1) },
	}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := client.Chat(context.Background(), ChatRequest{}, CallMeta{AgentID: "a"}); err != nil {
				t.Errorf("chat: %v", err)
			}
		}()
	}
	wg.Wait()
	if got := provider.max.Load(); got > 2 {
		t.Fatalf("max concurrent = %d, want <= 2", got)
	}
	if records.Load() != 8 {
		t.Fatalf("records = %d, want 8", records.Load())
	}
}

type blockingProvider struct{}

func (p *blockingProvider) Name() string { return "test" }
func (p *blockingProvider) Chat(ctx context.Context, _ ChatRequest) (ChatResponse, error) {
	<-ctx.Done()
	return ChatResponse{}, ctx.Err()
}

func TestClientTimeoutIsRecorded(t *testing.T) {
	cfg := testLLMConfig()
	cfg.Timeout = 30 * time.Millisecond
	var got CallRecord
	client := &Client{
		provider: &blockingProvider{}, cfg: cfg, sem: make(chan struct{}, 1),
		record: func(r CallRecord) { got = r },
	}
	_, err := client.Chat(context.Background(), ChatRequest{}, CallMeta{AgentID: "agent-1", Role: "writer", Step: 1})
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("error = %v, want timeout", err)
	}
	if got.Error == "" || got.DurationMS < 20 {
		t.Fatalf("record = %+v", got)
	}
}

func TestCost(t *testing.T) {
	cfg := config.LLMConfig{InputUSDPerMTok: 0.27, OutputUSDPerMTok: 1.10}
	got := Cost(cfg, 1_000_000, 1_000_000)
	if got != 1.37 {
		t.Fatalf("cost = %v, want 1.37", got)
	}
	if Cost(cfg, 1000, 2000) <= 0 {
		t.Fatal("expected positive cost")
	}
}

func TestOpenAIProviderToolCall(t *testing.T) {
	var sawAuth, sawPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		sawPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"model":"test-model",
			"choices":[{"index":0,"message":{"role":"assistant","content":"",
				"tool_calls":[{"id":"call_1","type":"function","function":{"name":"spawn_agent","arguments":"{\"role\":\"writer\"}"}}]},
				"finish_reason":"tool_calls"}],
			"usage":{"prompt_tokens":21,"completion_tokens":12,"total_tokens":33}
		}`))
	}))
	defer server.Close()

	provider, err := NewOpenAIProvider(config.LLMConfig{
		Provider: "openai", BaseURL: server.URL + "/v1", APIKey: "test-key", Model: "test-model",
	})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := provider.Chat(context.Background(), ChatRequest{Messages: []Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	if sawAuth != "Bearer test-key" || sawPath != "/v1/chat/completions" {
		t.Fatalf("auth=%q path=%q", sawAuth, sawPath)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Function.Name != "spawn_agent" {
		t.Fatalf("tool calls = %+v", resp.ToolCalls)
	}
	if resp.Usage.TotalTokens != 33 {
		t.Fatalf("usage = %+v", resp.Usage)
	}
}

func TestOpenAIProviderErrorsOnHTTPFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":{"message":"bad key"}}`, http.StatusUnauthorized)
	}))
	defer server.Close()
	provider, err := NewOpenAIProvider(config.LLMConfig{BaseURL: server.URL, APIKey: "x"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = provider.Chat(context.Background(), ChatRequest{})
	if err == nil || !strings.Contains(err.Error(), "HTTP 401") {
		t.Fatalf("error = %v", err)
	}
}
