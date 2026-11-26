package orchestrator

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/baiyideheimodao/super-agent-orchestrator/internal/llm"
)

type roleSpec struct {
	Title   string
	Mandate string
}

var roleSpecs = map[string]roleSpec{
	"showrunner": {
		Title:   "总策划 / Super-Agent",
		Mandate: "把复杂目标拆成可独立执行的子任务，选择角色并行派发，检查结果，必要时重试或降级，最后汇总成一份可交付成果。你不亲自包办所有细节。",
	},
	"story_architect": {
		Title:   "故事架构师",
		Mandate: "设计总体谜题、时间线、分集钩子、嫌疑人与最终反转，保证反转可从前三集线索回看验证。",
	},
	"character_designer": {
		Title:   "人物设计",
		Mandate: "设计核心人物、关系网、秘密、动机与行为矛盾，避免工具人。",
	},
	"writer": {
		Title:   "编剧",
		Mandate: "按分配到的集数撰写分集大纲，每集必须有冷开场、情节点和结尾钩子。",
	},
	"story_editor": {
		Title:   "审稿",
		Mandate: "以挑剔的审稿人身份检查逻辑、节奏、伏笔、人物一致性与可拍性，给出评分和必须修改项。",
	},
	"marketing": {
		Title:   "营销策划",
		Mandate: "提炼卖点，设计短视频钩子、发布节奏与互动转化方案。",
	},
	"trope_researcher": {
		Title:   "类型研究员",
		Mandate: "研究类型套路与同质化风险，给出可执行的差异化建议。",
	},
}

func systemPrompt(agent *Agent, settings Settings) string {
	spec, ok := roleSpecs[agent.Role]
	if !ok {
		spec = roleSpec{Title: agent.Role, Mandate: "完成分配给你的任务，并如实报告不确定性。"}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "你是 Super-Agent 多智能体系统中的一个独立 Agent。\n")
	fmt.Fprintf(&b, "角色：%s（%s）\n", spec.Title, agent.Role)
	fmt.Fprintf(&b, "职责：%s\n\n", spec.Mandate)

	b.WriteString("【上下文边界】\n")
	b.WriteString("1. 你只能看到本 system 消息、你的任务，以及 user 消息中的结构化任务包。\n")
	b.WriteString("2. 你看不到父 Agent 的完整对话，也看不到兄弟 Agent 的私有上下文；不要假装看过。\n")
	b.WriteString("3. 如果你派生新的 Agent，只能传目标、必要事实、上游结论和约束，绝对不要粘贴对话记录。\n")
	b.WriteString("4. 缺少信息时，在结果中明确标注假设，不要编造已经发生的事实。\n\n")

	b.WriteString("【执行规则】\n")
	fmt.Fprintf(&b, "- 当前深度 %d，系统最大派生深度 %d；超过深度的派发会被拒绝。\n", agent.Depth, settings.MaxDepth)
	fmt.Fprintf(&b, "- 你的步数上限为 %d；整个任务 token 预算为 %d，当前已用会由系统实时检查。\n", settings.AgentMaxSteps, settings.MaxTokens)
	b.WriteString("- 工具调用必须使用系统提供的参数结构。工具报错后，先修正参数或调整策略，不要原样重复。\n")
	b.WriteString("- 同一角色 + 同一任务 + 同一任务包会被去重；不要用重复派发制造循环。\n")

	if agent.IsRoot {
		b.WriteString("\n【你的工作方式】\n")
		b.WriteString("1. 先做运行时规划：哪些工作必须独立完成，哪些可以并行，哪些必须等待上游。\n")
		b.WriteString("2. 可以在同一轮返回多个 spawn_agent 工具调用，系统会并发执行它们。\n")
		b.WriteString("3. 每一轮都要读取工具结果，根据成功/失败/预算动态决定下一步；不要假设子 Agent 一定成功。\n")
		b.WriteString("4. 如果子 Agent 失败且可重试，换一个更小的任务或降级角色重派；不要无限重试。\n")
		b.WriteString("5. 预算不足时停止派生，调用 finish 提交已有成果，并明确列出未完成部分。\n")
		b.WriteString("6. 最终必须调用 finish，给出完整、可直接交付的中文结果。\n")
	} else {
		b.WriteString("\n【交付契约】\n")
		b.WriteString("1. 你的最终交付必须通过 submit_result 工具完成，不能只输出一段普通文本。\n")
		b.WriteString("2. summary 是给父 Agent 汇总的短摘要；artifact 是你的完整产物；self_check.complete 必须为 true，并写明自检说明。\n")
		if agent.Depth < settings.MaxDepth {
			b.WriteString("3. 如确有必要，你可以调用 spawn_agent 派生下级；否则直接 submit_result。\n")
		} else {
			b.WriteString("3. 你已处于最大深度，不能再派生 Agent；请直接完成并 submit_result。\n")
		}
	}

	fmt.Fprintf(&b, "\n[[role:%s]]\n", agent.Role)
	return b.String()
}

