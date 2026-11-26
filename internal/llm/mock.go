package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// MockProvider is a deterministic, offline provider used by the demo and the
// test suite. It still exercises the complete tool-calling loop: it emits tool
// calls, consumes tool results and produces different behavior by role. No
// network or API key is required.
type MockProvider struct {
	latency time.Duration
}

func NewMockProvider(latency time.Duration) *MockProvider {
	return &MockProvider{latency: latency}
}

func (m *MockProvider) Name() string { return "mock" }

func (m *MockProvider) Chat(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	if m.latency > 0 {
		timer := time.NewTimer(m.latency)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ChatResponse{}, ctx.Err()
		case <-timer.C:
		}
	}

	role := detectRole(req.Messages)
	step := assistantCount(req.Messages) + 1
	var resp ChatResponse
	switch role {
	case "showrunner":
		resp = mockShowrunner(req, step)
	case "story_architect":
		resp = mockArchitect(req, step)
	default:
		resp = mockSpecialist(req, role, step)
	}
	resp.Model = req.Model
	if resp.Usage.PromptTokens == 0 {
		resp.Usage.PromptTokens = estimateMessages(req.Messages)
		resp.Usage.CompletionTokens = estimateText(resp.Content) + estimateToolCalls(resp.ToolCalls)
		resp.Usage.TotalTokens = resp.Usage.PromptTokens + resp.Usage.CompletionTokens
	}
	return resp, nil
}

type mockToolResult struct {
	OK        bool   `json:"ok"`
	Role      string `json:"role"`
	Task      string `json:"task"`
	AgentID   string `json:"agent_id"`
	ErrorCode string `json:"error_code"`
	Error     string `json:"error"`
	Retryable bool   `json:"retryable"`
	Summary   string `json:"summary"`
	Artifact  string `json:"artifact"`
}

func mockShowrunner(req ChatRequest, step int) ChatResponse {
	results := mockResults(req.Messages)
	completed := make(map[string][]mockToolResult)
	for _, result := range results {
		if result.OK {
			completed[result.Role] = append(completed[result.Role], result)
		}
	}
	failed := make(map[string]mockToolResult)
	for _, result := range results {
		if !result.OK && len(completed[result.Role]) == 0 {
			failed[result.Role] = result
		}
	}

	hasSpawn := hasTool(req.Tools, "spawn_agent")
	if !hasSpawn {
		return finalText(singleAgentPlan())
	}

	// Hard budget/limit failures cannot be recovered by retrying. Return a
	// partial answer so the run can still finish cleanly.
	for _, result := range failed {
		if result.ErrorCode == "budget_exceeded" || result.ErrorCode == "max_agents_reached" ||
			result.ErrorCode == "depth_exceeded" || result.ErrorCode == "run_cancelled" {
			return finishCall(finalAnswer(completed, true), step)
		}
	}

	// One transient/forced failure is retried with a deliberately smaller task.
	for role, result := range failed {
		if result.Retryable && !strings.Contains(result.Task, "降级重试") {
			return spawnCall(role, result.Task+"（降级重试：缩小范围，优先交付最小可用版本）",
				contextFor(completed, true), step, 0)
		}
	}

	missing := make([]spawnSpec, 0, 2)
	for _, role := range []string{"story_architect", "character_designer"} {
		if len(completed[role]) == 0 {
			if result, ok := failed[role]; ok && !result.Retryable {
				return finishCall(finalAnswer(completed, true), step)
			}
			missing = append(missing, spawnSpec{role, baseTask(role)})
		}
	}
	if len(missing) > 0 {
		return multiSpawnCall(missing, contextFor(completed, false), step)
	}

	writers := completed["writer"]
	if len(writers) == 0 {
		return multiSpawnCall([]spawnSpec{
			{"writer", "撰写第 1-4 集分集大纲：每集包含冷开场、3 个情节点、结尾钩子和本集新增线索。"},
			{"writer", "撰写第 5-7 集分集大纲：中段反转、嫌疑人洗白与再反转，并回收前四集伏笔。"},
			{"writer", "撰写第 8-10 集分集大纲：真相揭示、双重反转、终局情绪与片尾彩蛋。"},
		}, contextFor(completed, false), step)
	}
	if len(writers) < 3 {
		return finishCall(finalAnswer(completed, true), step)
	}
	if len(completed["story_editor"]) == 0 {
		return spawnCall("story_editor", baseTask("story_editor"), contextFor(completed, false), step, 0)
	}
	if len(completed["marketing"]) == 0 {
		return spawnCall("marketing", baseTask("marketing"), contextFor(completed, false), step, 0)
	}
	return finishCall(finalAnswer(completed, false), step)
}

