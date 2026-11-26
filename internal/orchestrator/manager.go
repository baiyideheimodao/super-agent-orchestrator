package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/baiyideheimodao/super-agent-orchestrator/internal/config"
	"github.com/baiyideheimodao/super-agent-orchestrator/internal/llm"
	"github.com/baiyideheimodao/super-agent-orchestrator/internal/stream"
)

type Manager struct {
	cfg config.Config
	hub *stream.Hub
	llm *llm.Client
	log *slog.Logger

	mu   sync.RWMutex
	runs map[string]*Run

	auditMu sync.Mutex
}

func NewManager(cfg config.Config, hub *stream.Hub, logger *slog.Logger) (*Manager, error) {
	if logger == nil {
		logger = slog.Default()
	}
	m := &Manager{
		cfg:  cfg,
		hub:  hub,
		log:  logger,
		runs: make(map[string]*Run),
	}
	client, err := llm.NewClient(cfg.LLM, m.recordCall)
	if err != nil {
		return nil, fmt.Errorf("create LLM client: %w", err)
	}
	m.llm = client
	return m, nil
}

func (m *Manager) DefaultSettings() Settings {
	return SettingsFromConfig(m.cfg.Runtime)
}

func (m *Manager) Provider() string { return m.llm.ProviderName() }

func (m *Manager) CreateRun(goal string, settingsOverride *Settings) (*RunView, error) {
	goal = strings.TrimSpace(goal)
	if n := utf8.RuneCountInString(goal); n < 3 || n > 4000 {
		return nil, fmt.Errorf("goal length must be between 3 and 4000 runes")
	}
	settings := m.DefaultSettings()
	if settingsOverride != nil {
		settings = *settingsOverride
	}
	if err := settings.Validate(); err != nil {
		return nil, err
	}
	if settings.MaxDurationSec > 3600 {
		return nil, fmt.Errorf("max_duration_sec must be <= 3600")
	}

	now := time.Now().UTC()
	run := &Run{
		ID:        newID("run"),
		Goal:      goal,
		Status:    StatusQueued,
		Settings:  settings,
		CreatedAt: now,
		Agents:    make(map[string]*Agent),
		gate:      NewGate(settings.MaxConcurrency),
		dedupe:    make(map[string]string),
		attempts:  make(map[string]int),
		inFlight:  make(map[string]string),
	}
	m.mu.Lock()
	m.runs[run.ID] = run
	m.mu.Unlock()

	m.emit(run.ID, "", "run.created", map[string]any{
		"goal":     goal,
		"settings": settings,
	})
	go m.execute(run)
	view, ok := m.GetRun(run.ID)
	if !ok {
		return nil, fmt.Errorf("run not found after creation")
	}
	return view, nil
}

func (m *Manager) GetRun(id string) (*RunView, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	run, ok := m.runs[id]
	if !ok {
		return nil, false
	}
	view := snapshotRun(run)
	return &view, true
}

func (m *Manager) GetAgent(runID, agentID string) (*AgentDetail, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	run, ok := m.runs[runID]
	if !ok {
		return nil, false
	}
	agent, ok := run.Agents[agentID]
	if !ok {
		return nil, false
	}
	detail := snapshotAgentDetail(agent)
	return &detail, true
}

func (m *Manager) ListRuns() []RunSummary {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]RunSummary, 0, len(m.runs))
	for _, run := range m.runs {
		out = append(out, RunSummary{
			ID:          run.ID,
			Goal:        run.Goal,
			Status:      run.Status,
			CreatedAt:   run.CreatedAt,
			TotalTokens: run.Usage.TotalTokens,
			MaxTokens:   run.Settings.MaxTokens,
			AgentCount:  len(run.Agents),
		})
	}
	return sortedSummaries(out)
}

func (m *Manager) UpdateSettings(runID string, next Settings) (*RunView, error) {
	if err := next.Validate(); err != nil {
		return nil, err
	}
	if next.MaxDurationSec > 3600 {
		return nil, fmt.Errorf("max_duration_sec must be <= 3600")
	}
	m.mu.Lock()
	run, ok := m.runs[runID]
	if !ok {
		m.mu.Unlock()
		return nil, fmt.Errorf("run not found")
	}
	if isTerminal(run.Status) {
		m.mu.Unlock()
		return nil, fmt.Errorf("run already ended")
	}
	run.Settings = next
	run.gate.SetLimit(next.MaxConcurrency)
	m.mu.Unlock()

	m.emit(runID, "", "run.settings_updated", map[string]any{"settings": next})
	view, ok := m.GetRun(runID)
	if !ok {
		return nil, fmt.Errorf("run not found")
	}
	return view, nil
}

