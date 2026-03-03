# Hivemind
Semi-autonomous Go orchestrator that turns directives into reviewed code changes with worker execution and operational visibility.

---

## What it does
Hivemind receives a high-level directive and turns it into an executable plan. The planner uses GLM to break work into tasks, the launcher starts Codex workers on isolated Git branches, and the evaluator reviews each worker result before deciding next actions. State is persisted in SQLite so progress, events, and worker lifecycle survive restarts. Operators interact through the dashboard and Telegram, where the system sends completion, failure, review, and input-required notifications.

## Architecture
```text
+-----------------------------------------------------------+
|                    YOU (Decision maker)                   |
|               Telegram / Dashboard / Claude Code          |
+-----------------------------+-----------------------------+
                              |
                              v
+-----------------------------------------------------------+
|                ORCHESTRATOR (single Go binary)            |
|                                                           |
|  +----------+  +----------+  +------------+               |
|  | Planner  |  | Launcher |  | Evaluator  |               |
|  |  (GLM)   |  | (Codex)  |  |   (GLM)    |               |
|  +----------+  +----------+  +------------+               |
|  +----------+  +----------+  +------------+               |
|  |  State   |  | Notifier |  | Consultant |               |
|  | (SQLite) |  |(Telegram)|  |(Claude/    |               |
|  +----------+  +----------+  | Gemini API)|               |
|                               +------------+              |
+-----------------------------+-----------------------------+
                              |
                              v
+-----------------------------------------------------------+
|                     CODEX WORKERS                         |
|                                                           |
|  +---------+  +---------+  +---------+  +---------+      |
|  |Worker 1 |  |Worker 2 |  |Worker 3 |  |QA Worker|      |
|  |feature/ |  |feature/ |  |feature/ |  |testing  |      |
|  +---------+  +---------+  +---------+  +---------+      |
|                                                           |
|  Each worker: isolated branch + AGENTS context + cache    |
+-----------------------------------------------------------+
                              |
                              v
+-----------------------------------------------------------+
|                     DASHBOARD WEB                         |
|                                                           |
|  View 1: Global state                                     |
|  View 2: Project progress                                 |
|  View 3: Project context                                  |
+-----------------------------------------------------------+
```

The orchestrator is a single process that runs planning, execution, evaluation, API serving, and bot handling. A directive enters through stdin or Telegram, then the planner creates a task graph. Workers execute tasks in separate branches, while state updates flow into SQLite and are exposed through dashboard endpoints. The notifier reports key milestones and waits for operator decisions when required.

## Components
| Component | Package | Role |
|---|---|---|
| Planner | `internal/planner` | Breaks directives into executable task plans. |
| Launcher | `internal/launcher` | Starts Codex sessions and monitors worker lifecycle. |
| Evaluator | `internal/evaluator` | Reviews worker output and chooses next action. |
| State | `internal/state` | Stores projects, tasks, workers, and events in SQLite. |
| LLM clients | `internal/llm` | Wraps GLM and optional Claude/Gemini clients. |
| Dashboard | `internal/dashboard` | Exposes REST endpoints and serves embedded dashboard UI. |
| Notifier | `internal/notify` | Sends Telegram alerts and processes operator commands. |

## Stack
| Technology | Purpose |
|---|---|
| Go 1.22 | Orchestrator runtime |
| SQLite | Persistent state store |
| GLM API | Primary planning and evaluation |
| Codex CLI | Worker execution engine |
| Claude/Gemini APIs | Optional consultant opinions |
| Telegram Bot API | Operator notifications and commands |
| k3s | Cluster deployment target |
| Cloudflare Tunnel | Private dashboard exposure |

## Project structure
```text
cmd/
  orchestrator/                 # Binary entrypoint and interactive loop
internal/
  planner/                      # Task planning pipeline
  launcher/                     # Worker lifecycle and branch handling
  evaluator/                    # Output evaluation and retries
  state/                        # SQLite schema, migrations, queries
  llm/                          # GLM, Claude, Gemini clients
  dashboard/                    # REST handlers and server wiring
  notify/                       # Telegram bot and message formatter
  directive/                    # Directive routing parser
agents/                         # Per-project AGENTS context files
prompts/                        # Planner/evaluator/consultant prompts
dashboard/
  src/                          # Frontend source
  dist/                         # Built assets embedded in Go binary
deploy/
  orchestrator.yaml             # Main k3s deployment/service
  cloudflare-tunnel.yaml        # Optional dedicated tunnel deployment
templates/
  AGENTS_TEMPLATE.md            # Baseline AGENTS template
sessions/
  cache/                        # Worker session cache/log artifacts
```

## Getting started
### Prerequisites
- Go `1.22+`
- `make`
- `git`
- SQLite `3.x` (CLI optional, useful for inspection)

### Clone and build
```bash
git clone https://github.com/Zyrakk/hivemind.git
cd hivemind
go mod tidy
make build
```

### Configure runtime
```bash
cp config.yaml.example config.yaml
```

