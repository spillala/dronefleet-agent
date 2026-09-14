# dronefleet-agent

Agent 1 for [dronefleet](https://github.com/spillala/dronefleet): an LLM-driven
Go program that connects to [`dronefleet-mcp`](https://github.com/spillala/dronefleet-mcp)
as an MCP client and autonomously investigates faults in the simulated fleet.

This is **Phase 3** of the [K8s Autonomous SRE Agent](https://github.com/spillala)
portfolio project.

---

## Scope: diagnosis, not remediation

Agent 1 only ever calls the read/diagnostic tools — `get_fleet_status`,
`get_drone`, `list_missions`, `get_mission`, and `simulate_fault` (kept
around so the loop is demoable end-to-end). It never calls `recover_drone`:
that's Agent 2's job (Phase 4, not yet built). If Agent 1 decides a drone
needs recovery, it says so in its findings and stops there.

## Architecture

```
┌──────────────────────────────┐
│        dronefleet-agent      │
│ (this repo)                  │
│                               │
│  ┌─────────────────────────┐ │
│  │  Ollama-compatible LLM  │ │   HTTP · /api/chat (tools)
│  │  e.g. gemma4:e2b        │◄├────────────────────────┐
│  └───────────┬─────────────┘ │                        │
│              │ decides which │              ┌─────────┴────────┐
│              │ tool to call  │              │  Ollama server    │
│  ┌───────────▼─────────────┐ │              │  (sidecar)        │
│  │   MCP client (stdio)    │ │              └────────────────────┘
│  └───────────┬─────────────┘ │
└──────────────┼────────────────┘
               │ MCP / stdio (spawns the subprocess)
               ▼
┌──────────────────────────────┐
│        dronefleet-mcp        │
│  6 tools → HTTP REST         │
└──────────────┬────────────────┘
               ▼
┌──────────────────────────────┐
│         dronefleet API       │
│  (Postgres-backed)           │
└──────────────────────────────┘
```

The LLM is swappable without touching this code: point `-ollama-url` /
`OLLAMA_URL` at any Ollama-API-compatible server, or swap `internal/llm` for
a Claude client later — the MCP side doesn't change either way, matching
`dronefleet-mcp`'s own design note.

## Prerequisites

- Go 1.25+
- A running `dronefleet` instance (see its README) — the API this agent
  investigates, via `dronefleet-mcp`
- The `dronefleet-mcp` binary, built from its own repo
- [Ollama](https://ollama.com), with a tool-calling-capable model pulled —
  this project defaults to `gemma4:e2b-it-q4_K_M`; use `gemma4:e4b-it-q4_K_M`
  instead if your machine has more headroom (roughly 6GB+ free RAM for e2b,
  9GB+ for e4b — see "Memory behaviour" below for what that actually costs
  while running)

## Running locally

```bash
# 1. dronefleet (separate repo) — in-memory store is fine for this
cd ../dronefleet && go run ./cmd/server &

# 2. Ollama, with the model pulled once
ollama serve &
ollama pull gemma4:e2b-it-q4_K_M

# 3. dronefleet-mcp binary available somewhere on disk
cd ../dronefleet-mcp && go build -o dronefleet-mcp ./cmd/server

# 4. the agent itself
cd ../dronefleet-agent
go build -o dronefleet-agent ./cmd/agent
./dronefleet-agent \
  -mcp-server ../dronefleet-mcp/dronefleet-mcp \
  -dronefleet-url http://localhost:8080 \
  -ollama-url http://localhost:11434 \
  -model gemma4:e2b-it-q4_K_M
```

Add `-watch 5m` to re-run the diagnosis every 5 minutes instead of exiting
after one pass.

## Memory behaviour

The agent sends `keep_alive: "30s"` on every `/api/chat` call
(`ollamaKeepAlive` in `internal/agent/agent.go`), so Ollama unloads the
model 30 seconds after the last turn of a pass. It only has to outlast the
gaps *between* turns — MCP tool calls, sub-second — so it can be short.

Without it, Ollama's default is 5 minutes. With `-watch 5m` that is the same
period as the reconcile loop, so the model never unloaded and stayed resident
continuously; on a shared 14GB dev box that starved the MicroK8s control
plane. Measured on that box (CPU inference, no GPU):

- `gemma4:e2b-it-q4_K_M` costs ~3GB of anonymous RSS while loaded. The
  ~6.7GB Ollama reports is mostly mmap'd weights sitting in reclaimable page
  cache.
- One diagnose pass takes ~2m45s (two to three chat turns at roughly a minute
  each), so with `-watch 5m` the model is resident about 65% of the time.
  Faster inference or a longer `-watch` are the only bigger levers.

If you want the model to stay warm (e.g. an interactive single pass), the
constant is the one place to change.

## Configuration

| Flag | Env var | Default | Description |
|---|---|---|---|
| `-mcp-server` | `DRONEFLEET_MCP_PATH` | `./dronefleet-mcp` | Path to the `dronefleet-mcp` binary to spawn |
| `-dronefleet-url` | `DRONEFLEET_URL` | `http://localhost:8080` | Passed through to `dronefleet-mcp`'s own env |
| `-ollama-url` | `OLLAMA_URL` | `http://localhost:11434` | Base URL of the Ollama (or compatible) server |
| `-model` | `OLLAMA_MODEL` | `gemma4:e2b-it-q4_K_M` | Model tag to request |
| `-watch` | — | `0` (single pass) | Re-run interval, e.g. `5m` |

## Docker Compose

Bundles the agent and an Ollama sidecar. Assumes `dronefleet` is already
running on the host (see `DRONEFLEET_URL` in `docker-compose.yml` if it
lives elsewhere, e.g. on a shared Docker network instead).

```bash
docker compose up --build -d
docker compose exec ollama ollama pull gemma4:e2b-it-q4_K_M
docker compose restart agent
docker compose logs -f agent
```

## Project structure

```
dronefleet-agent/
├── cmd/agent/main.go             # CLI entrypoint, flag/env wiring, watch loop
├── internal/mcpclient/client.go  # spawns dronefleet-mcp, MCP stdio session
├── internal/llm/ollama.go        # minimal Ollama /api/chat client (with tools)
├── internal/agent/agent.go       # the reasoning loop + diagnostic tool allow-list
├── Dockerfile                    # bundles the agent + a built dronefleet-mcp
└── docker-compose.yml            # agent + Ollama sidecar
```

## Related repos

- [`dronefleet`](https://github.com/spillala/dronefleet) — the simulated
  fleet API
- [`dronefleet-mcp`](https://github.com/spillala/dronefleet-mcp) — the MCP
  server this agent drives

## License

MIT
