# AI 会话与 trace 说明

## 使用的工具与模型

- **AI 工具**：DeepSeek Harness（DSH）桌面/Web Harness
- **模型**：`deepseek-flash`
- **任务工作目录**：`/Users/zxy/Downloads/agent-g`
- **会话 ID**：`session-eb7670b4-1c88-43b8-8316-334f088aa6c6`
- **时间范围**：2026-11-26 13:02 起，至本次最终导出

## 文件

| 文件 | 内容 |
| :--- | :--- |
| `session-eb7670b4-1c88-43b8-8316-334f088aa6c6.jsonl` | 从 DSH `.jsonl.zstd` 解压出的完整会话（1,310 条记录），所有 user/assistant 消息、reasoning、工具调用与结果按 `seq` 顺序保留 |
| `raw/session-eb7670b4-1c88-43b8-8316-334f088aa6c6.jsonl.zstd` | DSH 原始压缩文件副本，未修改 |
| `session-...md` | 可读导出：按时间顺序展开每条用户提示、模型回复（含完整 reasoning）和工具调用/结果 |
| `trace-session-...jsonl` | 190 次工具调用的结构化 trace：turn、step、call_id、工具名、参数、结果、错误标志、耗时 |
| `usage-heatmap/usage-heatmap.png` | 最近 3 个月（2026-08-24 至 2026-11-26）的每日会话数/消息数/Token 热力图 |
| `usage-heatmap/daily-usage.csv` | 热力图原始日粒度数据 |

## 导出方式

DSH 将会话保存在：

```text
~/.dsh/sessions/<workspace-key>/<session-id>/session.v4.jsonl.zstd
```

本任务使用的 DSH 版本没有 `/export` 命令，因此采用以下等价方式：

```bash
zstd -dc ~/.dsh/sessions/--Users-zxy-Downloads-agent-g--/session-eb7670b4-1c88-43b8-8316-334f088aa6c6/session.v4.jsonl.zstd \
  > ai-logs/session-eb7670b4-1c88-43b8-8316-334f088aa6c6.jsonl
```

- `raw/` 中保留了原始 `.jsonl.zstd`，保证未修改的原始记录可复核；
- `.md` 与 `trace-*.jsonl` 是从同一份 JSONL 生成的只读派生物；
- 本次会话是本工作目录下唯一的 DSH session，因此 `ai-logs/` 包含从开始答题到导出时的全部会话，不是摘要。

## 脱敏

导出前后执行了以下模式扫描：

- `sk-...`
- `ghp_...`
- `github_pat_...`
- `xox[baprs]-...`
- `Bearer ...`

扫描结果为 0 处明文命中。仓库中没有任何真实 API key；如后续日志出现密钥，唯一允许的修改是替换为
`[REDACTED]`。本次导出没有做其他内容修改。

## 对应提交

本会话产生了仓库的全部提交，按阶段包括：

- `2362e3d` docs: initialize project README and implementation plan
- `5819e0f` feat: add configurable Go server with health endpoint
- `f01982f` feat: stream sequenced SSE events with replay
- `f45f4ca` feat: add Vite TypeScript shell and one-command dev startup
- `4b08d65` feat: add concurrent LLM client with usage and cost accounting
- `631bf20` feat: add budgeted recursive multi-agent orchestrator
- `7231d17` feat: build live agent tree and orchestration dashboard
- `d5316a3` fix: wire synchronous root creation and API constructor
- `2e3713c` build: add production static serving and container setup

README 最终版本、热力图最终数据与本次 `ai-logs/` 更新会作为后续文档提交；最后一次导出可能只修改
`ai-logs/`，这是任务书允许的例外。

## 用量热力图口径

- 数据源：本机 `~/.dsh/sessions/*/*/session.v4.jsonl.zstd`，共 15 个本地 session 文件，其中 12 个有消息；
- 会话数：当天有 user/assistant 消息的去重 session 数；
- 消息数：user + assistant 消息条数；
- Token：assistant 消息 `usage.totalTokens` 之和；
- 覆盖范围：2026-08-24 至 2026-11-26（最近 3 个月，14 个自然周）；
- **数据限制**：本机在 2026-11-02 之前没有 DSH 会话记录，因此 8–10 月显示为 0，不做伪造补数；
- 生成工具：Pillow（本地数据），未裁剪、未修改日期；
- 统计快照：约 465M tokens、2,271 条消息。最终数字以 `daily-usage.csv` 与最终导出的图片为准。

## 备注

- 任务过程中没有启动子 Agent 会话；所有工具调用都发生在这个主 DSH session 中。
- 运行系统自身的 Agent 对话不在 `ai-logs/` 中，它们属于演示产物，保存在被 `.gitignore` 排除的
  `data/runs/<run_id>.jsonl`；两者不要混淆。