func userPrompt(agent *Agent) string {
	if agent.IsRoot {
		return "总任务：\n" + agent.Task + "\n\n请开始运行时规划并执行。你不必向用户展示思考过程，直接调用工具推进任务。"
	}
	packet, _ := json.MarshalIndent(agent.Context, "", "  ")
	return "你的任务：\n" + agent.Task + "\n\n结构化任务包（这是你拥有的全部上游信息，不包含父 Agent 对话）：\n" +
		string(packet) + "\n\n请只基于以上信息完成你的职责，并通过 submit_result 交付。"
}

func toolDefs(agent *Agent, settings Settings) []llm.Tool {
	tools := make([]llm.Tool, 0, 2)
	if agent.Depth < settings.MaxDepth {
		tools = append(tools, spawnTool())
	}
	if agent.IsRoot {
		tools = append(tools, finishTool())
	} else {
		tools = append(tools, submitTool())
	}
	return tools
}

func spawnTool() llm.Tool {
	return llm.Tool{
		Type: "function",
		Function: llm.FunctionDef{
			Name:        "spawn_agent",
			Description: "派生一个拥有独立上下文的子 Agent，并等待其返回。可在同一轮调用多次以并发执行。context 只能包含结构化任务包，禁止粘贴完整对话。",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"role": map[string]any{
						"type":        "string",
						"enum":        []string{"story_architect", "character_designer", "writer", "story_editor", "marketing", "trope_researcher"},
						"description": "子 Agent 的角色。",
					},
					"task": map[string]any{
						"type":        "string",
						"minLength":   3,
						"maxLength":   2000,
						"description": "边界清晰、可独立交付的任务。",
					},
					"context": map[string]any{
						"type":                 "object",
						"description":          "结构化交接包；不要包含完整对话或隐藏推理。",
						"additionalProperties": false,
						"required":             []string{"goal", "facts", "upstream", "constraints"},
						"properties": map[string]any{
							"goal":        map[string]any{"type": "string", "maxLength": 500},
							"facts":       map[string]any{"type": "array", "maxItems": 12, "items": map[string]any{"type": "string", "maxLength": 300}},
							"upstream":    map[string]any{"type": "array", "maxItems": 8, "items": map[string]any{"type": "string", "maxLength": 500}},
							"constraints": map[string]any{"type": "array", "maxItems": 8, "items": map[string]any{"type": "string", "maxLength": 200}},
						},
					},
				},
				"required":             []string{"role", "task", "context"},
				"additionalProperties": false,
			},
		},
	}
}

func finishTool() llm.Tool {
	return llm.Tool{
		Type: "function",
		Function: llm.FunctionDef{
			Name:        "finish",
			Description: "结束总任务并提交最终交付。预算耗尽或部分子任务失败时也必须调用，并在结果中说明缺口。",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"final_answer": map[string]any{"type": "string", "minLength": 10, "maxLength": 30000},
				},
				"required":             []string{"final_answer"},
				"additionalProperties": false,
			},
		},
	}
}

func submitTool() llm.Tool {
	return llm.Tool{
		Type: "function",
		Function: llm.FunctionDef{
			Name:        "submit_result",
			Description: "提交子任务结果。summary 给父 Agent 汇总，artifact 是完整产物，self_check 必须自检。",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"summary":  map[string]any{"type": "string", "minLength": 10, "maxLength": 2000},
					"artifact": map[string]any{"type": "string", "minLength": 10, "maxLength": 20000},
					"self_check": map[string]any{
						"type":     "object",
						"required": []string{"complete", "notes"},
						"properties": map[string]any{
							"complete": map[string]any{"type": "boolean"},
							"notes":    map[string]any{"type": "string", "minLength": 3, "maxLength": 1000},
						},
						"additionalProperties": false,
					},
				},
				"required":             []string{"summary", "artifact", "self_check"},
				"additionalProperties": false,
			},
		},
	}
}
