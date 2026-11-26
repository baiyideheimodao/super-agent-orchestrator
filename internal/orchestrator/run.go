package orchestrator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/baiyideheimodao/super-agent-orchestrator/internal/llm"
)

type execError struct {
	Code      string
	Message   string
	Retryable bool
}

func (e *execError) Error() string { return e.Code + ": " + e.Message }

type toolOutcome struct {
	Content string
	Result  *AgentResult
	ChildID string
}

func (m *Manager) runAgent(ctx context.Context, run *Run, agent *Agent) (*AgentResult, *execError) {
	if err := run.gate.Acquire(ctx); err != nil {
		return nil, m.failAgent(run, agent, m.cancelCode(run, ctx), "等待执行槽位时任务被中止", false)
	}
	holding := true
	defer func() {
		if holding {
			run.gate.Release()
		}
	}()

	m.markRunning(run, agent)
	m.mu.RLock()
	settings := run.Settings
	m.mu.RUnlock()

	m.appendMessage(run, agent, llm.Message{Role: "system", Content: systemPrompt(agent, settings)})
	m.appendMessage(run, agent, llm.Message{Role: "user", Content: userPrompt(agent)})

	for step := 1; step <= settings.AgentMaxSteps; step++ {
		if err := ctx.Err(); err != nil {
			return nil, m.failAgent(run, agent, m.cancelCode(run, ctx), "任务在模型调用前被中止", false)
		}

		m.mu.RLock()
		currentSettings := run.Settings
		used := run.Usage.TotalTokens
		messages := append([]llm.Message(nil), agent.Messages...)
		m.mu.RUnlock()

		if currentSettings.AgentMaxSteps < step {
			return nil, m.failAgent(run, agent, CodeStepLimit, "达到步数上限", false)
		}
		remaining := currentSettings.MaxTokens - used
		if remaining < 160 {
			code := CodeBudget
			if agent.IsRoot {
				m.requestStop(run, "budget")
			}
			return nil, m.failAgent(run, agent, code, "剩余 token 预算不足以下一次模型调用", false)
		}
		maxOut := remaining / 2
		if maxOut > 2048 {
			maxOut = 2048
		}
		if maxOut < 128 {
			maxOut = 128
		}

		m.mu.Lock()
		agent.Step = step
		m.mu.Unlock()

		req := llm.ChatRequest{
			Messages:    messages,
			Tools:       toolDefs(agent, currentSettings),
			Temperature: 0.4,
			MaxTokens:   maxOut,
		}
		resp, err := m.llm.Chat(ctx, req, llm.CallMeta{
			RunID: run.ID, AgentID: agent.ID, Role: agent.Role, Step: step,
		})
		if err != nil {
			code := CodeLLM
			if ctx.Err() != nil {
				code = m.cancelCode(run, ctx)
			}
			retryable := code == CodeLLM || code == CodeTimeout
			return nil, m.failAgent(run, agent, code, err.Error(), retryable)
		}

		assistant := llm.Message{Role: "assistant", Content: resp.Content, ToolCalls: resp.ToolCalls}
		m.appendMessage(run, agent, assistant)
		if len(resp.ToolCalls) == 0 {
			if agent.IsRoot {
				if strings.TrimSpace(resp.Content) == "" {
					return nil, m.failAgent(run, agent, CodeInvalidResult, "主 Agent 返回空结果且未调用 finish", false)
				}
				result := &AgentResult{FinalAnswer: resp.Content, Summary: truncateRunes(resp.Content, 300)}
				m.completeAgent(run, agent, result)
				return result, nil
			}
			m.appendMessage(run, agent, llm.Message{
				Role:    "user",
				Content: "你还没有交付结果。请调用 submit_result，并提供 summary、artifact 和 self_check。",
			})
			continue
		}

		hasSpawn := false
		for _, call := range resp.ToolCalls {
			if call.Function.Name == "spawn_agent" {
				hasSpawn = true
				break
			}
		}
		if hasSpawn {
			run.gate.Release()
			holding = false
			m.setAgentStatus(run, agent, StatusWaiting)
		}

		outcomes := m.executeToolCalls(ctx, run, agent, resp.ToolCalls)
		if hasSpawn {
			if err := run.gate.Acquire(ctx); err != nil {
				return nil, m.failAgent(run, agent, m.cancelCode(run, ctx), "等待子 Agent 时任务被中止", false)
			}
			holding = true
			m.setAgentStatus(run, agent, StatusRunning)
		}

		for i, outcome := range outcomes {
			m.appendMessage(run, agent, llm.Message{
				Role:       "tool",
				ToolCallID: resp.ToolCalls[i].ID,
				Name:       resp.ToolCalls[i].Function.Name,
				Content:    outcome.Content,
			})
		}

		for _, outcome := range outcomes {
			if outcome.Result != nil {
				m.completeAgent(run, agent, outcome.Result)
				return outcome.Result, nil
			}
		}
	}

	return nil, m.failAgent(run, agent, CodeStepLimit, "Agent 在允许的步数内没有提交合格结果", false)
}

