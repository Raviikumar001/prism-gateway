# Prism

OpenAI-compatible LLM gateway with tenant isolation, budget controls, failover, streaming, and a semantic response cache.

Applications call one endpoint with a virtual key. Prism authenticates the key, enforces allowlists and rate limits, routes to the right model tier, serves cache hits when prompts match by meaning, and meters every token.

```
Client ──► Prism ──► Cerebras / OpenRouter / mock providers
              │
         Postgres + Redis
```

## Live deploy

Public Railway URL:

**https://gateway-production-e22b.up.railway.app**

| Path | URL |
|---|---|
| Health | https://gateway-production-e22b.up.railway.app/health |
| API base | https://gateway-production-e22b.up.railway.app/v1 |
| Chat completions | `POST https://gateway-production-e22b.up.railway.app/v1/chat/completions` |
| Ops console | https://gateway-production-e22b.up.railway.app/console/ |

Demo virtual key (API only): `prism-sk-research-4d5e6f`

### Try the API

```bash
curl -sS https://gateway-production-e22b.up.railway.app/health | python3 -m json.tool

curl -sS https://gateway-production-e22b.up.railway.app/v1/chat/completions \
  -H "Authorization: Bearer prism-sk-research-4d5e6f" \
  -H "Content-Type: application/json" \
  -d '{"model":"fast","messages":[{"role":"user","content":"Say hello"}],"max_tokens":32}' \
  | python3 -m json.tool
```

### Open the ops console

Open https://gateway-production-e22b.up.railway.app/console/ — it loads on its own. No admin token or virtual key.

You’ll see provider health, 24h spend, success rate, tenant budgets (team names only), and recent requests. Use **Refresh** or wait for the 15s auto-update.

Local console: `http://localhost:8080/console/` after `docker compose up`.

Programmatic admin APIs (`/admin/*`) still require `Authorization: Bearer <ADMIN_TOKEN>`. Chat completions still require a virtual key.

## Features

- **OpenAI Chat Completions API** — `POST /v1/chat/completions`, streaming and non-streaming
- **Virtual keys** — per-tenant allowlists, RPM limits, monthly spend caps with **pre-dispatch budget reservation**
- **Model aliases** — `fast`, `smart`, `code`, `sol`, `fable`, `glm`, and `auto` (feature-weighted difficulty routing)
- **Failover** — timeouts, retries with full jitter, ordered fallbacks, per-provider circuit breakers
- **Semantic cache** — exact-match then embedding similarity, scoped per key
- **Metering** — cost from upstream token usage and a price table; async request logs; usage API
- **Ops console** — usage, recent traffic, cache hit rate, provider / circuit-breaker health

## Stack

