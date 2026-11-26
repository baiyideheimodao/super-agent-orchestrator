package llm

import "time"

type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	Name       string     `json:"name,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
}

type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type Tool struct {
	Type     string      `json:"type"`
	Function FunctionDef `json:"function"`
}

type FunctionDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type ChatRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Tools       []Tool    `json:"tools,omitempty"`
	Temperature float64   `json:"temperature,omitempty"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type ChatResponse struct {
	Content      string
	ToolCalls    []ToolCall
	Usage        Usage
	FinishReason string
	Model        string
}

type CallMeta struct {
	RunID   string
	AgentID string
	Role    string
	Step    int
}

type CallRecord struct {
	ID               string    `json:"id"`
	RunID            string    `json:"run_id"`
	AgentID          string    `json:"agent_id"`
	Role             string    `json:"role"`
	Step             int       `json:"step"`
	Provider         string    `json:"provider"`
	Model            string    `json:"model"`
	InputTokens      int       `json:"input_tokens"`
	OutputTokens     int       `json:"output_tokens"`
	TotalTokens      int       `json:"total_tokens"`
	CostUSD          float64   `json:"cost_usd"`
	DurationMS       int64     `json:"duration_ms"`
	StartedAt        time.Time `json:"started_at"`
	Error            string    `json:"error,omitempty"`
	FinishReason     string    `json:"finish_reason,omitempty"`
	MessageCount     int       `json:"message_count"`
	ToolCallCount    int       `json:"tool_call_count"`
	ConcurrentActive int       `json:"concurrent_active"`
}
