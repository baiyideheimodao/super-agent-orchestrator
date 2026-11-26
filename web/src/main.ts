import "./styles.css";
import {
  ApiError,
  cancelRun,
  createRun,
  getAgent,
  getConfig,
  getRun,
  setFailNext,
  updateSettings
} from "./api";
import type { AgentDetail, AppConfig, RunView, Settings } from "./types";
import { formatCost, formatDuration, formatNumber, isActive, renderDetail, renderTree, statusLabel } from "./render";

function byId<T extends Element>(id: string): T {
  const node = document.getElementById(id);
  if (!node) throw new Error(`missing element #${id}`);
  return node as unknown as T;
}

const goalInput = byId<HTMLTextAreaElement>("goal");
const startBtn = byId<HTMLButtonElement>("start-btn");
const cancelBtn = byId<HTMLButtonElement>("cancel-btn");
const applyBtn = byId<HTMLButtonElement>("apply-btn");
const failNextInput = byId<HTMLInputElement>("fail-next");
const noticeBox = byId<HTMLDivElement>("notice");
const providerLabel = byId<HTMLSpanElement>("provider-label");
const runStatus = byId<HTMLSpanElement>("run-status");
const budgetText = byId<HTMLElement>("budget-text");
const budgetPercent = byId<HTMLElement>("budget-percent");
const budgetFill = byId<HTMLElement>("budget-fill");
const costText = byId<HTMLElement>("cost-text");
const elapsedText = byId<HTMLElement>("elapsed-text");
const agentCount = byId<HTMLElement>("agent-count");
const peakConcurrency = byId<HTMLElement>("peak-concurrency");
const stageSubtitle = byId<HTMLElement>("stage-subtitle");
const finalPanel = byId<HTMLDivElement>("final-panel");
const finalText = byId<HTMLPreElement>("final-text");
const finalMeta = byId<HTMLElement>("final-meta");
const lastEventEl = byId<HTMLElement>("last-event");
const tree = byId<SVGSVGElement>("tree");
const detailBox = byId<HTMLDivElement>("detail");
const sseSeq = byId<HTMLElement>("sse-seq");
const sseState = byId<HTMLElement>("sse-state");
const sseDot = byId<HTMLElement>("sse-dot");
const healthEl = byId<HTMLSpanElement>("health");
const clockEl = byId<HTMLElement>("clock");

const settingInputs = {
  max_tokens: byId<HTMLInputElement>("max-tokens"),
  max_duration_sec: byId<HTMLInputElement>("max-duration"),
  max_depth: byId<HTMLInputElement>("max-depth"),
  max_concurrency: byId<HTMLInputElement>("max-concurrency"),
  max_agents: byId<HTMLInputElement>("max-agents"),
  agent_max_steps: byId<HTMLInputElement>("max-steps")
};

type State = {
  config: AppConfig | null;
  run: RunView | null;
  runID: string | null;
  selectedAgentID: string | null;
  detail: AgentDetail | null;
  detailLoading: boolean;
  applyInFlight: boolean;
  pendingFailNext: boolean;
};

const state: State = {
  config: null,
  run: null,
  runID: null,
  selectedAgentID: null,
  detail: null,
  detailLoading: false,
  applyInFlight: false,
  pendingFailNext: false
};

let refreshInFlight = false;
let refreshQueued = false;
let detailSequence = 0;
let refreshTimer: number | undefined;
let lastEventAt = 0;
let noticeTimer: number | undefined;

function setNotice(message: string, kind: "info" | "error" | "success" = "info"): void {
  window.clearTimeout(noticeTimer);
  noticeBox.hidden = false;
  noticeBox.className = `notice ${kind}`;
  noticeBox.textContent = message;
  noticeTimer = window.setTimeout(() => {
    noticeBox.hidden = true;
  }, kind === "error" ? 9000 : 5000);
}

function readSettings(): Settings {
  const read = (input: HTMLInputElement, label: string, min: number, max: number): number => {
    const value = Number(input.value);
    if (!Number.isInteger(value) || value < min || value > max) {
      throw new Error(`${label} 必须是 ${min}–${max} 的整数`);
    }
    return value;
  };
  return {
    max_tokens: read(settingInputs.max_tokens, "总 token 预算", 200, 500000),
    max_duration_sec: read(settingInputs.max_duration_sec, "总时长", 5, 3600),
    max_depth: read(settingInputs.max_depth, "最大深度", 0, 8),
    max_concurrency: read(settingInputs.max_concurrency, "最大并发", 1, 32),
    max_agents: read(settingInputs.max_agents, "最大 Agent 数", 1, 100),
    agent_max_steps: read(settingInputs.agent_max_steps, "单 Agent 步数", 1, 30)
  };
}

