# Prism Demo / Explainer Video Script

Assignment requires a short explainer/demo video. Record this locally (Loom/OBS, ~5–8 minutes).

## Setup (before recording)

```bash
docker compose up --build -d
# PROVIDER_MODE=live for real providers, or mocks for a deterministic demo
export ADMIN_TOKEN=dev-admin-change-me   # match .env
```

Open:

- Chat: terminal + `curl` / OpenCode pointing at `http://localhost:8080/v1`
- Ops console: `http://localhost:8080/console/`

## Suggested outline

1. **What Prism is** (30s) — OpenAI-compatible LLM gateway with virtual keys, routing, cache, budgets, failover.
2. **Happy path** (60s) — `POST /v1/chat/completions` with `fast`; show `x-prism-*` headers and usage.
3. **Semantic cache** (60s) — identical prompt → hit; paraphrase → hit; “current status…” → miss.
4. **Routing** (45s) — `auto` on a short vs complex prompt; show `route_reason` in `/admin/logs`.
5. **Quotas** (60s) — free-tier RPM burst (`load_test.py`); budget-demo key → `budget_exceeded`.
6. **Failover** (45s) — mock alpha `down` → `x-prism-fallback: true` (mock mode).
7. **Coding agent** (45s) — tools / streaming via `coding_agent_test.py` or OpenCode against Prism.
8. **Ops console** (30s) — providers health, usage `from`/`to`, recent logs with retries.

## Commands to paste on screen

```bash
curl -sD - http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer prism-sk-research-4d5e6f" \
  -H "Content-Type: application/json" \
  -d '{"model":"auto","messages":[{"role":"user","content":"Explain CAP theorem briefly"}],"max_tokens":64}'

python3 scripts/smoke_test.py --url http://localhost:8080 \
  --key prism-sk-search-1a2b3c --model fast --admin-token "$ADMIN_TOKEN"

python3 scripts/load_test.py --url http://localhost:8080 \
  --key prism-sk-free-7g8h9i --model fast --requests 30 --concurrency 10 --rpm-limit 10 --max-tokens 64
```

After recording, put the public link in your submission notes and optionally append it to `VERIFICATION.md`.
