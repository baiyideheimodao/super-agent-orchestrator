package llm

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync/atomic"
	"time"

	"github.com/baiyideheimodao/super-agent-orchestrator/internal/config"
)

type Provider interface {
	Chat(ctx context.Context, req ChatRequest) (ChatResponse, error)
	Name() string
}

// Client applies cross-agent concurrency limits, per-call timeouts, usage
// accounting and cost calculation. A single Client is shared by every Agent in
// a run, which makes the configured concurrency ceiling global.
type Client struct {
	provider Provider
	cfg      config.LLMConfig
	sem      chan struct{}
	active   atomic.Int64
	record   func(CallRecord)
	seq      atomic.Int64
}

func NewClient(cfg config.LLMConfig, record func(CallRecord)) (*Client, error) {
	provider, err := NewProvider(cfg)
	if err != nil {
		return nil, err
	}
	if cfg.MaxConcurrency < 1 {
		return nil, errors.New("LLM max concurrency must be >= 1")
	}
	return &Client{
		provider: provider,
		cfg:      cfg,
		sem:      make(chan struct{}, cfg.MaxConcurrency),
		record:   record,
	}, nil
}

func NewProvider(cfg config.LLMConfig) (Provider, error) {
	switch cfg.Provider {
	case "mock":
		return NewMockProvider(time.Duration(cfg.MockStepLatencyMS) * time.Millisecond), nil
	case "openai":
		return NewOpenAIProvider(cfg)
	default:
		return nil, fmt.Errorf("unsupported LLM provider %q", cfg.Provider)
	}
}

func (c *Client) ProviderName() string { return c.provider.Name() }

func (c *Client) Chat(ctx context.Context, req ChatRequest, meta CallMeta) (ChatResponse, error) {
	select {
	case c.sem <- struct{}{}:
	case <-ctx.Done():
		return ChatResponse{}, fmt.Errorf("wait for LLM slot: %w", ctx.Err())
	}
	defer func() { <-c.sem }()

	active := c.active.Add(1)
	defer c.active.Add(-1)

	callCtx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()

	started := time.Now()
	if req.Model == "" {
		req.Model = c.cfg.Model
	}
	if req.MaxTokens <= 0 || req.MaxTokens > c.cfg.MaxOutputTokens {
		req.MaxTokens = c.cfg.MaxOutputTokens
	}

	resp, err := c.provider.Chat(callCtx, req)
	duration := time.Since(started)

	record := CallRecord{
		ID:               fmt.Sprintf("%s-call-%d", meta.AgentID, c.seq.Add(1)),
		RunID:            meta.RunID,
		AgentID:          meta.AgentID,
		Role:             meta.Role,
		Step:             meta.Step,
		Provider:         c.provider.Name(),
		Model:            req.Model,
		DurationMS:       duration.Milliseconds(),
		StartedAt:        started.UTC(),
		MessageCount:     len(req.Messages),
		ToolCallCount:    len(resp.ToolCalls),
		ConcurrentActive: int(active),
	}
	if err != nil {
		if errors.Is(callCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
			err = fmt.Errorf("LLM call timed out after %s: %w", c.cfg.Timeout, err)
		}
		record.Error = err.Error()
		if c.record != nil {
			c.record(record)
		}
		return ChatResponse{}, err
	}
	record.InputTokens = resp.Usage.PromptTokens
	record.OutputTokens = resp.Usage.CompletionTokens
	record.TotalTokens = resp.Usage.TotalTokens
	if record.TotalTokens == 0 {
		record.TotalTokens = record.InputTokens + record.OutputTokens
	}
	record.CostUSD = Cost(c.cfg, record.InputTokens, record.OutputTokens)
	record.FinishReason = resp.FinishReason
	if c.record != nil {
		c.record(record)
	}
	return resp, nil
}

func Cost(cfg config.LLMConfig, inputTokens, outputTokens int) float64 {
	cost := float64(inputTokens)/1_000_000*cfg.InputUSDPerMTok + float64(outputTokens)/1_000_000*cfg.OutputUSDPerMTok
	return math.Round(cost*1e8) / 1e8
}
