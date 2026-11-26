package orchestrator

import (
	"strings"
	"testing"
	"time"

	"github.com/baiyideheimodao/super-agent-orchestrator/internal/config"
	"github.com/baiyideheimodao/super-agent-orchestrator/internal/stream"
)

func testConfig(t *testing.T) config.Config {
	t.Helper()
	return config.Config{
		DataDir: t.TempDir(),
		LLM: config.LLMConfig{
			Provider: "mock", Model: "mock-model", MaxConcurrency: 4,
			Timeout: 3 * time.Second, MaxOutputTokens: 2048, MockStepLatencyMS: 0,
			InputUSDPerMTok: 1, OutputUSDPerMTok: 2,
		},
		Runtime: config.RuntimeConfig{
			MaxTokens: 60000, MaxDuration: 60 * time.Second, MaxDepth: 2,
			MaxConcurrency: 4, MaxAgents: 20, AgentMaxSteps: 8, MaxContextChars: 6000,
		},
	}
}

func testSettings() Settings {
	return Settings{
		MaxTokens: 60000, MaxDurationSec: 60, MaxDepth: 2,
		MaxConcurrency: 4, MaxAgents: 20, AgentMaxSteps: 8,
	}
}

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	m, err := NewManager(testConfig(t), stream.NewHub(), nil)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func waitRun(t *testing.T, m *Manager, id string, timeout time.Duration) *RunView {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		view, ok := m.GetRun(id)
		if !ok {
			t.Fatalf("run %s disappeared", id)
		}
		if isTerminal(view.Status) {
			return view
		}
		time.Sleep(10 * time.Millisecond)
	}
	view, _ := m.GetRun(id)
	t.Fatalf("run did not finish: %+v", view)
	return nil
}

func TestRunBuildsNestedTreeAndCompletes(t *testing.T) {
	m := newTestManager(t)
	settings := testSettings()
	view, err := m.CreateRun("策划一部 10 集悬疑短剧", &settings)
	if err != nil {
		t.Fatal(err)
	}
	view = waitRun(t, m, view.ID, 10*time.Second)
	if view.Status != StatusCompleted {
		t.Fatalf("status = %s, error = %s", view.Status, view.Error)
	}
	if !strings.Contains(view.FinalAnswer, "10 集") && !strings.Contains(view.FinalAnswer, "十集") {
		t.Fatalf("final answer missing series scope: %s", view.FinalAnswer)
	}
	if view.Usage.TotalTokens == 0 || view.CostUSD <= 0 {
		t.Fatalf("usage/cost not recorded: %+v cost=%v", view.Usage, view.CostUSD)
	}
	roles := map[string]int{}
	maxDepth := 0
	for _, agent := range view.Agents {
		roles[agent.Role]++
		if agent.Depth > maxDepth {
			maxDepth = agent.Depth
		}
		if agent.TotalTokens == 0 {
			t.Errorf("agent %s has no token usage", agent.ID)
		}
	}
	for _, role := range []string{"showrunner", "story_architect", "character_designer", "writer", "story_editor", "marketing", "trope_researcher"} {
		if roles[role] == 0 {
			t.Errorf("missing role %s: %v", role, roles)
		}
	}
	if roles["writer"] != 3 {
		t.Errorf("writer count = %d, want 3", roles["writer"])
	}
	if maxDepth != 2 {
		t.Errorf("max depth = %d, want 2", maxDepth)
	}
	if !strings.Contains(view.FinalAnswer, "审稿") || !strings.Contains(view.FinalAnswer, "营销") {
		t.Errorf("final answer does not aggregate all branches")
	}
	if view.MaxObservedConcurrency < 2 {
		t.Errorf("expected concurrent children, peak = %d", view.MaxObservedConcurrency)
	}

	// A child's transcript must begin with its own system prompt and task, not
	// the parent's conversation.
	for _, agent := range view.Agents {
		if agent.IsRoot {
			continue
		}
		detail, ok := m.GetAgent(view.ID, agent.ID)
		if !ok {
			t.Fatal("agent detail missing")
		}
		if len(detail.Messages) < 2 {
			t.Fatalf("agent %s has %d messages", agent.ID, len(detail.Messages))
		}
		if strings.Contains(detail.Messages[0].Content, "总任务：") {
			t.Fatalf("child inherited parent system prompt")
		}
		if detail.Context.Goal == "" {
			t.Fatalf("child context packet is empty")
		}
	}
}

