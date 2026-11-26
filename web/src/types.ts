export type Settings = {
  max_tokens: number;
  max_duration_sec: number;
  max_depth: number;
  max_concurrency: number;
  max_agents: number;
  agent_max_steps: number;
};

export type Usage = {
  input_tokens: number;
  output_tokens: number;
  total_tokens: number;
};

export type AgentView = {
  id: string;
  parent_id?: string;
  role: string;
  task: string;
  depth: number;
  status: string;
  step: number;
  is_root: boolean;
  child_count: number;
  created_at: string;
  started_at?: string;
  ended_at?: string;
  duration_ms: number;
  input_tokens: number;
  output_tokens: number;
  total_tokens: number;
  cost_usd: number;
  error_code?: string;
  error?: string;
  result_summary?: string;
};

export type RunView = {
  id: string;
  goal: string;
  status: string;
  settings: Settings;
  created_at: string;
  started_at?: string;
  ended_at?: string;
  root_agent_id: string;
  final_answer?: string;
  error?: string;
  stop_reason?: string;
  budget_hit: boolean;
  usage: Usage;
  cost_usd: number;
  budget_percent: number;
  elapsed_ms: number;
  total_agents: number;
  active_agents: number;
  max_observed_concurrency: number;
  fail_next: boolean;
  agents: AgentView[];
};

export type ContextPacket = {
  goal: string;
  facts: string[];
  upstream: string[];
  constraints: string[];
};

export type ToolCall = {
  id: string;
  type: string;
  function: { name: string; arguments: string };
};

export type Message = {
  role: string;
  content?: string;
  name?: string;
  tool_call_id?: string;
  tool_calls?: ToolCall[];
};

export type CallRecord = {
  id: string;
  agent_id: string;
  step: number;
  provider: string;
  model: string;
  input_tokens: number;
  output_tokens: number;
  total_tokens: number;
  cost_usd: number;
  duration_ms: number;
  started_at: string;
  error?: string;
  finish_reason?: string;
  message_count: number;
  tool_call_count: number;
  concurrent_active: number;
};

export type ToolTrace = {
  id: string;
  agent_id: string;
  name: string;
  args: string;
  result: string;
  status: string;
  error?: string;
  child_id?: string;
  started_at: string;
  duration_ms: number;
};

export type AgentResult = {
  summary: string;
  artifact: string;
  self_check?: Record<string, unknown>;
  final_answer?: string;
};

export type AgentDetail = AgentView & {
  context: ContextPacket;
  messages: Message[];
  calls: CallRecord[];
  tools: ToolTrace[];
  result?: AgentResult;
};

export type AppConfig = {
  provider: string;
  model: string;
  default_settings: Settings;
  max_context_chars: number;
  llm_max_concurrency: number;
  pricing: { input_usd_per_mtok: number; output_usd_per_mtok: number };
};

export type RunSummary = {
  id: string;
  goal: string;
  status: string;
  created_at: string;
  total_tokens: number;
  max_tokens: number;
  agent_count: number;
};
