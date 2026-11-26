import type { AgentDetail, AgentView, Message, RunView, ToolCall } from "./types";

export const ROLE_LABELS: Record<string, string> = {
  showrunner: "总策划",
  story_architect: "故事架构",
  character_designer: "人物设计",
  writer: "编剧",
  story_editor: "审稿",
  marketing: "营销",
  trope_researcher: "类型研究"
};

const STATUS_LABELS: Record<string, string> = {
  queued: "排队",
  running: "执行中",
  waiting: "等待子任务",
  completed: "完成",
  failed: "失败",
  cancelled: "已取消",
  timed_out: "超时",
  budget_exhausted: "预算耗尽",
  cancelling: "取消中"
};

export function roleLabel(role: string): string {
  return ROLE_LABELS[role] ?? role;
}

export function statusLabel(status: string): string {
  return STATUS_LABELS[status] ?? status;
}

export function formatNumber(value: number): string {
  return new Intl.NumberFormat("zh-CN").format(Math.round(value));
}

export function formatCost(value: number): string {
  if (value > 0 && value < 0.01) return `$${value.toFixed(5)}`;
  return `$${value.toFixed(4)}`;
}

export function formatDuration(ms: number): string {
  if (ms < 1000) return `${Math.max(0, Math.round(ms))}ms`;
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)}s`;
  return `${Math.floor(ms / 60_000)}m ${Math.round((ms % 60_000) / 1000)}s`;
}

export function isActive(status: string): boolean {
  return ["queued", "running", "waiting", "cancelling"].includes(status);
}

function el<K extends keyof HTMLElementTagNameMap>(
  tag: K,
  className?: string,
  text?: string
): HTMLElementTagNameMap[K] {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}

function svgEl<K extends keyof SVGElementTagNameMap>(
  tag: K,
  attrs: Record<string, string>
): SVGElementTagNameMap[K] {
  const node = document.createElementNS("http://www.w3.org/2000/svg", tag);
  for (const [key, value] of Object.entries(attrs)) node.setAttribute(key, value);
  return node;
}

function statusClass(status: string): string {
  return `status-${status.replace(/[^a-z_]/g, "")}`;
}

function truncate(value: string, max = 72): string {
  return value.length <= max ? value : `${value.slice(0, max - 1)}…`;
}

type Position = { x: number; y: number };

export function renderTree(
  svg: SVGSVGElement,
  run: RunView | null,
  selectedAgentID: string | null,
  onSelect: (agentID: string) => void
): void {
  svg.replaceChildren();
  if (!run || run.agents.length === 0) {
    delete svg.dataset.layoutKey;
    svg.setAttribute("width", "100%");
    svg.setAttribute("height", "100%");
    const empty = svgEl("text", {
      x: "50%",
      y: "46%",
      "text-anchor": "middle",
      class: "tree-empty-svg"
    });
    empty.textContent = "提交任务后，Agent 派发树会在这里实时展开";
    svg.append(empty);
    return;
  }

  const byID = new Map(run.agents.map((agent) => [agent.id, agent]));
  const children = new Map<string, AgentView[]>();
  for (const agent of run.agents) {
    if (!agent.parent_id) continue;
    const list = children.get(agent.parent_id) ?? [];
    list.push(agent);
    children.set(agent.parent_id, list);
  }
  const root = byID.get(run.root_agent_id) ?? run.agents[0];

  const nodeW = 192;
  const nodeH = 84;
  const hGap = 26;
  const vGap = 62;
  const padX = 36;
  const padY = 30;
  const positions = new Map<string, Position>();
  let cursor = padX + nodeW / 2;

  const place = (agent: AgentView, depth: number): number => {
    const kids = children.get(agent.id) ?? [];
    let x: number;
    if (kids.length === 0) {
      x = cursor;
      cursor += nodeW + hGap;
    } else {
      const childXs = kids.map((kid) => place(kid, depth + 1));
      x = (childXs[0] + childXs[childXs.length - 1]) / 2;
    }
    positions.set(agent.id, { x, y: padY + nodeH / 2 + depth * (nodeH + vGap) });
    return x;
  };

  place(root, 0);
  const maxDepth = Math.max(...run.agents.map((agent) => agent.depth));
  const width = Math.max(760, cursor + padX - hGap);
  const height = padY * 2 + (maxDepth + 1) * nodeH + maxDepth * vGap;
  svg.setAttribute("width", String(width));
  svg.setAttribute("height", String(height));
  svg.setAttribute("viewBox", `0 0 ${width} ${height}`);

  // Edges first so nodes cover their endpoints.
  for (const agent of run.agents) {
    if (!agent.parent_id) continue;
    const parent = positions.get(agent.parent_id);
    const child = positions.get(agent.id);
    if (!parent || !child) continue;
    const y1 = parent.y + nodeH / 2;
    const y2 = child.y - nodeH / 2;
    const mid = (y1 + y2) / 2;
    const edge = svgEl("path", {
      d: `M ${parent.x} ${y1} C ${parent.x} ${mid}, ${child.x} ${mid}, ${child.x} ${y2}`,
      class: "tree-edge"
    });
    svg.append(edge);
  }

  for (const agent of run.agents) {
    const position = positions.get(agent.id);
    if (!position) continue;
    const group = svgEl("g", {
      transform: `translate(${position.x - nodeW / 2}, ${position.y - nodeH / 2})`,
      class: `tree-node ${statusClass(agent.status)} ${selectedAgentID === agent.id ? "selected" : ""}`,
      tabindex: "0",
      role: "button",
      "aria-label": `${roleLabel(agent.role)} ${statusLabel(agent.status)}`
    });
    group.addEventListener("click", () => onSelect(agent.id));
    group.addEventListener("keydown", (event) => {
      if ((event as KeyboardEvent).key === "Enter" || (event as KeyboardEvent).key === " ") {
        event.preventDefault();
        onSelect(agent.id);
      }
    });

    const title = svgEl("title", {});
    title.textContent = `${roleLabel(agent.role)}\n${agent.task}\n${statusLabel(agent.status)} · ${formatNumber(agent.total_tokens)} tokens`;
    group.append(title);
    group.append(svgEl("rect", {
      width: String(nodeW), height: String(nodeH), rx: "12", class: "node-box"
    }));
    group.append(svgEl("circle", { cx: "16", cy: "18", r: "4", class: "node-dot" }));

    const role = svgEl("text", { x: "28", y: "23", class: "node-role" });
    role.textContent = roleLabel(agent.role);
    group.append(role);

    const status = svgEl("text", { x: String(nodeW - 12), y: "23", "text-anchor": "end", class: "node-status" });
    status.textContent = statusLabel(agent.status);
    group.append(status);

    const meta = svgEl("text", { x: "14", y: "48", class: "node-meta" });
    meta.textContent = `${formatNumber(agent.total_tokens)} tok · ${formatDuration(agent.duration_ms)}`;
    group.append(meta);

    const task = svgEl("text", { x: "14", y: "69", class: "node-task" });
    task.textContent = truncate(agent.task, 18);
    group.append(task);

    if (agent.is_root) {
      const rootBadge = svgEl("text", { x: String(nodeW - 12), y: "48", "text-anchor": "end", class: "node-root" });
      rootBadge.textContent = "ROOT";
      group.append(rootBadge);
    }
    svg.append(group);
  }

  const wrap = svg.parentElement;
  const rootPosition = positions.get(root.id);
  const layoutKey = `${run.id}:${run.agents.length}`;
  if (wrap && rootPosition && svg.dataset.layoutKey !== layoutKey) {
    svg.dataset.layoutKey = layoutKey;
    requestAnimationFrame(() => {
      wrap.scrollLeft = Math.max(0, rootPosition.x - wrap.clientWidth / 2);
    });
  }
}

function section(title: string, className = "detail-section"): HTMLElement {
  const node = el("section", className);
  node.append(el("h3", "", title));
  return node;
}

function messageNode(message: Message): HTMLElement {
  const wrap = el("details", `message message-${message.role}`);
  const summary = el("summary");
  const role = el("span", "msg-role", message.role);
  summary.append(role);
  if (message.name) summary.append(el("span", "msg-name", message.name));
  if (message.tool_call_id) summary.append(el("span", "msg-call-id", message.tool_call_id));
  const size = (message.content?.length ?? 0) + (message.tool_calls?.length ?? 0) * 80;
  summary.append(el("span", "msg-size", `${formatNumber(size)} chars`));
  wrap.append(summary);
  if (message.role !== "system") wrap.open = true;

  if (message.content) {
    const pre = el("pre", "msg-content");
    pre.textContent = message.content;
    wrap.append(pre);
  }
  for (const call of message.tool_calls ?? []) {
    const callBox = el("div", "tool-call-block");
    callBox.append(el("div", "tool-call-name", `→ ${call.function.name}`));
    const args = el("pre", "msg-content");
    args.textContent = prettyJSON(call.function.arguments);
    callBox.append(args);
    wrap.append(callBox);
  }
  return wrap;
}

function prettyJSON(raw: string): string {
  try {
    return JSON.stringify(JSON.parse(raw), null, 2);
  } catch {
    return raw;
  }
}

function renderContext(detail: AgentDetail): HTMLElement {
  const box = section("独立上下文 / Context Packet");
  const notice = el(
    "p",
    "scope-note",
    detail.is_root
      ? "根 Agent 只看到原始任务与全局约束；子 Agent 只收到下面的结构化任务包，不包含父对话或兄弟分支。"
      : "这是该子 Agent 能看到的全部上游信息。它看不到父 Agent 的完整对话，也看不到兄弟 Agent 的私有消息。"
  );
  box.append(notice);
  box.append(el("div", "context-label", "goal"));
  box.append(el("p", "context-text", detail.context?.goal ?? ""));
  const groups: Array<[string, string[]]> = [
    ["facts", detail.context?.facts ?? []],
    ["upstream", detail.context?.upstream ?? []],
    ["constraints", detail.context?.constraints ?? []]
  ];
  for (const [label, items] of groups) {
    if (items.length === 0) continue;
    box.append(el("div", "context-label", label));
    const list = el("ul", "context-list");
    for (const item of items) list.append(el("li", "", item));
    box.append(list);
  }
  return box;
}

function renderResult(detail: AgentDetail): HTMLElement | null {
  if (!detail.result) return null;
  const box = section("交付结果");
  if (detail.result.final_answer) {
    box.append(el("div", "context-label", "final_answer"));
    const pre = el("pre", "result-block");
    pre.textContent = detail.result.final_answer;
    box.append(pre);
  }
  box.append(el("div", "context-label", "summary"));
  box.append(el("p", "context-text", detail.result.summary));
  if (detail.result.artifact) {
    box.append(el("div", "context-label", "artifact"));
    const pre = el("pre", "result-block");
    pre.textContent = detail.result.artifact;
    box.append(pre);
  }
  if (detail.result.self_check) {
    box.append(el("div", "context-label", "self_check"));
    const pre = el("pre", "result-block");
    pre.textContent = JSON.stringify(detail.result.self_check, null, 2);
    box.append(pre);
  }
  return box;
}

function renderConversation(detail: AgentDetail): HTMLElement {
  const box = section("完整对话");
  const messages = detail.messages ?? [];
  box.append(el("p", "muted", `该 Agent 的消息数：${messages.length}。点击 system 消息可展开它的系统提示词。`));
  for (const message of messages) box.append(messageNode(message));
  return box;
}

function renderCalls(detail: AgentDetail): HTMLElement {
  const box = section("模型调用记录");
  const calls = detail.calls ?? [];
  if (calls.length === 0) {
    box.append(el("p", "muted", "还没有模型调用。"));
    return box;
  }
  const table = el("table", "data-table");
  const head = el("tr");
  for (const label of ["step", "输入", "输出", "费用", "耗时", "并发", "结果"]) {
    head.append(el("th", "", label));
  }
  const thead = el("thead");
  thead.append(head);
  table.append(thead);
  const body = el("tbody");
  for (const call of calls) {
    const row = el("tr");
    row.append(el("td", "", String(call.step)));
    row.append(el("td", "", formatNumber(call.input_tokens)));
    row.append(el("td", "", formatNumber(call.output_tokens)));
    row.append(el("td", "", formatCost(call.cost_usd)));
    row.append(el("td", "", formatDuration(call.duration_ms)));
    row.append(el("td", "", String(call.concurrent_active)));
    const result = el("td", call.error ? "cell-error" : "cell-ok", call.error ? "error" : call.finish_reason ?? "ok");
    if (call.error) result.title = call.error;
    row.append(result);
    body.append(row);
  }
  table.append(body);
  box.append(table);
  const total = calls.reduce((sum, call) => sum + call.cost_usd, 0);
  box.append(el("p", "muted", `调用合计：${formatNumber(calls.length)} 次，${formatCost(total)}。`));
  return box;
}

function renderTools(detail: AgentDetail): HTMLElement {
  const box = section("工具调用 trace");
  const tools = detail.tools ?? [];
  if (tools.length === 0) {
    box.append(el("p", "muted", "还没有工具调用。"));
    return box;
  }
  for (const tool of tools) {
    const item = el("details", "tool-trace");
    const summary = el("summary");
    summary.append(el("span", `tool-status ${tool.status === "ok" ? "ok" : "error"}`, tool.status));
    summary.append(el("span", "tool-name", tool.name));
    summary.append(el("span", "tool-duration", formatDuration(tool.duration_ms)));
    item.append(summary);
    const args = el("pre", "result-block");
    args.textContent = `args:\n${prettyJSON(tool.args)}\n\nresult:\n${prettyJSON(tool.result)}`;
    item.append(args);
    box.append(item);
  }
  return box;
}

export function renderDetail(container: HTMLElement, detail: AgentDetail | null, loading: boolean): void {
  container.replaceChildren();
  if (loading) {
    container.append(el("p", "muted", "加载 Agent 详情…"));
    return;
  }
  if (!detail) {
    const empty = el("div", "detail-empty");
    empty.append(el("div", "tree-empty-icon", "⌁"));
    empty.append(el("h3", "", "选择一个 Agent"));
    empty.append(el("p", "muted", "点击左侧树节点，查看它独立上下文中的完整对话、tool trace 与 token 账目。"));
    container.append(empty);
    return;
  }

  const header = el("div", "detail-head");
  const titleRow = el("div", "detail-title-row");
  titleRow.append(el("h2", "", roleLabel(detail.role)));
  titleRow.append(el("span", `pill ${statusClass(detail.status)}`, statusLabel(detail.status)));
  header.append(titleRow);
  header.append(el("p", "detail-task", detail.task));
  container.append(header);

  const stats = el("div", "stat-grid");
  const items: Array<[string, string]> = [
    ["tokens", formatNumber(detail.total_tokens)],
    ["输入 / 输出", `${formatNumber(detail.input_tokens)} / ${formatNumber(detail.output_tokens)}`],
    ["费用", formatCost(detail.cost_usd)],
    ["耗时", formatDuration(detail.duration_ms)],
    ["深度 / 步数", `${detail.depth} / ${detail.step}`],
    ["子 Agent", String(detail.child_count)]
  ];
  for (const [label, value] of items) {
    const stat = el("div", "stat");
    stat.append(el("span", "stat-label", label));
    stat.append(el("strong", "", value));
    stats.append(stat);
  }
  container.append(stats);

  if (detail.error) {
    const error = el("div", "detail-error");
    error.append(el("strong", "", detail.error_code || "error"));
    error.append(el("p", "", detail.error));
    container.append(error);
  }

  const result = renderResult(detail);
  if (result) container.append(result);
  container.append(renderContext(detail));
  container.append(renderConversation(detail));
  container.append(renderCalls(detail));
  container.append(renderTools(detail));

  const meta = section("标识");
  meta.append(el("p", "muted", `Agent ${detail.id}`));
  if (detail.parent_id) meta.append(el("p", "muted", `父 Agent ${detail.parent_id}`));
  container.append(meta);
}

export function toolCallLabel(call: ToolCall): string {
  return `${call.function.name}(${truncate(call.function.arguments, 60)})`;
}