func mockArchitect(req ChatRequest, step int) ChatResponse {
	hasSpawn := hasTool(req.Tools, "spawn_agent")
	results := mockResults(req.Messages)
	if hasSpawn && len(results) == 0 {
		return spawnCall("trope_researcher",
			"总结 3 个悬疑短剧常用反转套路，并指出本剧应如何避免同质化。",
			spawnContext{
				Goal:        "为 10 集悬疑短剧提供结构决策",
				Facts:       []string{"项目为竖屏短剧，每集 45-60 秒", "最终反转必须在前三集埋下可回看线索"},
				Constraints: []string{"不要直接复用知名作品的标志性桥段"},
			}, step, 0)
	}

	facts := []string{}
	if len(results) > 0 && results[0].OK {
		facts = append(facts, "已完成的套路研究："+results[0].Summary)
	}
	artifact := "十集悬疑短剧《回声》结构案\n" +
		"核心谜题：女主在每晚 23:47 收到一段来自七天后的监控录像，录像里她正在处理一具尸体。\n" +
		"时间线：第 1 集建立「未来录像」规则；第 4 集证明录像可以被人为篡改；第 7 集揭示第一段录像是伪造的；第 9 集让观众发现真正的尸体身份；第 10 集完成双重反转。\n" +
		"嫌疑人：前夫顾沉（伪造录像的技术来源）、邻居周岚（失踪者姐姐）、搭档刑警陆鸣（知情者）。\n" +
		"终局反转：女主并非凶手，她一直在保护七年前那起旧案的唯一证人；真正的操控者利用她的记忆缺口布置了整场戏。\n" +
		"伏笔回收：23:47、坏掉的走廊声控灯、反复出现的红色雨伞、第 2 集被剪掉的 12 秒监控。"
	if len(facts) > 0 {
		artifact += "\n" + strings.Join(facts, "\n")
	}
	return submitCall("story_architect", "完成十集总体结构、时间线、嫌疑人与双重反转设计。", artifact, step)
}