| Layer | Choice |
|---|---|
| API | Go 1.23+, [chi](https://github.com/go-chi/chi) on `net/http` |
| Primary store | PostgreSQL **17** + [pgvector](https://github.com/pgvector/pgvector) |
| Hot path | Redis **7.4** (RPM + spend counters via Lua) |
| Upstreams | OpenAI-compatible HTTP (Cerebras, OpenRouter, local mocks) |
| Console | Static UI served by the gateway |
| Deploy | Docker · Railway (Postgres + Redis plugins) |

Local Compose pins these majors. On Railway, use the platform Postgres/Redis plugins and connect via `DATABASE_URL` / `REDIS_URL` — prefer pinning the plugin image major so it does not auto-jump.

## Quick start

### Prerequisites

- Go 1.23+
- Docker / Docker Compose
- Python 3.9+ (optional local mock providers)

### Run locally

```bash
cp .env.example .env
docker compose up --build
```

Gateway: `http://localhost:8080`  
Health: `GET /health` (checks Postgres + Redis)  
Ops console: `http://localhost:8080/console/`

```bash
curl -s http://localhost:8080/health
```
### Mock providers (local failover drills)

Compose starts `mock-alpha` / `mock-beta` and loads `gateway_config.docker.json` by default (`PROVIDER_MODE=mocks`).

```bash
python3 scripts/mock_provider.py --port 9001 --name alpha
python3 scripts/mock_provider.py --port 9002 --name beta
```

Take a provider down without restarting:

```bash
curl -X POST http://localhost:9001/admin/config -d '{"mode":"down"}'
```

### Live providers (Cerebras + OpenRouter)

Put keys in `.env` (never commit them):

```
PROVIDER_MODE=live
CEREBRAS_API_KEY=...
OPENROUTER_API_KEY=...
```

With `PROVIDER_MODE=live` and no `GATEWAY_CONFIG` override, Prism loads `data/gateway_config.live.json`:

| Alias | Primary | Fallbacks |
|---|---|---|
| `fast` | OpenRouter `openai/gpt-5.6-luna` | GPT-4o mini → Mistral Nemo → Cerebras Gemma |
| `smart` | OpenRouter `openai/gpt-5.6-luna-pro` | GPT-5.4 → Claude Sonnet 5 → Cerebras `zai-glm-4.7` → Qwen3 Coder Plus |
| `code` | OpenRouter `qwen/qwen3-coder` | Codestral → Luna → DeepSeek V4 Flash |
| `sol` | OpenRouter `openai/gpt-5.6-sol` | Luna Pro → Claude Sonnet 5 |
| `fable` | OpenRouter `anthropic/claude-fable-5` | Claude Opus 4.8 → GPT-5.6 Sol |
| `glm` | OpenRouter `z-ai/glm-5.3-flash` | Cerebras `zai-glm-4.7` → Qwen3 Coder Flash |
| `auto` | difficulty → `fast` (simple) / `smart` (complex) | same chains as above |

Live catalog also exposes direct model IDs (OpenAI GPT-4.1/5.4/Luna/Sol, Claude Fable, GLM 5.3 Flash, Gemini, DeepSeek, Qwen coder, Cerebras GLM, …) for keys with `*` allowlist.

Live OpenRouter base URL: `https://openrouter.ai/api/v1`. We do **not** expose the full OpenRouter catalog — a curated mix of 18 active models covering cheap OSS plus Google / OpenAI / Anthropic. Research key allowlist includes `*` so those model IDs can be requested directly. Archived and scheduled-for-deprecation models are excluded.

Run against Compose Postgres/Redis without mocks:

```bash
docker compose up -d postgres redis
# export DATABASE_URL / REDIS_URL from .env, then:
PROVIDER_MODE=live go run ./cmd/prism
```

Or point Compose at live config (keys via `env_file: .env`):

```bash
PROVIDER_MODE=live GATEWAY_CONFIG=/data/gateway_config.live.json docker compose up --build gateway
```

## API

### Data plane

| Method | Path | Auth |
|---|---|---|
| `POST` | `/v1/chat/completions` | `Authorization: Bearer <virtual-key>` |
| `GET` | `/v1/models` | `Authorization: Bearer <virtual-key>` |
| `GET` | `/v1/models/{model-id}` | `Authorization: Bearer <virtual-key>` |
| `GET` | `/health` | none |

### Coding-agent clients

Prism preserves OpenAI Chat Completions fields such as `tools`, `tool_choice`,
assistant `tool_calls`, tool-result messages, sampling controls, structured
output options, and streaming tool-call deltas. Clients that accept an
OpenAI-compatible Chat Completions base URL can use:

```
base_url = http://localhost:8080/v1
api_key  = prism-sk-research-4d5e6f
model    = openai/gpt-4.1-mini  # or fast / smart / sol / fable / glm / auto
```

Tool-using and multi-turn requests bypass the semantic response cache so stale
tool calls cannot be replayed. If no output cap is supplied, Prism enforces
`max_tokens: 4096` for bounded budget admission.
RPM and TPM are enforced before upstream dispatch; TPM reserves the request's
prompt upper bound plus its output cap, so callers should set `max_tokens` to a
realistic value.

Known compatibility boundary: Prism implements Chat Completions and model
discovery, not OpenAI's `/v1/responses`, embeddings, audio, or fine-tuning APIs.

Run the live coding-agent contract check:

```bash
python3 scripts/coding_agent_test.py \
  --url http://localhost:8080 \
  --key prism-sk-research-4d5e6f \
  --model openai/gpt-4o-mini
```

### Admin

| Method | Path | Auth |
|---|---|---|
| `GET` | `/admin/usage?key=&from=&to=` | admin token |
| `GET` | `/admin/usage?key=&month=YYYYMM` | admin token (legacy single-month) |
| `GET` | `/admin/logs?key=&limit=` | admin token |
| `GET` | `/admin/cache/stats` | admin token |
| `GET` | `/admin/providers/health` | admin token |
| `GET` | `/console/` | HTML UI (no token) |
| `GET` | `/console/api/overview` | Public read-only dashboard JSON |

`from` / `to` accept `YYYY-MM-DD`, `YYYYMM`, or RFC3339. When omitted, usage defaults to the current UTC calendar month. The response includes `from`, `to`, `month`, aggregated token/cost totals, and `cache_hits`. Logs include `route_reason` and `retries`.

Admin auth: `Authorization: Bearer <ADMIN_TOKEN>`.

Errors use an OpenAI-style body with a distinct `error.type` (`authentication_error`, `model_not_allowed`, `rate_limit_exceeded`, `budget_exceeded`, `not_found_error`, `upstream_error`).

## Configuration

Seed data lives under `data/`:

- `seed_keys.json` — tenants, budgets, RPM, allowlists, cache settings
- `model_pricing.json` — USD per 1M input/output tokens
- `gateway_config.sample.json` — mock providers (local)
- `gateway_config.live.json` — Cerebras + OpenRouter (`api_key_env`, no secrets in file)
- `gateway_config.docker.json` — Compose mock URLs

Environment (see `.env.example`):

| Variable | Description |
|---|---|
| `PORT` | Listen port (Railway injects this) |
| `DATABASE_URL` | Postgres connection string |
| `REDIS_URL` | Redis connection string |
| `ADMIN_TOKEN` | Admin API auth (`/admin/*`) |
| `SEED_ON_BOOT` | Seed demo tenants/prices on startup (`true` by default) |
| `UPSTREAM_TIMEOUT` | Non-stream timeout and stream inactivity timeout (`120s`) |
| `PROVIDER_MODE` | `mocks` (default) or `live` |
| `GATEWAY_CONFIG` | Optional override; live defaults to `gateway_config.live.json` under `DATA_DIR` |
| `CEREBRAS_API_KEY` | Required when `PROVIDER_MODE=live` |
| `OPENROUTER_API_KEY` | Required when `PROVIDER_MODE=live` |

Per-key quotas (seeded in Postgres): `monthly_budget_usd`, RPM, **TPM** (tokens/minute), model allowlist, and semantic-cache threshold. RPM and TPM are both enforced before dispatch.

## Deploy on Railway

Prism is designed for Railway: one Go service plus managed Postgres and Redis.

**This project’s live URL:** https://gateway-production-e22b.up.railway.app  
Ops console: https://gateway-production-e22b.up.railway.app/console/

1. Create a Railway project; add **PostgreSQL** and **Redis**.
2. Deploy this repo (Dockerfile at repo root).
3. Set service variables:

```
DATABASE_URL=${{Postgres.DATABASE_URL}}
REDIS_URL=${{Redis.REDIS_URL}}
ADMIN_TOKEN=<secret>
PROVIDER_MODE=live
DATA_DIR=/data
CEREBRAS_API_KEY=...
OPENROUTER_API_KEY=...
```

`PROVIDER_MODE=live` selects `/data/gateway_config.live.json` unless `GATEWAY_CONFIG` is set. Keys are injected from env via `api_key_env` — do not bake secrets into the image.
4. Health check path: `/health`
5. Generate a public domain on the gateway service

The virtual keys in `data/seed_keys.json` are public demo credentials. Replace
them before exposing a deployment, then set `SEED_ON_BOOT=false` after initial
provisioning.

The process listens on `0.0.0.0:$PORT`. Migrations run on startup.

Details: [docs/DEPLOYMENT.md](docs/DEPLOYMENT.md)

## Architecture

Request path:

1. Authenticate virtual key
2. Allowlist → RPM / TPM
3. Resolve alias (`auto` uses feature-weighted difficulty)
4. Cache (exact hash, then semantic; skipped for tools, multi-turn, and time-sensitive prompts)
5. **Budget reserve** using the output cap and most expensive fallback
6. Upstream with timeout / retry+jitter / **circuit breaker** / cross-vendor failover
7. Stream or return; meter from provider `usage`; **settle reservation**
8. Enqueue request log + update usage

Ops console also surfaces **provider / breaker health** via `/admin/providers/health`.

Design notes live under `docs/` locally (not committed). See `docs/ARCHITECTURE.md` and `docs/BUILD_PLAN.md`.

## Evaluate (reviewers / TAs)

One command runs health + smoke + load + routing + budget probe and prints a scorecard:

```bash
# Live Railway
python3 scripts/evaluate.py \
  --url https://gateway-production-e22b.up.railway.app \
  --admin-token "$ADMIN_TOKEN"

# Local mocks (+ optional failover)
python3 scripts/evaluate.py --url http://localhost:8080 \
  --admin-token dev-admin-change-me --check-failover
```

Details: [`EVAL.md`](EVAL.md). Artifacts land in `verification/evaluate_report.*`.

OpenCode users: see [`OPENCODE.md`](OPENCODE.md) / [`opencode.json`](opencode.json).

## Development

```bash
# unit tests
go test ./...

# routing eval (auto classifier vs length baseline)
go run ./cmd/routeval ./data/routing_eval.jsonl

# contract / load checks against a running gateway
python3 scripts/smoke_test.py --url http://localhost:8080 --key prism-sk-search-1a2b3c --model fast \
  --admin-token dev-admin-change-me --check-failover
python3 scripts/load_test.py  --url http://localhost:8080 --key prism-sk-free-7g8h9i \
  --model fast --requests 30 --concurrency 10 --rpm-limit 10 --max-tokens 64
```

Local planning notes (gitignored): `docs/BUILD_PLAN.md`, `docs/PROVIDERS.md`, `docs/CONFIGURATION.md`.

Verification artifacts and the assignment report: [`VERIFICATION.md`](VERIFICATION.md), [`DEMO.md`](DEMO.md), [`EVAL.md`](EVAL.md), [`verification/`](verification/).

## License

MIT