func (m *Manager) executeToolCalls(ctx context.Context, run *Run, agent *Agent, calls []llm.ToolCall) []toolOutcome {
	outcomes := make([]toolOutcome, len(calls))
	var wg sync.WaitGroup
	for i := range calls {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			outcomes[i] = m.executeTool(ctx, run, agent, calls[i])
		}(i)
	}
	wg.Wait()
	return outcomes
}

func (m *Manager) executeTool(ctx context.Context, run *Run, agent *Agent, call llm.ToolCall) toolOutcome {
	started := time.Now().UTC()
	m.emit(run.ID, agent.ID, "tool.call", map[string]any{
		"tool_call_id": call.ID,
		"name":         call.Function.Name,
		"arguments":    call.Function.Arguments,
	})

	var outcome toolOutcome
	switch call.Function.Name {
	case "spawn_agent":
		if agent.Depth >= m.currentMaxDepth(run) {
			outcome = toolError("depth_exceeded", "当前 Agent 已达到最大派生深度", false, "", "")
		} else {
			outcome = m.spawnAndWait(ctx, run, agent, call.Function.Arguments)
		}
	case "finish":
		if !agent.IsRoot {
			outcome = toolError(CodeInvalidTool, "只有主 Agent 可以调用 finish", false, "", "")
			break
		}
		args, err := parseFinishArgs(call.Function.Arguments)
		if err != nil {
			outcome = toolError(CodeInvalidResult, err.Error(), true, "", "")
			break
		}
		outcome = toolOutcome{
			Content: toolOK(map[string]any{"status": "finished"}),
			Result:  &AgentResult{FinalAnswer: args.FinalAnswer, Summary: truncateRunes(args.FinalAnswer, 300)},
		}
	case "submit_result":
		if agent.IsRoot {
			outcome = toolError(CodeInvalidTool, "主 Agent 应使用 finish 而不是 submit_result", false, "", "")
			break
		}
		args, err := parseSubmitArgs(call.Function.Arguments)
		if err != nil {
			outcome = toolError(CodeInvalidResult, err.Error(), true, "", "")
			break
		}
		outcome = toolOutcome{
			Content: toolOK(map[string]any{"status": "submitted", "agent_id": agent.ID}),
			Result:  &AgentResult{Summary: args.Summary, Artifact: args.Artifact, SelfCheck: args.SelfCheck},
		}
	default:
		outcome = toolError(CodeInvalidTool, "未知工具: "+call.Function.Name, false, "", "")
	}

	duration := time.Since(started)
	trace := ToolTrace{
		ID:         call.ID,
		AgentID:    agent.ID,
		Name:       call.Function.Name,
		Args:       call.Function.Arguments,
		Result:     truncateRunes(outcome.Content, 8000),
		Status:     "ok",
		ChildID:    outcome.ChildID,
		StartedAt:  started,
		DurationMS: duration.Milliseconds(),
	}
	if isToolError(outcome.Content) {
		trace.Status = "error"
		trace.Error = truncateRunes(outcome.Content, 1000)
	}
	m.mu.Lock()
	agent.Tools = append(agent.Tools, trace)
	m.mu.Unlock()

	m.emit(run.ID, agent.ID, "tool.result", map[string]any{
		"tool_call_id": call.ID,
		"name":         call.Function.Name,
		"status":       trace.Status,
		"result":       outcome.Content,
		"duration_ms":  trace.DurationMS,
		"child_id":     trace.ChildID,
	})
	return outcome
}