function writeSettings(settings: Settings): void {
  settingInputs.max_tokens.value = String(settings.max_tokens);
  settingInputs.max_duration_sec.value = String(settings.max_duration_sec);
  settingInputs.max_depth.value = String(settings.max_depth);
  settingInputs.max_concurrency.value = String(settings.max_concurrency);
  settingInputs.max_agents.value = String(settings.max_agents);
  settingInputs.agent_max_steps.value = String(settings.agent_max_steps);
}

function activeRun(): boolean {
  return state.run !== null && isActive(state.run.status);
}

function persistRunID(id: string | null): void {
  if (id) {
    window.localStorage.setItem("superagent.runID", id);
    window.history.replaceState(null, "", `#run=${encodeURIComponent(id)}`);
  } else {
    window.localStorage.removeItem("superagent.runID");
    window.history.replaceState(null, "", window.location.pathname);
  }
}

function renderRun(): void {
  const run = state.run;
  if (!run) {
    runStatus.textContent = "尚未运行";
    runStatus.className = "pill pill-muted";
    budgetText.textContent = "0 / 60,000 tokens";
    budgetPercent.textContent = "0%";
    budgetFill.style.width = "0%";
    budgetFill.className = "budget-fill";
    costText.textContent = "$0.0000";
    elapsedText.textContent = "0ms";
    agentCount.textContent = "0 agents";
    peakConcurrency.textContent = "peak 0";
    stageSubtitle.textContent = "提交任务后，树形图会随 SSE 事件逐步展开。点击节点查看独立上下文。";
    finalPanel.hidden = true;
    cancelBtn.disabled = true;
    applyBtn.disabled = false;
    failNextInput.disabled = false;
    failNextInput.checked = state.pendingFailNext;
    renderTree(tree, null, null, selectAgent);
    return;
  }

  runStatus.textContent = statusLabel(run.status);
  runStatus.className = `pill status-${run.status}`;
  budgetText.textContent = `${formatNumber(run.usage.total_tokens)} / ${formatNumber(run.settings.max_tokens)} tokens`;
  const percent = Math.max(0, run.budget_percent);
  budgetPercent.textContent = `${percent.toFixed(0)}%`;
  budgetFill.style.width = `${Math.min(100, percent)}%`;
  budgetFill.className = `budget-fill${percent >= 85 ? " warm" : ""}${run.budget_hit ? " over" : ""}`;
  budgetPercent.className = `budget-percent${percent >= 85 ? " warm" : ""}`;
  costText.textContent = formatCost(run.cost_usd);
  elapsedText.textContent = formatDuration(run.elapsed_ms);
  agentCount.textContent = `${run.total_agents} agents · ${run.active_agents} active`;
  peakConcurrency.textContent = `peak ${run.max_observed_concurrency}`;
  stageSubtitle.textContent = `${run.goal.slice(0, 100)}${run.goal.length > 100 ? "…" : ""}`;

  const active = isActive(run.status);
  cancelBtn.disabled = !active;
  applyBtn.disabled = !active || state.applyInFlight;
  failNextInput.disabled = !active;
  failNextInput.checked = active ? run.fail_next : state.pendingFailNext;

  if (run.final_answer) {
    finalPanel.hidden = false;
    finalText.textContent = run.final_answer;
    finalMeta.textContent = `${statusLabel(run.status)} · ${formatNumber(run.usage.total_tokens)} tokens · ${formatCost(run.cost_usd)}`;
  } else {
    finalPanel.hidden = true;
  }

  renderTree(tree, run, state.selectedAgentID, selectAgent);
}

async function refreshRun(): Promise<void> {
  if (!state.runID) {
    renderRun();
    return;
  }
  if (refreshInFlight) {
    refreshQueued = true;
    return;
  }
  refreshInFlight = true;
  try {
    const next = await getRun(state.runID);
    if (state.runID !== next.id) return;
    state.run = next;
    if (!state.selectedAgentID || !next.agents.some((agent) => agent.id === state.selectedAgentID)) {
      state.selectedAgentID = next.root_agent_id || next.agents[0]?.id || null;
    }
    renderRun();
    if (state.selectedAgentID) void loadDetail(state.selectedAgentID);
  } catch (error) {
    if (error instanceof ApiError && error.status === 404) {
      state.runID = null;
      state.run = null;
      persistRunID(null);
    }
  } finally {
    refreshInFlight = false;
    if (refreshQueued) {
      refreshQueued = false;
      void refreshRun();
    }
  }
}