func mockSpecialist(req ChatRequest, role string, step int) ChatResponse {
	task := lastUserContent(req.Messages)
	switch role {
	case "trope_researcher":
		return submitCall(role,
			"整理出「不可靠叙述」「时间错位」「亲密关系背叛」三类套路，并给出差异化建议。",
			"套路研究：\n1. 不可靠叙述：让录像本身成为第二叙事者，而不是单纯失忆。\n2. 时间错位：所有误导都严格遵循 23:47 的规则，观众可回看验证。\n3. 亲密关系背叛：前夫不是最终反派，而是被威胁的共犯。\n差异化建议：把「未来录像」做成有代价的工具，每使用一次就丢失一段真实记忆。", step)
	case "character_designer":
		return submitCall(role,
			"完成四位核心人物、三位嫌疑人与各自秘密的关系网。",
			"人物关系网：\n- 林未：记忆修复师，习惯用录音校准现实；弱点是无法信任自己的记忆。\n- 顾沉：前夫，监控工程师，表面操控者，实际被旧案凶手胁迫。\n- 周岚：失踪者周雨的姐姐，用错误信息试探林未。\n- 陆鸣：刑警搭档，七年前旧案的目击者，因愧疚一直保护林未。\n每个人都有一句「真话」，但在不同时间点说出来会指向不同凶手。", step)
	case "writer":
		rangeLabel := "第 1-4 集"
		body := writerArtifact(rangeLabel)
		if strings.Contains(task, "5-7") {
			rangeLabel = "第 5-7 集"
			body = writerArtifact(rangeLabel)
		} else if strings.Contains(task, "8-10") {
			rangeLabel = "第 8-10 集"
			body = writerArtifact(rangeLabel)
		}
		return submitCall(role, rangeLabel+"分集大纲完成，包含冷开场、三个情节点和结尾钩子。", body, step)
	case "story_editor":
		return submitCall(role,
			"审稿评分 86/100；发现 3 处时间线风险并给出可执行修改。",
			"审稿报告：\n评分：86/100（结构 90，人物 84，可拍性 85）。\n必须修改：\n1. 第 3 集走廊声控灯的时间戳与第 7 集回放冲突，统一为 23:46:52。\n2. 第 6 集周岚的动机揭示过早，建议推迟到第 8 集冷开场。\n3. 第 10 集双重反转之间增加 8 秒静默，让观众消化第一层真相。\n通过项：23:47 规则、红伞、12 秒缺失监控三条伏笔均能闭环。\n结论：修改后可进入拍摄。", step)
	case "marketing":
		return submitCall(role,
			"完成上线营销方案，含核心卖点、3 条短视频钩子和发布节奏。",
			"营销方案：\n核心卖点：「你收到的未来录像里，凶手就是你自己。」\n短视频钩子：\n1. 第一视角拆快递，盒子里是七天后自己的求救录音。\n2. 23:47 手机倒计时合拍挑战，最后一秒切到女主的脸。\n3. 三位嫌疑人各说一句真话，评论区投票谁在撒谎。\n发布节奏：首日连更 3 集建立规则；第 4 集卡点停更 24 小时；大结局前释出「无删减监控」互动页。\n转化设计：每集结尾统一使用红色雨伞符号，形成可识别的系列资产。", step)
	default:
		return submitCall(role, "完成分配任务。", "角色 "+role+" 已提交结果。任务："+truncate(task, 300), step)
	}
}

func writerArtifact(rangeLabel string) string {
	episodes := map[string][]string{
		"第 1-4 集": {
			"EP01 冷开场：林未在 23:47 收到未来监控，画面中她拖着一只黑色行李箱。钩子：录像里的她抬头看向镜头。",
			"EP02 三位嫌疑人依次出现，每人提供一段互相矛盾的证词；结尾发现录像被剪掉 12 秒。",
			"EP03 林未按录像提示来到旧居民楼，声控灯在 23:46:52 熄灭；她在墙角找到红色雨伞。",
			"EP04 顾沉承认自己见过录像，却坚称「七天后死的人不是你」；结尾集装箱打开，里面是空的。",
		},
		"第 5-7 集": {
			"EP05 陆鸣调出七年前旧案卷宗，失踪者周雨的照片与录像中的尸体衣着一致。",
			"EP06 林未用记忆录音自证，却发现录音里有第三个人的呼吸声；周岚的谎言被拆穿一半。",
			"EP07 中段反转：第一段未来录像是伪造的，制作时间戳来自一台早已报废的摄像机。",
		},
		"第 8-10 集": {
			"EP08 周岚承认自己一直在试探林未，真正目标是逼出七年前旧案的目击者。",
			"EP09 林未在旧案档案里听见自己的声音，确认自己曾修改证词；红色雨伞指向陆鸣。",
			"EP10 真相：林未不是凶手，她保护的是当年唯一敢作证的周雨；顾沉以录像为诱饵引真凶现身。双重反转后，23:47 的录像再次响起，这一次画面里是观众。",
		},
	}
	lines := episodes[rangeLabel]
	if len(lines) == 0 {
		lines = []string{"分集内容占位"}
	}
	return "分集大纲 " + rangeLabel + "：\n- " + strings.Join(lines, "\n- ")
}