func (m *Manager) SetFailNext(runID string, enabled bool) (*RunView, error) {
	m.mu.Lock()
	run, ok := m.runs[runID]
	if !ok {
		m.mu.Unlock()
		return nil, fmt.Errorf("run not found")
	}
	if isTerminal(run.Status) {
		m.mu.Unlock()
		return nil, fmt.Errorf("run already ended")
	}
	run.failNext = enabled
	m.mu.Unlock()
	m.emit(runID, "", "run.fail_next_changed", map[string]any{"enabled": enabled})
	view, ok := m.GetRun(runID)
	if !ok {
		return nil, fmt.Errorf("run not found")
	}
	return view, nil
}

func (m *Manager) Cancel(runID, reason string) (*RunView, error) {
	m.mu.Lock()
	run, ok := m.runs[runID]
	if !ok {
		m.mu.Unlock()
		return nil, fmt.Errorf("run not found")
	}
	if isTerminal(run.Status) {
		m.mu.Unlock()
		return nil, fmt.Errorf("run already ended")
	}
	if run.StopReason == "" {
		run.StopReason = "user"
		if reason != "" {
			run.StopReason = reason
		}
	}
	run.Status = StatusCancelling
	cancel := run.cancel
	stopReason := run.StopReason
	m.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	m.emit(runID, "", "run.cancelling", map[string]any{"reason": stopReason})
	view, ok := m.GetRun(runID)
	if !ok {
		return nil, fmt.Errorf("run not found")
	}
	return view, nil
}

func (m *Manager) execute(run *Run) {
	started := time.Now().UTC()
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(run.Settings.MaxDurationSec)*time.Second)

	m.mu.Lock()
	if run.finished {
		m.mu.Unlock()
		cancel()
		return
	}
	if run.StopReason != "" {
		ended := time.Now().UTC()
		run.finished = true
		run.Status = StatusCancelled
		run.EndedAt = &ended
		run.Error = "任务在启动前被取消"
		run.FinalAnswer = partialAnswer(run)
		view := snapshotRun(run)
		m.mu.Unlock()
		cancel()
		m.emit(run.ID, "", "run.cancelled", map[string]any{"status": run.Status, "error": run.Error})
		m.persistSnapshot(view)
		return
	}
	run.ctx = ctx
	run.cancel = cancel
	run.StartedAt = &started
	run.Status = StatusRunning
	root := &Agent{
		ID:        newID("ag"),
		RunID:     run.ID,
		Role:      "showrunner",
		Task:      run.Goal,
		Context:   ContextPacket{Goal: run.Goal, Constraints: []string{"遵守系统预算、深度与并发限制"}},
		Depth:     0,
		Status:    StatusQueued,
		CreatedAt: started,
		IsRoot:    true,
	}
	run.RootAgentID = root.ID
	run.Agents[root.ID] = root
	run.Order = append(run.Order, root.ID)
	m.mu.Unlock()

	m.emit(run.ID, "", "run.started", map[string]any{"root_agent_id": root.ID})
	m.emit(run.ID, root.ID, "agent.created", agentEventData(root))
	go m.monitor(run)

	result, execErr := m.runAgent(ctx, run, root)
	m.finalize(run, root, result, execErr)
}

func (m *Manager) finalize(run *Run, root *Agent, result *AgentResult, execErr *execError) {
	ended := time.Now().UTC()

	m.mu.Lock()
	if run.finished {
		m.mu.Unlock()
		return
	}
	run.finished = true
	run.EndedAt = &ended
	if run.cancel != nil {
		run.cancel()
	}
	if run.monitorStop != nil {
		close(run.monitorStop)
	}

	switch {
	case run.StopReason == "user":
		run.Status = StatusCancelled
		run.Error = "任务被用户取消"
	case run.StopReason == "timeout":
		run.Status = StatusTimedOut
		run.Error = "任务超过总时长上限"
	case run.StopReason == "budget":
		run.Status = StatusBudgetExhausted
		run.BudgetHit = true
		run.Error = "任务达到总 token 预算"
	case execErr != nil:
		run.Status = StatusFailed
		run.Error = execErr.Message
	default:
		run.Status = StatusCompleted
	}
	if result != nil {
		run.FinalAnswer = result.FinalAnswer
		if result.Summary != "" && run.FinalAnswer == "" {
			run.FinalAnswer = result.Summary
		}
	}
	if run.FinalAnswer == "" && (run.Status == StatusBudgetExhausted || run.Status == StatusTimedOut || run.Status == StatusCancelled) {
		run.FinalAnswer = partialAnswer(run)
	}
	if root != nil {
		if root.EndedAt == nil {
			root.EndedAt = &ended
		}
		if result != nil {
			root.Result = result
		}
		if run.Status != StatusCompleted && root.Status == StatusRunning {
			root.Status = run.Status
		}
	}
	view := snapshotRun(run)
	m.mu.Unlock()

	eventType := "run.completed"
	switch run.Status {
	case StatusCancelled:
		eventType = "run.cancelled"
	case StatusTimedOut, StatusFailed, StatusBudgetExhausted:
		eventType = "run.failed"
	}
	m.emit(run.ID, "", eventType, map[string]any{
		"status":       run.Status,
		"error":        run.Error,
		"final_answer": run.FinalAnswer,
		"usage":        run.Usage,
		"cost_usd":     run.CostUSD,
	})
	m.persistSnapshot(view)
	m.log.Info("run finished", "run_id", run.ID, "status", run.Status, "tokens", run.Usage.TotalTokens, "agents", len(run.Agents))
}