function scheduleRefresh(): void {
  window.clearTimeout(refreshTimer);
  refreshTimer = window.setTimeout(() => void refreshRun(), 80);
}

async function loadDetail(agentID: string): Promise<void> {
  if (!state.runID) return;
  const runID = state.runID;
  const sequence = ++detailSequence;
  state.detailLoading = true;
  renderDetail(detailBox, null, true);
  try {
    const detail = await getAgent(runID, agentID);
    if (sequence !== detailSequence || state.selectedAgentID !== agentID || state.runID !== runID) return;
    state.detail = detail;
    renderDetail(detailBox, detail, false);
  } catch (error) {
    if (sequence !== detailSequence) return;
    state.detail = null;
    renderDetail(detailBox, null, false);
    setNotice(error instanceof Error ? error.message : "加载 Agent 详情失败", "error");
  } finally {
    if (sequence === detailSequence) state.detailLoading = false;
  }
}

function selectAgent(agentID: string): void {
  if (!state.run) return;
  if (state.selectedAgentID === agentID) return;
  state.selectedAgentID = agentID;
  state.detail = null;
  renderTree(tree, state.run, state.selectedAgentID, selectAgent);
  void loadDetail(agentID);
}

async function startTask(): Promise<void> {
  if (activeRun()) {
    const confirmed = window.confirm("当前任务仍在运行。启动新任务不会自动取消旧任务，继续？");
    if (!confirmed) return;
  }
  let settings: Settings;
  try {
    settings = readSettings();
  } catch (error) {
    setNotice(error instanceof Error ? error.message : "参数不合法", "error");
    return;
  }
  startBtn.disabled = true;
  startBtn.textContent = "启动中…";
  try {
    const run = await createRun(goalInput.value.trim(), settings);
    state.runID = run.id;
    state.run = run;
    state.selectedAgentID = run.root_agent_id || null;
    state.detail = null;
    state.pendingFailNext = failNextInput.checked;
    persistRunID(run.id);
    renderRun();
    setNotice("任务已启动。可点击节点查看独立上下文。", "success");
    if (state.pendingFailNext) {
      try {
        const updated = await setFailNext(run.id, true);
        state.run = updated;
        state.pendingFailNext = false;
        renderRun();
      } catch {
        // The run may already have completed; the main task is unaffected.
      }
    }
    if (state.selectedAgentID) void loadDetail(state.selectedAgentID);
  } catch (error) {
    setNotice(error instanceof Error ? error.message : "启动任务失败", "error");
  } finally {
    startBtn.disabled = false;
    startBtn.textContent = "启动演示任务";
  }
}

async function applySettings(): Promise<void> {
  if (!state.run) {
    try {
      readSettings();
      setNotice("参数有效，将在启动新任务时使用。", "info");
    } catch (error) {
      setNotice(error instanceof Error ? error.message : "参数不合法", "error");
    }
    return;
  }
  if (!activeRun()) {
    setNotice("任务已结束。当前参数会保留给下一次启动。", "info");
    return;
  }
  let settings: Settings;
  try {
    settings = readSettings();
  } catch (error) {
    setNotice(error instanceof Error ? error.message : "参数不合法", "error");
    return;
  }
  state.applyInFlight = true;
  applyBtn.disabled = true;
  try {
    state.run = await updateSettings(state.run.id, settings);
    renderRun();
    setNotice("设置已实时应用。", "success");
  } catch (error) {
    setNotice(error instanceof Error ? error.message : "应用设置失败", "error");
  } finally {
    state.applyInFlight = false;
    applyBtn.disabled = !activeRun();
  }
}

async function cancelTask(): Promise<void> {
  if (!state.run || !activeRun()) return;
  cancelBtn.disabled = true;
  try {
    state.run = await cancelRun(state.run.id);
    renderRun();
    setNotice("已发送取消请求，等待所有 Agent 停止。", "info");
  } catch (error) {
    setNotice(error instanceof Error ? error.message : "取消失败", "error");
    cancelBtn.disabled = !activeRun();
  }
}