`config.yaml.example` is the baseline reference. Start with `glm`, `consultants`, `telegram`, `dashboard`, `database`, `git`, and `codex` sections. Set your API key environment variable names and verify the branch and repo path settings before launching workers. Current runtime code reads `codex.repos_dir` in `cmd/orchestrator/main.go`, while the example file still shows `codex.workers_dir`.

### Run locally
```bash
export ZAI_API_KEY="your-zai-key"
# Optional Telegram integration:
# export TELEGRAM_BOT_TOKEN="your-bot-token"
# export TELEGRAM_CHAT_ID="123456789"

make run
```

Interactive mode starts only when stdin is a TTY. Type directives directly, or route explicitly with `Project: <project>. Directive: <work item>`. Use `exit` or `quit` to stop.

### Run tests
```bash
make test
make vet
```

Equivalent direct commands:
```bash
go test ./...
go vet ./...
```

## Configuration reference
| Section | What it controls | Key fields |
|---|---|---|
| `glm` (llm) | Primary planning and evaluation model client | `api_key_env`, `model`, `base_url`, `timeout` |
| `consultants` | Optional secondary model clients and guardrails | `enabled`, `api_key_env`, `model`, `max_calls_per_day` |
| `telegram` | Bot authentication and command access scope | `bot_token_env`, `allowed_chat_id` |
| `git` | Worker branch naming and remote push target | `default_remote`, `branch_prefix` |
| `dashboard` | HTTP server bind address for API and UI | `host`, `port` |
| `codex` (workers) | Worker execution mode and time limits | `approval_mode`, `timeout_minutes`, `workers_dir` / `repos_dir` |

## Deployment
Hivemind is designed for k3s deployment with manifests in `deploy/`. The default architecture runs orchestrator, dashboard API/UI, and Telegram bot in the same pod (`hivemind-orchestrator`). `deploy/orchestrator.yaml` defines namespace, PVC, deployment, and service; `deploy/cloudflare-tunnel.yaml` exposes the dashboard through Cloudflare Tunnel. Keep secrets out of Git by copying `deploy/secrets.yaml.example` to `deploy/secrets.yaml` locally.

```bash
cp deploy/secrets.yaml.example deploy/secrets.yaml
kubectl apply -f deploy/secrets.yaml
kubectl apply -f deploy/orchestrator.yaml
# Optional dedicated tunnel deployment:
# kubectl apply -f deploy/cloudflare-tunnel.yaml
```

## Telegram bot
| Command | Description |
|---|---|
| `/run {project} {directive}` | Create a plan and register it for approval. |
| `/status` | Show global project and worker summary. |
| `/project {name}` | Show detailed state for one project. |
| `/approve {id}` | Approve plan, PR, or input request. |
| `/reject {id} {reason}` | Reject pending approval with reason. |
| `/pause {project}` | Pause project workers and set project paused. |
| `/resume {project}` | Resume paused workers and project execution. |
| `/consult {question}` | Ask an enabled consultant model. |
| `/pending` | List active pending approvals. |
| `/help` | Show command reference. |

The bot sends notifications for task completed, worker failed, PR ready, needs input, and budget warning events.

## AGENTS.md system
`AGENTS.md` files are structured project memory for both humans and workers. They encode project goals, architecture decisions, constraints, run/test commands, and session history so each worker starts with stable context. Use `templates/AGENTS_TEMPLATE.md` to create new project context files consistently. The `agents/` directory stores per-project context documents consumed by the orchestrator workflow.

## Development
### Add a new project
1. Create `agents/<project>.md` from `templates/AGENTS_TEMPLATE.md`.
2. Register runtime settings in `config.yaml` for repo location and Git defaults (`codex`, `git`).
3. Add the project record to SQLite through the dashboard API.

```bash
curl -sS -X POST http://127.0.0.1:8080/api/projects \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "my-project",
    "description": "Short description",
    "status": "working",
    "repo_url": "https://github.com/acme/my-project",
    "agents_md_path": "agents/my-project.md"
  }'
```

### Run interactive orchestrator mode
```bash
make run
# Directive (default: flux) > Implement health endpoint
# Execute plan? (y/n/edit): y
```

If stdin is not a TTY, interactive prompts are disabled and only the HTTP server runs.

### Makefile targets
| Target | Description |
|---|---|
| `build` | Compile `cmd/orchestrator` into `bin/orchestrator`. |
| `test` | Run all Go tests. |
| `vet` | Run `go vet` across all packages. |
| `run` | Start orchestrator from source. |
| `docker-build` | Build container image from `Dockerfile`. |
| `deploy` | Apply all manifests in `deploy/` with `kubectl`. |

## Roadmap
| Phase | Status |
|---|---|
| Phase 0 - foundations | done |
| Phase 1 - dashboard web v1 | in progress |
| Phase 2 - GLM orchestrator v1 | in progress |
| Phase 3 - notifications and communication | in progress |
| Phase 4 - automated testing and self-correction | planned |
| Phase 5 - self-improvement and metrics | planned |

See `docs/roadmap.md` for full phase details.

## License
TBD (no `LICENSE` file is currently present in this repository).