func singleAgentPlan() string {
	return "《回声》单 Agent 基线方案\n\n" +
		"核心谜题：女主林未每晚 23:47 收到七天后的监控，画面中她正在处理一具尸体，而死者尚未出现。\n" +
		"人物：林未（记忆修复师）、顾沉（前夫/监控工程师）、周岚（失踪者姐姐）、陆鸣（刑警搭档）。\n" +
		"十集速览：\n" +
		"EP01 未来录像出现，林未拖黑色行李箱。\n" +
		"EP02 三位嫌疑人给出矛盾证词，录像缺失 12 秒。\n" +
		"EP03 旧居民楼声控灯熄灭，林未找到红伞。\n" +
		"EP04 顾沉称七天后死的不是林未，集装箱却是空的。\n" +
		"EP05 旧案卷宗显示失踪者周雨与录像尸体衣着一致。\n" +
		"EP06 林未的录音里出现第三个人呼吸声。\n" +
		"EP07 第一段录像是伪造的，摄像机早已报废。\n" +
		"EP08 周岚承认试探林未，目标是逼出旧案目击者。\n" +
		"EP09 林未确认自己改过证词，红伞指向陆鸣。\n" +
		"EP10 真相：林未保护的是唯一证人周雨，顾沉以录像引真凶现身；双重反转让 23:47 再次响起。\n" +
		"营销：以「你收到的未来录像里，凶手就是你自己」为卖点，首日连更三集，第 4 集卡点停更，大结局前释出无删减监控互动页。\n" +
		"自查：谜题、人物、十集钩子与营销均覆盖；未经过独立审稿角色复核，时间线细节需人工二次校对。"
}

func baseTask(role string) string {
	switch role {
	case "story_architect":
		return "设计 10 集悬疑短剧的总体谜题、时间线、10 集钩子、嫌疑人与最终反转。"
	case "character_designer":
		return "设计核心人物关系、每位嫌疑人的秘密和可验证的行为动机。"
	case "story_editor":
		return "审阅全部大纲，检查逻辑漏洞、节奏与伏笔回收，给出评分和修改清单。"
	case "marketing":
		return "制定上线营销方案：核心卖点、3 条短视频钩子、发布节奏与转化设计。"
	default:
		return "完成分配给 " + role + " 的任务。"
	}
}

func finalAnswer(completed map[string][]mockToolResult, partial bool) string {
	var b strings.Builder
	b.WriteString("《回声》10 集悬疑短剧策划交付\n\n")
	b.WriteString("一句话卖点：她每晚收到七天后的监控，录像里她正在处理一具尸体，而死者尚未出现。\n\n")
	labels := []struct {
		role  string
		title string
	}{
		{"story_architect", "总体结构"},
		{"character_designer", "人物关系"},
		{"writer", "分集大纲"},
		{"story_editor", "审稿结论"},
		{"marketing", "营销方案"},
	}
	for _, label := range labels {
		items := completed[label.role]
		if len(items) == 0 {
			continue
		}
		b.WriteString("【" + label.title + "】\n")
		for _, item := range items {
			if label.role == "writer" {
				b.WriteString(">" + truncate(item.Task, 120) + "\n")
			}
			if strings.TrimSpace(item.Artifact) != "" {
				b.WriteString(item.Artifact + "\n")
			} else {
				b.WriteString(item.Summary + "\n")
			}
		}
		b.WriteString("\n")
	}
	if partial {
		b.WriteString("说明：部分子任务因预算/限制/失败未能完成，以上为基于已完成节点的部分交付。\n")
	} else {
		b.WriteString("结论：结构、人物、分集与营销已闭环，审稿提出的 3 处修改可在终稿阶段直接执行。")
	}
	return b.String()
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		s = s[:idx]
	}
	return truncate(s, 160)
}

type spawnContext struct {
	Goal        string   `json:"goal"`
	Facts       []string `json:"facts"`
	Upstream    []string `json:"upstream"`
	Constraints []string `json:"constraints"`
}

type spawnSpec struct {
	Role string
	Task string
}

func contextFor(completed map[string][]mockToolResult, reduced bool) spawnContext {
	ctx := spawnContext{
		Goal: "为 10 集竖屏悬疑短剧《回声》完成可拍摄的策划案",
		Constraints: []string{
			"每集 45-60 秒，结尾必须有钩子",
			"最终反转必须能在前三集找到可回看线索",
			"不得使用梦境、双胞胎或超自然力量作为最终解释",
		},
	}
	limit := 8
	if reduced {
		limit = 3
	}
	for _, role := range []string{"story_architect", "character_designer", "writer", "story_editor", "marketing"} {
		for _, item := range completed[role] {
			if len(ctx.Facts) >= limit {
				break
			}
			ctx.Facts = append(ctx.Facts, truncate(item.Summary, 180))
		}
	}
	return ctx
}