async function toggleFailNext(): Promise<void> {
  if (!state.run || !activeRun()) {
    state.pendingFailNext = failNextInput.checked;
    setNotice("已设置；任务启动后第一个子 Agent 会失败。", "info");
    return;
  }
  const enabled = failNextInput.checked;
  failNextInput.disabled = true;
  try {
    state.run = await setFailNext(state.run.id, enabled);
    renderRun();
    setNotice(enabled ? "已开启：下一个子 Agent 将失败。" : "已关闭失败注入。", "info");
  } catch (error) {
    failNextInput.checked = !enabled;
    setNotice(error instanceof Error ? error.message : "切换失败注入失败", "error");
  } finally {
    failNextInput.disabled = !activeRun();
  }
}

function connectSSE(): void {
  const source = new EventSource("/api/stream");
  const types = [
    "run.created",
    "run.started",
    "run.completed",
    "run.failed",
    "run.cancelled",
    "run.cancelling",
    "run.stopping",
    "run.settings_updated",
    "run.fail_next_changed",
    "agent.created",
    "agent.started",
    "agent.waiting",
    "agent.running",
    "agent.completed",
    "agent.failed",
    "llm.call",
    "tool.call",
    "tool.result"
  ];

  source.addEventListener("hello", (event) => {
    const payload = JSON.parse((event as MessageEvent<string>).data) as { seq: number };
    sseSeq.textContent = String(payload.seq);
    sseState.textContent = "在线";
    sseState.className = "pill status-completed";
    sseDot.className = "dot online";
  });

  source.addEventListener("tick", (event) => {
    const payload = JSON.parse((event as MessageEvent<string>).data) as { seq: number };
    sseSeq.textContent = String(payload.seq);
    if (Date.now() - lastEventAt > 2500 && activeRun()) {
      lastEventAt = Date.now();
      void refreshRun();
    }
  });

  for (const type of types) {
    source.addEventListener(type, (event) => {
      const payload = JSON.parse((event as MessageEvent<string>).data) as {
        seq: number;
        run_id?: string;
        agent_id?: string;
      };
      sseSeq.textContent = String(payload.seq);
      if (!payload.run_id || !state.run || payload.run_id !== state.run.id) return;
      lastEventAt = Date.now();
      lastEventEl.textContent = `#${payload.seq} ${type}${payload.agent_id ? ` · ${payload.agent_id}` : ""}`;
      scheduleRefresh();
    });
  }

  source.onopen = () => {
    sseState.textContent = "在线";
    sseState.className = "pill status-completed";
    sseDot.className = "dot online";
  };

  source.onerror = () => {
    sseState.textContent = "重连中";
    sseState.className = "pill status-waiting";
    sseDot.className = "dot offline";
  };
}

async function loadHealth(): Promise<void> {
  try {
    const response = await fetch("/health", { cache: "no-store" });
    if (!response.ok) throw new Error(String(response.status));
    const health = (await response.json()) as { status: string; version: string };
    healthEl.textContent = `${health.status} · ${health.version}`;
    healthEl.className = "pill status-completed";
  } catch {
    healthEl.textContent = "后端不可达";
    healthEl.className = "pill status-failed";
  }
}

function startClock(): void {
  const tick = (): void => {
    clockEl.textContent = new Date().toLocaleTimeString("zh-CN", { hour12: false });
  };
  tick();
  window.setInterval(tick, 1000);
}

async function init(): Promise<void> {
  startClock();
  connectSSE();
  void loadHealth();
  window.setInterval(() => void loadHealth(), 30_000);

  try {
    state.config = await getConfig();
    providerLabel.textContent = `${state.config.provider} · ${state.config.model}`;
    writeSettings(state.config.default_settings);
  } catch (error) {
    setNotice(error instanceof Error ? error.message : "加载配置失败", "error");
  }

  startBtn.addEventListener("click", () => void startTask());
  cancelBtn.addEventListener("click", () => void cancelTask());
  applyBtn.addEventListener("click", () => void applySettings());
  failNextInput.addEventListener("change", () => void toggleFailNext());

  const params = new URLSearchParams(window.location.hash.slice(1));
  const restored = params.get("run") ?? window.localStorage.getItem("superagent.runID");
  if (restored) {
    state.runID = restored;
    await refreshRun();
    if (state.run) {
      setNotice(`已恢复任务 ${restored}`, "info");
      if (state.config) writeSettings(state.run.settings);
    } else {
      state.runID = null;
      persistRunID(null);
      renderRun();
    }
  } else {
    renderRun();
  }

  window.setInterval(() => {
    if (activeRun()) void refreshRun();
  }, 3000);
}

void init();
