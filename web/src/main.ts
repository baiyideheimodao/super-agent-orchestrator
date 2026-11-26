import "./styles.css";

type Health = {
  status: string;
  service: string;
  version: string;
  uptime_seconds: number;
};

const healthEl = document.querySelector<HTMLSpanElement>("#health");
const sseSeqEl = document.querySelector<HTMLElement>("#sse-seq");
const sseStateEl = document.querySelector<HTMLElement>("#sse-state");
const sseDotEl = document.querySelector<HTMLElement>("#sse-dot");
const clockEl = document.querySelector<HTMLElement>("#clock");

function setSSEState(label: string, state: "online" | "offline" | "waiting"): void {
  if (sseStateEl) sseStateEl.textContent = label;
  if (sseDotEl) sseDotEl.className = `dot ${state}`;
}

async function loadHealth(): Promise<void> {
  try {
    const response = await fetch("/health", { cache: "no-store" });
    if (!response.ok) throw new Error(`${response.status}`);
    const health = (await response.json()) as Health;
    if (healthEl) {
      healthEl.textContent = `${health.status} · ${health.version}`;
      healthEl.classList.remove("pill-muted");
    }
  } catch {
    if (healthEl) {
      healthEl.textContent = "后端不可达";
      healthEl.classList.add("pill-muted");
    }
  }
}

function connectStream(): void {
  const source = new EventSource("/api/stream");

  source.addEventListener("hello", (event) => {
    const payload = JSON.parse((event as MessageEvent<string>).data) as { seq: number };
    if (sseSeqEl) sseSeqEl.textContent = String(payload.seq);
    setSSEState("在线", "online");
  });

  source.addEventListener("tick", (event) => {
    const payload = JSON.parse((event as MessageEvent<string>).data) as { seq: number };
    if (sseSeqEl) sseSeqEl.textContent = String(payload.seq);
    setSSEState("实时", "online");
  });

  source.onerror = () => {
    setSSEState("重连中", "offline");
  };
}

function startClock(): void {
  const render = (): void => {
    if (clockEl) clockEl.textContent = new Date().toLocaleTimeString("zh-CN", { hour12: false });
  };
  render();
  window.setInterval(render, 1000);
}

void loadHealth();
window.setInterval(() => void loadHealth(), 30_000);
connectStream();
startClock();
