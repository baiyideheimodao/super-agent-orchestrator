package orchestrator

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/baiyideheimodao/super-agent-orchestrator/internal/llm"
)

// scriptedProvider exercises orchestration rules without depending on the
// demo script.
type scriptedProvider struct {
	root func(step int) llm.ChatResponse
}

func (p *scriptedProvider) Name() string { return "scripted" }
func (p *scriptedProvider) Chat(_ context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	role := "unknown"
	for _, message := range req.Messages {
		if message.Role != "system" {
			continue
		}
		if idx := strings.Index(message.Content, "[[role:"); idx >= 0 {
			rest := message.Content[idx+len("[[role:"):]
			if end := strings.IndexByte(rest, ']'); end >= 0 {
				role = rest[:end]
			}
		}
	}
	if role != "showrunner" {
		args, _ := json.Marshal(map[string]any{
			"summary":  "scripted child summary",
			"artifact": "scripted child artifact with enough length",
			"self_check": map[string]any{
				"complete": true, "notes": "scripted self check",
			},
		})
		return llm.ChatResponse{
			ToolCalls: []llm.ToolCall{{
				ID: "submit", Type: "function",
				Function: llm.FunctionCall{Name: "submit_result", Arguments: string(args)},
			}},
			Usage: llm.Usage{PromptTokens: 20, CompletionTokens: 10, TotalTokens: 30}, FinishReason: "tool_calls",
		}, nil
	}
	assistants := 0
	for _, message := range req.Messages {
		if message.Role == "assistant" {
			assistants++
		}
	}
	return p.root(assistants + 1), nil
}

func installScriptedProvider(t *testing.T, m *Manager, provider llm.Provider) {
	t.Helper()
	client, err := llm.NewClientWithProvider(provider, m.cfg.LLM, m.recordCall)
	if err != nil {
		t.Fatal(err)
	}
	m.llm = client
}

func makeSpawnArgs(role, task string, context ContextPacket) string {
	raw, _ := json.Marshal(map[string]any{"role": role, "task": task, "context": context})
	return string(raw)
}

func TestDuplicateDispatchIsBlocked(t *testing.T) {
	m := newTestManager(t)
	args := makeSpawnArgs("writer", "完全相同的任务", ContextPacket{
		Goal: "同一个目标", Facts: []string{"同一个事实"}, Constraints: []string{"同一约束"},
	})
	provider := &scriptedProvider{root: func(step int) llm.ChatResponse {
		if step == 1 {
			return llm.ChatResponse{
				ToolCalls: []llm.ToolCall{
					{ID: "a", Type: "function", Function: llm.FunctionCall{Name: "spawn_agent", Arguments: args}},
					{ID: "b", Type: "function", Function: llm.FunctionCall{Name: "spawn_agent", Arguments: args}},
				},
				Usage: llm.Usage{PromptTokens: 30, CompletionTokens: 20, TotalTokens: 50}, FinishReason: "tool_calls",
			}
		}
		finish, _ := json.Marshal(map[string]string{"final_answer": "重复派发已被阻止，保留一个子 Agent 的结果。"})
		return llm.ChatResponse{
			ToolCalls: []llm.ToolCall{{ID: "fin", Type: "function", Function: llm.FunctionCall{Name: "finish", Arguments: string(finish)}}},
			Usage:     llm.Usage{PromptTokens: 40, CompletionTokens: 20, TotalTokens: 60}, FinishReason: "tool_calls",
		}
	}}
	installScriptedProvider(t, m, provider)

	settings := testSettings()
	view, err := m.CreateRun("测试重复派发", &settings)
	if err != nil {
		t.Fatal(err)
	}
	view = waitRun(t, m, view.ID, 5*time.Second)
	if view.Status != StatusCompleted {
		t.Fatalf("status = %s, error = %s", view.Status, view.Error)
	}
	if view.TotalAgents != 2 {
		t.Fatalf("agents = %d, want root + one child", view.TotalAgents)
	}
	root, ok := m.GetAgent(view.ID, view.RootAgentID)
	if !ok {
		t.Fatal("root missing")
	}
	duplicates := 0
	for _, tool := range root.Tools {
		if strings.Contains(tool.Result, CodeDuplicate) {
			duplicates++
		}
	}
	if duplicates != 1 {
		t.Fatalf("duplicate rejections = %d, want 1; tools=%+v", duplicates, root.Tools)
	}
}

func TestOversizedContextIsRejectedBeforeSpawn(t *testing.T) {
	m := newTestManager(t)
	facts := make([]string, 12)
	for i := range facts {
		facts[i] = strings.Repeat("事实", 150)
	}
	upstream := make([]string, 8)
	for i := range upstream {
		upstream[i] = strings.Repeat("上游", 250)
	}
	args := makeSpawnArgs("writer", "尝试携带过大的上下文", ContextPacket{
		Goal: "测试上下文预算", Facts: facts, Upstream: upstream, Constraints: []string{"不得超限"},
	})
	provider := &scriptedProvider{root: func(step int) llm.ChatResponse {
		if step == 1 {
			return llm.ChatResponse{
				ToolCalls: []llm.ToolCall{{ID: "big", Type: "function", Function: llm.FunctionCall{Name: "spawn_agent", Arguments: args}}},
				Usage:     llm.Usage{PromptTokens: 30, CompletionTokens: 20, TotalTokens: 50}, FinishReason: "tool_calls",
			}
		}
		finish, _ := json.Marshal(map[string]string{"final_answer": "超大上下文被拒绝，未创建子 Agent。"})
		return llm.ChatResponse{
			ToolCalls: []llm.ToolCall{{ID: "fin", Type: "function", Function: llm.FunctionCall{Name: "finish", Arguments: string(finish)}}},
			Usage:     llm.Usage{PromptTokens: 40, CompletionTokens: 20, TotalTokens: 60}, FinishReason: "tool_calls",
		}
	}}
	installScriptedProvider(t, m, provider)

	settings := testSettings()
	view, err := m.CreateRun("测试上下文超限", &settings)
	if err != nil {
		t.Fatal(err)
	}
	view = waitRun(t, m, view.ID, 5*time.Second)
	if view.TotalAgents != 1 {
		t.Fatalf("agents = %d, want only root", view.TotalAgents)
	}
	root, _ := m.GetAgent(view.ID, view.RootAgentID)
	found := false
	for _, tool := range root.Tools {
		if strings.Contains(tool.Result, CodeContextTooLarge) {
			found = true
		}
	}
	if !found {
		t.Fatalf("context_too_large not recorded: %+v", root.Tools)
	}
}
