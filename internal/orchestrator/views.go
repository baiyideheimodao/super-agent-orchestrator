package orchestrator

import (
	"sort"
	"time"

	"github.com/baiyideheimodao/super-agent-orchestrator/internal/llm"
)

type RunSummary struct {
	ID          string    `json:"id"`
	Goal        string    `json:"goal"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
	TotalTokens int       `json:"total_tokens"`
	MaxTokens   int       `json:"max_tokens"`
	AgentCount  int       `json:"agent_count"`
}

type RunView struct {
	ID                     string      `json:"id"`
	Goal                   string      `json:"goal"`
	Status                 string      `json:"status"`
	Settings               Settings    `json:"settings"`
	CreatedAt              time.Time   `json:"created_at"`
	StartedAt              *time.Time  `json:"started_at,omitempty"`
	EndedAt                *time.Time  `json:"ended_at,omitempty"`
	RootAgentID            string      `json:"root_agent_id"`
	FinalAnswer            string      `json:"final_answer,omitempty"`
	Error                  string      `json:"error,omitempty"`
	StopReason             string      `json:"stop_reason,omitempty"`
	BudgetHit              bool        `json:"budget_hit"`
	Usage                  UsageTotals `json:"usage"`
	CostUSD                float64     `json:"cost_usd"`
	BudgetPercent          float64     `json:"budget_percent"`
	ElapsedMS              int64       `json:"elapsed_ms"`
	TotalAgents            int         `json:"total_agents"`
	ActiveAgents           int         `json:"active_agents"`
	MaxObservedConcurrency int         `json:"max_observed_concurrency"`
	FailNext               bool        `json:"fail_next"`
	Agents                 []AgentView `json:"agents"`
}

type AgentView struct {
	ID            string     `json:"id"`
	ParentID      string     `json:"parent_id,omitempty"`
	Role          string     `json:"role"`
	Task          string     `json:"task"`
	Depth         int        `json:"depth"`
	Status        string     `json:"status"`
	Step          int        `json:"step"`
	IsRoot        bool       `json:"is_root"`
	ChildCount    int        `json:"child_count"`
	CreatedAt     time.Time  `json:"created_at"`
	StartedAt     *time.Time `json:"started_at,omitempty"`
	EndedAt       *time.Time `json:"ended_at,omitempty"`
	DurationMS    int64      `json:"duration_ms"`
	InputTokens   int        `json:"input_tokens"`
	OutputTokens  int        `json:"output_tokens"`
	TotalTokens   int        `json:"total_tokens"`
	CostUSD       float64    `json:"cost_usd"`
	ErrorCode     string     `json:"error_code,omitempty"`
	Error         string     `json:"error,omitempty"`
	ResultSummary string     `json:"result_summary,omitempty"`
}

type AgentDetail struct {
	AgentView
	Context  ContextPacket    `json:"context"`
	Messages []llm.Message    `json:"messages"`
	Calls    []llm.CallRecord `json:"calls"`
	Tools    []ToolTrace      `json:"tools"`
	Result   *AgentResult     `json:"result,omitempty"`
}

func snapshotRun(run *Run) RunView {
	view := RunView{
		ID:                     run.ID,
		Goal:                   run.Goal,
		Status:                 run.Status,
		Settings:               run.Settings,
		CreatedAt:              run.CreatedAt,
		StartedAt:              run.StartedAt,
		EndedAt:                run.EndedAt,
		RootAgentID:            run.RootAgentID,
		FinalAnswer:            run.FinalAnswer,
		Error:                  run.Error,
		StopReason:             run.StopReason,
		BudgetHit:              run.BudgetHit,
		Usage:                  run.Usage,
		CostUSD:                run.CostUSD,
		TotalAgents:            len(run.Agents),
		FailNext:               run.failNext,
		MaxObservedConcurrency: run.gate.Peak(),
	}
	if run.Settings.MaxTokens > 0 {
		view.BudgetPercent = float64(run.Usage.TotalTokens) / float64(run.Settings.MaxTokens) * 100
	}
	now := time.Now()
	if run.EndedAt != nil {
		now = *run.EndedAt
	}
	if run.StartedAt != nil {
		view.ElapsedMS = now.Sub(*run.StartedAt).Milliseconds()
	} else {
		view.ElapsedMS = now.Sub(run.CreatedAt).Milliseconds()
	}
	for _, id := range run.Order {
		agent, ok := run.Agents[id]
		if !ok {
			continue
		}
		if agent.Status == StatusRunning || agent.Status == StatusWaiting {
			view.ActiveAgents++
		}
		view.Agents = append(view.Agents, snapshotAgent(agent))
	}
	if view.Agents == nil {
		view.Agents = []AgentView{}
	}
	return view
}

func snapshotAgent(agent *Agent) AgentView {
	view := AgentView{
		ID:           agent.ID,
		ParentID:     agent.ParentID,
		Role:         agent.Role,
		Task:         agent.Task,
		Depth:        agent.Depth,
		Status:       agent.Status,
		Step:         agent.Step,
		IsRoot:       agent.IsRoot,
		ChildCount:   agent.ChildCount,
		CreatedAt:    agent.CreatedAt,
		StartedAt:    agent.StartedAt,
		EndedAt:      agent.EndedAt,
		DurationMS:   agentDuration(agent),
		InputTokens:  agent.Usage.InputTokens,
		OutputTokens: agent.Usage.OutputTokens,
		TotalTokens:  agent.Usage.TotalTokens,
		CostUSD:      agent.CostUSD,
		ErrorCode:    agent.ErrorCode,
		Error:        agent.Error,
	}
	if agent.Result != nil {
		view.ResultSummary = agent.Result.Summary
	}
	return view
}

func snapshotAgentDetail(agent *Agent) AgentDetail {
	detail := AgentDetail{
		AgentView: snapshotAgent(agent),
		Context:   agent.Context,
		Messages:  append([]llm.Message{}, agent.Messages...),
		Calls:     append([]llm.CallRecord{}, agent.Calls...),
		Tools:     append([]ToolTrace{}, agent.Tools...),
	}
	if agent.Result != nil {
		result := *agent.Result
		detail.Result = &result
	}
	return detail
}

func sortedSummaries(items []RunSummary) []RunSummary {
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.After(items[j].CreatedAt) })
	return items
}
