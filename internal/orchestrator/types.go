package orchestrator

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/baiyideheimodao/super-agent-orchestrator/internal/config"
	"github.com/baiyideheimodao/super-agent-orchestrator/internal/llm"
)

const (
	StatusQueued          = "queued"
	StatusRunning         = "running"
	StatusWaiting         = "waiting"
	StatusCompleted       = "completed"
	StatusFailed          = "failed"
	StatusCancelled       = "cancelled"
	StatusTimedOut        = "timed_out"
	StatusBudgetExhausted = "budget_exhausted"
	StatusCancelling      = "cancelling"
)

const (
	CodeBudget          = "budget_exceeded"
	CodeBudgetLow       = "budget_low"
	CodeDepth           = "depth_exceeded"
	CodeMaxAgents       = "max_agents_reached"
	CodeDuplicate       = "duplicate_dispatch"
	CodeForced          = "forced_failure"
	CodeInvalidResult   = "invalid_result"
	CodeStepLimit       = "step_limit"
	CodeLLM             = "llm_error"
	CodeTimeout         = "timeout"
	CodeCancelled       = "run_cancelled"
	CodeInvalidTool     = "invalid_tool_call"
	CodeContextTooLarge = "context_too_large"
)

type Settings struct {
	MaxTokens      int `json:"max_tokens"`
	MaxDurationSec int `json:"max_duration_sec"`
	MaxDepth       int `json:"max_depth"`
	MaxConcurrency int `json:"max_concurrency"`
	MaxAgents      int `json:"max_agents"`
	AgentMaxSteps  int `json:"agent_max_steps"`
}

func SettingsFromConfig(cfg config.RuntimeConfig) Settings {
	return Settings{
		MaxTokens:      cfg.MaxTokens,
		MaxDurationSec: int(cfg.MaxDuration.Seconds()),
		MaxDepth:       cfg.MaxDepth,
		MaxConcurrency: cfg.MaxConcurrency,
		MaxAgents:      cfg.MaxAgents,
		AgentMaxSteps:  cfg.AgentMaxSteps,
	}
}

func (s Settings) Validate() error {
	switch {
	case s.MaxTokens < 200:
		return fmt.Errorf("max_tokens must be >= 200")
	case s.MaxDurationSec < 5:
		return fmt.Errorf("max_duration_sec must be >= 5")
	case s.MaxDepth < 0 || s.MaxDepth > 8:
		return fmt.Errorf("max_depth must be between 0 and 8")
	case s.MaxConcurrency < 1 || s.MaxConcurrency > 32:
		return fmt.Errorf("max_concurrency must be between 1 and 32")
	case s.MaxAgents < 1 || s.MaxAgents > 100:
		return fmt.Errorf("max_agents must be between 1 and 100")
	case s.AgentMaxSteps < 1 || s.AgentMaxSteps > 30:
		return fmt.Errorf("agent_max_steps must be between 1 and 30")
	}
	return nil
}

type ContextPacket struct {
	Goal        string   `json:"goal"`
	Facts       []string `json:"facts"`
	Upstream    []string `json:"upstream"`
	Constraints []string `json:"constraints"`
}

type AgentResult struct {
	Summary     string         `json:"summary"`
	Artifact    string         `json:"artifact"`
	SelfCheck   map[string]any `json:"self_check,omitempty"`
	FinalAnswer string         `json:"final_answer,omitempty"`
}

type UsageTotals struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

func (u *UsageTotals) Add(record llm.CallRecord) {
	u.InputTokens += record.InputTokens
	u.OutputTokens += record.OutputTokens
	u.TotalTokens += record.TotalTokens
}

type ToolTrace struct {
	ID         string    `json:"id"`
	AgentID    string    `json:"agent_id"`
	Name       string    `json:"name"`
	Args       string    `json:"args"`
	Result     string    `json:"result"`
	Status     string    `json:"status"`
	Error      string    `json:"error,omitempty"`
	ChildID    string    `json:"child_id,omitempty"`
	StartedAt  time.Time `json:"started_at"`
	DurationMS int64     `json:"duration_ms"`
}

type Agent struct {
	ID         string
	RunID      string
	ParentID   string
	Role       string
	Task       string
	Context    ContextPacket
	Depth      int
	Status     string
	Step       int
	CreatedAt  time.Time
	StartedAt  *time.Time
	EndedAt    *time.Time
	Usage      UsageTotals
	CostUSD    float64
	Messages   []llm.Message
	Calls      []llm.CallRecord
	Tools      []ToolTrace
	Result     *AgentResult
	Error      string
	ErrorCode  string
	Retryable  bool
	FinalText  string
	IsRoot     bool
	ChildCount int
}

type Run struct {
	ID          string
	Goal        string
	Status      string
	Settings    Settings
	CreatedAt   time.Time
	StartedAt   *time.Time
	EndedAt     *time.Time
	RootAgentID string
	FinalAnswer string
	Error       string
	StopReason  string
	BudgetHit   bool

	Usage    UsageTotals
	CostUSD  float64
	Reserved int

	Agents map[string]*Agent
	Order  []string

	ctx         context.Context
	cancel      context.CancelFunc
	gate        *Gate
	monitorStop chan struct{}
	failNext    bool
	dedupe      map[string]string
	attempts    map[string]int
	inFlight    map[string]string
	maxActive   int
	finished    bool
	cancelOnce  bool
}

func newID(prefix string) string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
	}
	return prefix + "_" + hex.EncodeToString(b[:])
}

func normalizeTask(task string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(task)), " "))
}

func isTerminal(status string) bool {
	switch status {
	case StatusCompleted, StatusFailed, StatusCancelled, StatusTimedOut, StatusBudgetExhausted:
		return true
	}
	return false
}