func (m *Manager) monitor(run *Run) {
	stop := make(chan struct{})
	m.mu.Lock()
	run.monitorStop = stop
	m.mu.Unlock()

	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	lastEmitted := ""
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			m.mu.RLock()
			if run.finished {
				m.mu.RUnlock()
				return
			}
			reason := ""
			if run.Usage.TotalTokens >= run.Settings.MaxTokens {
				reason = "budget"
			}
			if run.StartedAt != nil && time.Since(*run.StartedAt) >= time.Duration(run.Settings.MaxDurationSec)*time.Second {
				reason = "timeout"
			}
			if run.StopReason != "" {
				reason = ""
			}
			cancel := run.cancel
			m.mu.RUnlock()

			if reason != "" && reason != lastEmitted {
				lastEmitted = reason
				m.mu.Lock()
				if run.StopReason == "" {
					run.StopReason = reason
				}
				run.Status = StatusCancelling
				m.mu.Unlock()
				if cancel != nil {
					cancel()
				}
				m.emit(run.ID, "", "run.stopping", map[string]any{"reason": reason})
			}
		}
	}
}

func (m *Manager) recordCall(record llm.CallRecord) {
	m.mu.Lock()
	run, ok := m.runs[record.RunID]
	if !ok {
		m.mu.Unlock()
		return
	}
	run.Usage.Add(record)
	run.CostUSD += record.CostUSD
	if agent, ok := run.Agents[record.AgentID]; ok {
		agent.Usage.Add(record)
		agent.CostUSD += record.CostUSD
		agent.Calls = append(agent.Calls, record)
	}
	m.mu.Unlock()
	m.emit(record.RunID, record.AgentID, "llm.call", record)
}

func (m *Manager) emit(runID, agentID, eventType string, data any) {
	event := m.hub.Publish(eventType, runID, agentID, data)
	m.appendAudit(runID, event)
}

func (m *Manager) appendAudit(runID string, event stream.Event) {
	if runID == "" {
		return
	}
	raw, err := json.Marshal(event)
	if err != nil {
		return
	}
	dir := filepath.Join(m.cfg.DataDir, "runs")
	m.auditMu.Lock()
	defer m.auditMu.Unlock()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		m.log.Warn("create audit directory", "error", err)
		return
	}
	file, err := os.OpenFile(filepath.Join(dir, runID+".jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		m.log.Warn("open audit file", "error", err)
		return
	}
	defer file.Close()
	if _, err := file.Write(append(raw, '\n')); err != nil {
		m.log.Warn("write audit event", "error", err)
	}
}

func (m *Manager) persistSnapshot(view RunView) {
	raw, err := json.MarshalIndent(view, "", "  ")
	if err != nil {
		return
	}
	dir := filepath.Join(m.cfg.DataDir, "runs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	path := filepath.Join(dir, view.ID+".json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return
	}
	_ = os.Rename(tmp, path)
}

func agentEventData(agent *Agent) map[string]any {
	return map[string]any{
		"id":            agent.ID,
		"parent_id":     agent.ParentID,
		"role":          agent.Role,
		"task":          agent.Task,
		"depth":         agent.Depth,
		"status":        agent.Status,
		"total_tokens":  agent.Usage.TotalTokens,
		"input_tokens":  agent.Usage.InputTokens,
		"output_tokens": agent.Usage.OutputTokens,
	}
}

func partialAnswer(run *Run) string {
	var b strings.Builder
	reason := "任务提前结束"
	switch run.Status {
	case StatusBudgetExhausted:
		reason = "token 预算耗尽，任务提前结束"
	case StatusTimedOut:
		reason = "总时长耗尽，任务提前结束"
	case StatusCancelled:
		reason = "任务被取消，以下是已完成的部分"
	}
	b.WriteString("【部分交付】" + reason + "。\n\n")
	completed := 0
	for _, id := range run.Order {
		agent := run.Agents[id]
		if agent == nil || agent.IsRoot || agent.Result == nil {
			continue
		}
		completed++
		b.WriteString("- " + agent.Role + "：" + truncateRunes(agent.Result.Summary, 220) + "\n")
	}
	if completed == 0 {
		b.WriteString("尚无可汇总的子 Agent 结果；已保留所有调用与失败记录供排查。\n")
	} else {
		b.WriteString("\n以上为已完成节点的确定性汇总，未完成任务未计入，以免编造内容。\n")
	}
	return b.String()
}