func TestDepthLimitStopsNestedSpawn(t *testing.T) {
	m := newTestManager(t)
	settings := testSettings()
	settings.MaxDepth = 1
	view, err := m.CreateRun("策划 10 集悬疑短剧", &settings)
	if err != nil {
		t.Fatal(err)
	}
	view = waitRun(t, m, view.ID, 10*time.Second)
	if view.Status != StatusCompleted {
		t.Fatalf("status = %s, error = %s", view.Status, view.Error)
	}
	for _, agent := range view.Agents {
		if agent.Depth > 1 {
			t.Fatalf("agent %s exceeded max depth: %d", agent.Role, agent.Depth)
		}
	}
	for _, agent := range view.Agents {
		if agent.Role == "trope_researcher" {
			t.Fatal("nested researcher should not exist at max_depth=1")
		}
	}
}

func TestMaxDepthZeroStillProducesPartialDelivery(t *testing.T) {
	m := newTestManager(t)
	settings := testSettings()
	settings.MaxDepth = 0
	view, err := m.CreateRun("策划 10 集悬疑短剧", &settings)
	if err != nil {
		t.Fatal(err)
	}
	view = waitRun(t, m, view.ID, 5*time.Second)
	if view.Status != StatusCompleted {
		t.Fatalf("status = %s, error = %s", view.Status, view.Error)
	}
	if len(view.Agents) != 1 || view.Agents[0].Role != "showrunner" {
		t.Fatalf("agents = %+v", view.Agents)
	}
	if view.FinalAnswer == "" {
		t.Fatal("expected a final answer when spawning is disabled")
	}
}

func TestForcedFailureIsRetriedByShowrunner(t *testing.T) {
	m := newTestManager(t)
	settings := testSettings()
	view, err := m.CreateRun("策划 10 集悬疑短剧", &settings)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetFailNext(view.ID, true); err != nil {
		t.Fatal(err)
	}
	view = waitRun(t, m, view.ID, 10*time.Second)
	if view.Status != StatusCompleted {
		t.Fatalf("status = %s, error = %s", view.Status, view.Error)
	}
	failed, retried := 0, 0
	for _, agent := range view.Agents {
		if agent.ErrorCode == CodeForced {
			failed++
		}
		if agent.Status == StatusCompleted && strings.Contains(agent.Task, "降级重试") {
			retried++
		}
	}
	if failed != 1 || retried != 1 {
		t.Fatalf("failed=%d retried=%d; agents=%+v", failed, retried, view.Agents)
	}
}

func TestLowBudgetStopsSpawningAndFinishes(t *testing.T) {
	m := newTestManager(t)
	settings := testSettings()
	settings.MaxTokens = 2400
	view, err := m.CreateRun("策划 10 集悬疑短剧", &settings)
	if err != nil {
		t.Fatal(err)
	}
	view = waitRun(t, m, view.ID, 10*time.Second)
	if view.Status != StatusCompleted && view.Status != StatusBudgetExhausted {
		t.Fatalf("status = %s, error = %s", view.Status, view.Error)
	}
	if view.TotalAgents > 6 {
		t.Fatalf("budget did not stop fan-out: %d agents, %d tokens", view.TotalAgents, view.Usage.TotalTokens)
	}
	if view.FinalAnswer == "" {
		t.Fatal("expected a partial final answer")
	}
}

func TestCancelStopsRunAndAgents(t *testing.T) {
	cfg := testConfig(t)
	cfg.LLM.MockStepLatencyMS = 40
	m, err := NewManager(cfg, stream.NewHub(), nil)
	if err != nil {
		t.Fatal(err)
	}
	settings := testSettings()
	view, err := m.CreateRun("策划 10 集悬疑短剧", &settings)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if _, err := m.Cancel(view.ID, "user"); err != nil {
		t.Fatal(err)
	}
	view = waitRun(t, m, view.ID, 5*time.Second)
	if view.Status != StatusCancelled {
		t.Fatalf("status = %s, error = %s", view.Status, view.Error)
	}
	for _, agent := range view.Agents {
		if !isTerminal(agent.Status) {
			t.Fatalf("agent %s still %s after cancel", agent.Role, agent.Status)
		}
	}
}

func TestConcurrencyOneDoesNotDeadlockNestedSpawn(t *testing.T) {
	m := newTestManager(t)
	settings := testSettings()
	settings.MaxConcurrency = 1
	view, err := m.CreateRun("策划 10 集悬疑短剧", &settings)
	if err != nil {
		t.Fatal(err)
	}
	view = waitRun(t, m, view.ID, 15*time.Second)
	if view.Status != StatusCompleted {
		t.Fatalf("status = %s, error = %s", view.Status, view.Error)
	}
	if view.MaxObservedConcurrency > 1 {
		t.Fatalf("peak concurrency = %d, want <= 1", view.MaxObservedConcurrency)
	}
}
