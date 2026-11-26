package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/baiyideheimodao/super-agent-orchestrator/internal/config"
)

type OpenAIProvider struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

func NewOpenAIProvider(cfg config.LLMConfig) (*OpenAIProvider, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, fmt.Errorf("LLM_API_KEY is required when LLM_PROVIDER=openai")
	}
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return nil, fmt.Errorf("LLM_BASE_URL is required when LLM_PROVIDER=openai")
	}
	return &OpenAIProvider{
		baseURL: strings.TrimRight(cfg.BaseURL, "/"),
		apiKey:  cfg.APIKey,
		// The per-call context owns the timeout so the transport can be shared
		// safely while still honoring cancellation.
		http: &http.Client{Timeout: 0},
	}, nil
}

func (p *OpenAIProvider) Name() string { return "openai" }

type openAIResponse struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Index   int `json:"index"`
		Message struct {
			Role      string     `json:"role"`
			Content   string     `json:"content"`
			ToolCalls []ToolCall `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage Usage `json:"usage"`
}

func (p *OpenAIProvider) Chat(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return ChatResponse{}, fmt.Errorf("encode request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return ChatResponse{}, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)

	started := time.Now()
	resp, err := p.http.Do(httpReq)
	if err != nil {
		return ChatResponse{}, fmt.Errorf("chat completion request after %s: %w", time.Since(started).Round(time.Millisecond), err)
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return ChatResponse{}, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ChatResponse{}, fmt.Errorf("chat completion HTTP %d: %s", resp.StatusCode, truncate(string(payload), 600))
	}
	var decoded openAIResponse
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return ChatResponse{}, fmt.Errorf("decode response: %w", err)
	}
	if len(decoded.Choices) == 0 {
		return ChatResponse{}, fmt.Errorf("chat completion returned no choices")
	}
	choice := decoded.Choices[0]
	return ChatResponse{
		Content:      choice.Message.Content,
		ToolCalls:    choice.Message.ToolCalls,
		Usage:        decoded.Usage,
		FinishReason: choice.FinishReason,
		Model:        decoded.Model,
	}, nil
}

func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}