type spawnResult struct {
	OK         bool   `json:"ok"`
	AgentID    string `json:"agent_id,omitempty"`
	Role       string `json:"role"`
	Task       string `json:"task"`
	Status     string `json:"status,omitempty"`
	Summary    string `json:"summary,omitempty"`
	Artifact   string `json:"artifact,omitempty"`
	Tokens     int    `json:"tokens,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
	ErrorCode  string `json:"error_code,omitempty"`
	Error      string `json:"error,omitempty"`
	Retryable  bool   `json:"retryable,omitempty"`
}

func (m *Manager) spawnAndWait(ctx context.Context, run *Run, parent *Agent, raw string) toolOutcome {
	args, err := parseSpawnArgs(raw)
	if err != nil {
		return toolError(CodeInvalidTool, err.Error(), false, "", "")
	}
	if err := validateContextBudget(args.Context, m.cfg.Runtime.MaxContextChars); err != nil {
		return toolError(CodeContextTooLarge, err.Error(), false, args.Role, args.Task)
	}

	key := dedupeKey(args)
	now := time.Now().UTC()
	child := &Agent{
		ID:        newID("ag"),
		RunID:     run.ID,
		ParentID:  parent.ID,
		Role:      args.Role,
		Task:      args.Task,
		Context:   args.Context,
		Depth:     parent.Depth + 1,
		Status:    StatusQueued,
		CreatedAt: now,
	}

	m.mu.Lock()
	if run.Status != StatusRunning {
		m.mu.Unlock()
		return toolError(CodeCancelled, "任务已不在运行状态", false, args.Role, args.Task)
	}
	if parent.Depth >= run.Settings.MaxDepth {
		m.mu.Unlock()
		return toolError(CodeDepth, fmt.Sprintf("最大派生深度为 %d", run.Settings.MaxDepth), false, args.Role, args.Task)
	}
	if len(run.Agents) >= run.Settings.MaxAgents {
		m.mu.Unlock()
		return toolError(CodeMaxAgents, fmt.Sprintf("最多允许 %d 个 Agent", run.Settings.MaxAgents), false, args.Role, args.Task)
	}
	if existing, ok := run.inFlight[key]; ok {
		m.mu.Unlock()
		return toolError(CodeDuplicate, "相同任务正在执行，已阻止重复派发（in-flight）", false, args.Role, args.Task, existing)
	}
	if existing, ok := run.dedupe[key]; ok {
		m.mu.Unlock()
		return toolError(CodeDuplicate, "相同任务已成功完成，已阻止重复派发（completed）", false, args.Role, args.Task, existing)
	}
	if run.attempts[key] >= 2 {
		m.mu.Unlock()
		return toolError(CodeDuplicate, "相同任务已失败两次，已阻止继续重复派发", false, args.Role, args.Task)
	}
	softLimit := run.Settings.MaxTokens * 85 / 100
	const reservation = 300
	if run.Usage.TotalTokens+run.Reserved+reservation > softLimit {
		m.mu.Unlock()
		return toolError(CodeBudgetLow, "预算接近上限，禁止继续派生；请汇总已有结果", false, args.Role, args.Task)
	}
	forced := run.failNext
	if forced {
		run.failNext = false
	}
	run.attempts[key]++
	run.inFlight[key] = child.ID
	run.Reserved += reservation
	run.Agents[child.ID] = child
	run.Order = append(run.Order, child.ID)
	parent.ChildCount++
	m.mu.Unlock()

	m.emit(run.ID, child.ID, "agent.created", agentEventData(child))
	if forced {
		failedAt := time.Now().UTC()
		m.mu.Lock()
		child.Status = StatusFailed
		child.EndedAt = &failedAt
		child.ErrorCode = CodeForced
		child.Error = "调试面板注入的强制失败"
		child.Retryable = true
		delete(run.inFlight, key)
		if run.Reserved >= reservation {
			run.Reserved -= reservation
		}
		m.mu.Unlock()
		m.emit(run.ID, child.ID, "agent.failed", map[string]any{
			"error_code": CodeForced, "error": child.Error, "forced": true,
		})
		return toolError(CodeForced, "该子 Agent 被调试开关强制失败", true, args.Role, args.Task, child.ID)
	}

	result, execErr := m.runAgent(ctx, run, child)

	m.mu.Lock()
	delete(run.inFlight, key)
	if run.Reserved >= reservation {
		run.Reserved -= reservation
	}
	if execErr == nil && result != nil {
		run.dedupe[key] = child.ID
	}
	m.mu.Unlock()

	if execErr != nil {
		return toolError(execErr.Code, execErr.Message, execErr.Retryable, args.Role, args.Task, child.ID)
	}
	if result == nil {
		return toolError(CodeInvalidResult, "子 Agent 没有返回结果", true, args.Role, args.Task, child.ID)
	}

	payload := spawnResult{
		OK:         true,
		AgentID:    child.ID,
		Role:       args.Role,
		Task:       args.Task,
		Status:     StatusCompleted,
		Summary:    result.Summary,
		Artifact:   result.Artifact,
		Tokens:     child.Usage.TotalTokens,
		DurationMS: childDuration(child),
	}
	rawPayload, _ := json.Marshal(payload)
	return toolOutcome{Content: string(rawPayload), ChildID: child.ID}
}

func dedupeKey(args spawnArgs) string {
	contextRaw, _ := json.Marshal(args.Context)
	sum := sha256.Sum256([]byte(args.Role + "|" + normalizeTask(args.Task) + "|" + string(contextRaw)))
	return hex.EncodeToString(sum[:12])
}

func (m *Manager) currentMaxDepth(run *Run) int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return run.Settings.MaxDepth
}

func (m *Manager) markRunning(run *Run, agent *Agent) {
	now := time.Now().UTC()
	m.mu.Lock()
	if agent.StartedAt == nil {
		agent.StartedAt = &now
	}
	agent.Status = StatusRunning
	m.mu.Unlock()
	m.emit(run.ID, agent.ID, "agent.started", agentEventData(agent))
}

func (m *Manager) setAgentStatus(run *Run, agent *Agent, status string) {
	m.mu.Lock()
	if isTerminal(agent.Status) {
		m.mu.Unlock()
		return
	}
	agent.Status = status
	m.mu.Unlock()
	m.emit(run.ID, agent.ID, "agent."+status, agentEventData(agent))
}

func (m *Manager) completeAgent(run *Run, agent *Agent, result *AgentResult) {
	now := time.Now().UTC()
	m.mu.Lock()
	if isTerminal(agent.Status) {
		m.mu.Unlock()
		return
	}
	agent.Status = StatusCompleted
	agent.EndedAt = &now
	agent.Result = result
	m.mu.Unlock()
	m.emit(run.ID, agent.ID, "agent.completed", map[string]any{
		"agent_id": agent.ID, "role": agent.Role, "status": StatusCompleted,
		"summary": result.Summary, "total_tokens": agent.Usage.TotalTokens,
		"duration_ms": agentDuration(agent),
	})
}

func (m *Manager) failAgent(run *Run, agent *Agent, code, message string, retryable bool) *execError {
	now := time.Now().UTC()
	status := StatusFailed
	switch code {
	case CodeCancelled:
		status = StatusCancelled
	case CodeTimeout:
		status = StatusTimedOut
	case CodeBudget:
		status = StatusBudgetExhausted
	}
	m.mu.Lock()
	if isTerminal(agent.Status) {
		m.mu.Unlock()
		return &execError{Code: code, Message: message, Retryable: retryable}
	}
	agent.Status = status
	agent.EndedAt = &now
	agent.ErrorCode = code
	agent.Error = message
	agent.Retryable = retryable
	m.mu.Unlock()
	m.emit(run.ID, agent.ID, "agent.failed", map[string]any{
		"agent_id": agent.ID, "role": agent.Role, "status": status,
		"error_code": code, "error": message, "retryable": retryable,
		"total_tokens": agent.Usage.TotalTokens,
	})
	return &execError{Code: code, Message: message, Retryable: retryable}
}

func (m *Manager) appendMessage(run *Run, agent *Agent, message llm.Message) {
	m.mu.Lock()
	agent.Messages = append(agent.Messages, message)
	m.mu.Unlock()
	m.emit(run.ID, agent.ID, "agent.message", map[string]any{
		"role":         message.Role,
		"content":      message.Content,
		"tool_calls":   message.ToolCalls,
		"tool_call_id": message.ToolCallID,
	})
}

func (m *Manager) cancelCode(run *Run, ctx context.Context) string {
	m.mu.RLock()
	reason := run.StopReason
	m.mu.RUnlock()
	switch reason {
	case "budget":
		return CodeBudget
	case "timeout":
		return CodeTimeout
	case "user":
		return CodeCancelled
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return CodeTimeout
	}
	return CodeCancelled
}

func (m *Manager) requestStop(run *Run, reason string) {
	m.mu.Lock()
	if run.StopReason == "" {
		run.StopReason = reason
	}
	run.Status = StatusCancelling
	cancel := run.cancel
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func toolOK(payload map[string]any) string {
	raw, _ := json.Marshal(payload)
	return string(raw)
}

func toolError(code, message string, retryable bool, role, task string, extra ...string) toolOutcome {
	agentID := ""
	if len(extra) > 0 {
		agentID = extra[0]
	}
	payload := spawnResult{
		OK: false, AgentID: agentID, Role: role, Task: task,
		ErrorCode: code, Error: message, Retryable: retryable,
	}
	raw, _ := json.Marshal(payload)
	return toolOutcome{Content: string(raw), ChildID: agentID}
}

func isToolError(content string) bool {
	var probe struct {
		OK bool `json:"ok"`
	}
	if json.Unmarshal([]byte(content), &probe) != nil {
		return false
	}
	return !probe.OK
}

func truncateRunes(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	return string([]rune(s)[:max]) + "…"
}

func agentDuration(agent *Agent) int64 {
	if agent.StartedAt == nil {
		return 0
	}
	end := time.Now()
	if agent.EndedAt != nil {
		end = *agent.EndedAt
	}
	return end.Sub(*agent.StartedAt).Milliseconds()
}

func childDuration(agent *Agent) int64 {
	return agentDuration(agent)
}
