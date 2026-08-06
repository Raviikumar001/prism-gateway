# Prism

OpenAI-compatible LLM gateway with tenant isolation, budget controls, failover, streaming, and a semantic response cache.

Applications call one endpoint with a virtual key. Prism authenticates the key, enforces allowlists and rate limits, routes to the right model tier, serves cache hits when prompts match by meaning, and meters every token.

```
Client ──► Prism ──► Cerebras / OpenRouter / mock providers
              │
         Postgres + Redis
```

## Features

- **OpenAI Chat Completions API** — `POST /v1/chat/completions`, streaming and non-streaming
- **Virtual keys** — per-tenant allowlists, RPM limits, monthly spend caps with **pre-dispatch budget reservation**
- **Model aliases** — `fast`, `smart`, and `auto` (feature-weighted difficulty routing)
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
Ops console: `http://localhost:8080/console/` (enter `ADMIN_TOKEN`)

```bash
curl -s http://localhost:8080/health
```
### Mock providers (local failover drills)

```bash
python3 scripts/mock_provider.py --port 9001 --name alpha
python3 scripts/mock_provider.py --port 9002 --name beta
```

Take a provider down without restarting:

```bash
curl -X POST http://localhost:9001/admin/config -d '{"mode":"down"}'
```

## API

### Data plane

| Method | Path | Auth |
|---|---|---|
| `POST` | `/v1/chat/completions` | `Authorization: Bearer <virtual-key>` |
| `GET` | `/health` | none |

### Admin

| Method | Path | Auth |
|---|---|---|
| `GET` | `/admin/usage?key=&from=&to=` | admin token |
| `GET` | `/admin/logs?key=&limit=` | admin token |
| `GET` | `/admin/cache/stats` | admin token |
| `GET` | `/admin/providers/health` | admin token |
| `GET` | `/console/` | HTML UI (APIs still need admin token) |

Admin auth: `Authorization: Bearer <ADMIN_TOKEN>`.

Errors use an OpenAI-style body with a distinct `error.type` (`authentication_error`, `model_not_allowed`, `rate_limit_exceeded`, `budget_exceeded`, `not_found_error`, `upstream_error`).

## Configuration

Seed data lives under `data/`:

- `seed_keys.json` — tenants, budgets, RPM, allowlists, cache settings
- `model_pricing.json` — USD per 1M input/output tokens
- `gateway_config.sample.json` — providers, aliases, retry policy

Environment (see `.env.example`):

| Variable | Description |
|---|---|
| `PORT` | Listen port (Railway injects this) |
| `DATABASE_URL` | Postgres connection string |
| `REDIS_URL` | Redis connection string |
| `ADMIN_TOKEN` | Admin / console auth |
| `PROVIDER_MODE` | `mocks` or `live` |
| `CEREBRAS_API_KEY` | Live Cerebras traffic |
| `OPENROUTER_API_KEY` | Live OpenRouter traffic + embeddings |

## Deploy on Railway

Prism is designed for Railway: one Go service plus managed Postgres and Redis.

1. Create a Railway project; add **PostgreSQL** and **Redis**.
2. Deploy this repo (Dockerfile at repo root).
3. Set service variables:

```
DATABASE_URL=${{Postgres.DATABASE_URL}}
REDIS_URL=${{Redis.REDIS_URL}}
ADMIN_TOKEN=<secret>
PROVIDER_MODE=live
CEREBRAS_API_KEY=...
OPENROUTER_API_KEY=...
```

4. Health check path: `/health`
5. Generate a public domain on the gateway service

The process listens on `0.0.0.0:$PORT`. Migrations run on startup.

Details: [docs/DEPLOYMENT.md](docs/DEPLOYMENT.md)

## Architecture

Request path:

1. Authenticate virtual key  
2. Allowlist → RPM → **budget reserve**  
3. Resolve alias (`auto` uses feature-weighted difficulty)  
4. Cache (exact hash, then semantic)  
5. Upstream with timeout / retry+jitter / **circuit breaker** / cross-vendor failover  
6. Stream or return; meter from provider `usage`; **settle reservation**  
7. Enqueue request log + update usage  

Ops console also surfaces **provider / breaker health** via `/admin/providers/health`.

Design notes live under `docs/` locally (not committed). See `docs/ARCHITECTURE.md` and `docs/BUILD_PLAN.md`.

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
  --model fast --requests 30 --concurrency 10 --rpm-limit 10
```

Local planning notes (gitignored): `docs/BUILD_PLAN.md`, `docs/PROVIDERS.md`, `docs/CONFIGURATION.md`.

## License

MIT
