import type {
  AgentDetail,
  AppConfig,
  RunSummary,
  RunView,
  Settings
} from "./types";

export class ApiError extends Error {
  status: number;
  code: string;

  constructor(status: number, code: string, message: string) {
    super(message);
    this.status = status;
    this.code = code;
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(path, {
    ...init,
    headers: {
      "Content-Type": "application/json",
      ...(init?.headers ?? {})
    }
  });
  if (!response.ok) {
    let code = `http_${response.status}`;
    let message = `请求失败（HTTP ${response.status}）`;
    try {
      const payload = (await response.json()) as { error?: { code?: string; message?: string } };
      if (payload.error?.code) code = payload.error.code;
      if (payload.error?.message) message = payload.error.message;
    } catch {
      // Keep the HTTP fallback message.
    }
    throw new ApiError(response.status, code, message);
  }
  return (await response.json()) as T;
}

export function getConfig(): Promise<AppConfig> {
  return request<AppConfig>("/api/config");
}

export function createRun(goal: string, settings: Settings): Promise<RunView> {
  return request<RunView>("/api/runs", {
    method: "POST",
    body: JSON.stringify({ goal, settings })
  });
}

export function getRun(id: string): Promise<RunView> {
  return request<RunView>(`/api/runs/${encodeURIComponent(id)}`);
}

export function getAgent(runID: string, agentID: string): Promise<AgentDetail> {
  return request<AgentDetail>(
    `/api/runs/${encodeURIComponent(runID)}/agents/${encodeURIComponent(agentID)}`
  );
}

export function listRuns(): Promise<{ runs: RunSummary[] }> {
  return request<{ runs: RunSummary[] }>("/api/runs");
}

export function updateSettings(id: string, settings: Settings): Promise<RunView> {
  return request<RunView>(`/api/runs/${encodeURIComponent(id)}/settings`, {
    method: "PATCH",
    body: JSON.stringify(settings)
  });
}

export function setFailNext(id: string, enabled: boolean): Promise<RunView> {
  return request<RunView>(`/api/runs/${encodeURIComponent(id)}/fail-next`, {
    method: "POST",
    body: JSON.stringify({ enabled })
  });
}

export function cancelRun(id: string): Promise<RunView> {
  return request<RunView>(`/api/runs/${encodeURIComponent(id)}/cancel`, {
    method: "POST",
    body: "{}"
  });
}