func spawnCall(role, task string, sc spawnContext, step, index int) ChatResponse {
	return multiSpawnCall([]spawnSpec{{role, task}}, sc, step)
}

func multiSpawnCall(specs []spawnSpec, sc spawnContext, step int) ChatResponse {
	calls := make([]ToolCall, 0, len(specs))
	for i, spec := range specs {
		args, _ := json.Marshal(map[string]any{
			"role":    spec.Role,
			"task":    spec.Task,
			"context": sc,
		})
		calls = append(calls, ToolCall{
			ID:       fmt.Sprintf("call_%d_spawn_%d", step, i),
			Type:     "function",
			Function: FunctionCall{Name: "spawn_agent", Arguments: string(args)},
		})
	}
	return ChatResponse{ToolCalls: calls, FinishReason: "tool_calls"}
}

func finalText(content string) ChatResponse {
	return ChatResponse{Content: content, FinishReason: "stop"}
}

func finishCall(answer string, step int) ChatResponse {
	args, _ := json.Marshal(map[string]string{"final_answer": answer})
	return ChatResponse{
		ToolCalls: []ToolCall{{
			ID:       fmt.Sprintf("call_%d_finish", step),
			Type:     "function",
			Function: FunctionCall{Name: "finish", Arguments: string(args)},
		}},
		FinishReason: "tool_calls",
	}
}

func submitCall(role, summary, artifact string, step int) ChatResponse {
	args, _ := json.Marshal(map[string]any{
		"summary":  summary,
		"artifact": artifact,
		"self_check": map[string]any{
			"complete": true,
			"notes":    "已核对任务范围与约束，结果可被主 Agent 直接汇总。",
		},
	})
	return ChatResponse{
		ToolCalls: []ToolCall{{
			ID:       fmt.Sprintf("call_%d_submit_%s", step, role),
			Type:     "function",
			Function: FunctionCall{Name: "submit_result", Arguments: string(args)},
		}},
		FinishReason: "tool_calls",
	}
}

func mockResults(messages []Message) []mockToolResult {
	results := make([]mockToolResult, 0)
	for _, message := range messages {
		if message.Role != "tool" {
			continue
		}
		var result mockToolResult
		if json.Unmarshal([]byte(message.Content), &result) == nil {
			results = append(results, result)
		}
	}
	return results
}

func detectRole(messages []Message) string {
	for _, message := range messages {
		if message.Role != "system" {
			continue
		}
		const marker = "[[role:"
		if idx := strings.Index(message.Content, marker); idx >= 0 {
			rest := message.Content[idx+len(marker):]
			if end := strings.IndexByte(rest, ']'); end >= 0 {
				return rest[:end]
			}
		}
	}
	return "showrunner"
}

func hasTool(tools []Tool, name string) bool {
	for _, tool := range tools {
		if tool.Function.Name == name {
			return true
		}
	}
	return false
}

func assistantCount(messages []Message) int {
	n := 0
	for _, message := range messages {
		if message.Role == "assistant" {
			n++
		}
	}
	return n
}

func lastUserContent(messages []Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			return messages[i].Content
		}
	}
	return ""
}

func estimateText(s string) int {
	if s == "" {
		return 0
	}
	return utf8.RuneCountInString(s)/2 + 8
}

func estimateMessages(messages []Message) int {
	total := 0
	for _, message := range messages {
		total += estimateText(message.Content) + 8
		for _, call := range message.ToolCalls {
			total += estimateText(call.Function.Arguments) + 12
		}
	}
	return total
}

func estimateToolCalls(calls []ToolCall) int {
	total := 0
	for _, call := range calls {
		total += estimateText(call.Function.Arguments) + 12
	}
	return total
}
