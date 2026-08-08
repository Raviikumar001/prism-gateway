# How to evaluate Prism

Use this if you are a reviewer / TA / teammate verifying the project.

## Fastest path (live deploy)

```bash
export ADMIN_TOKEN=dev-admin-change-me   # or the Railway ADMIN_TOKEN
python3 scripts/evaluate.py \
  --url https://gateway-production-e22b.up.railway.app \
  --admin-token "$ADMIN_TOKEN"
```

This prints a scorecard and writes:

- `verification/evaluate_report.json`
- `verification/evaluate_report.txt`
- per-step logs under `verification/evaluate_*.txt`

## Local mock path (failover included)

```bash
docker compose up --build -d
python3 scripts/evaluate.py \
  --url http://localhost:8080 \
  --admin-token dev-admin-change-me \
  --check-failover
```

## What the scorecard checks

| Check | Script / probe | Pass means |
|---|---|---|
| Health | `GET /health` | Postgres + Redis up |
| Smoke | `scripts/smoke_test.py` | headers, stream, cache, auth, admin |
| Load | `scripts/load_test.py` | free-tier RPM; **no over-admission** |
| Routing | `go run ./cmd/routeval` | feature router beats length baseline |
| Budget | budget-demo key | request rejected for budget |
| Coding agent | `scripts/coding_agent_test.py` | tools/stream/models (soft on live) |

## Manual “user access” smoke

Users access Prism like OpenAI:

```bash
curl -sD - https://gateway-production-e22b.up.railway.app/v1/chat/completions \
  -H "Authorization: Bearer prism-sk-research-4d5e6f" \
  -H "Content-Type: application/json" \
  -d '{"model":"auto","messages":[{"role":"user","content":"Hello from Prism"}],"max_tokens":32}'
```

Ops console: https://gateway-production-e22b.up.railway.app/console/  
(admin token + virtual key → Load)

## Individual scripts (same as assignment)

```bash
python3 scripts/smoke_test.py --url ... --key prism-sk-search-1a2b3c --model fast --admin-token ...
python3 scripts/load_test.py  --url ... --key prism-sk-free-7g8h9i --model fast --requests 30 --concurrency 10 --rpm-limit 10
go run ./cmd/routeval ./data/routing_eval.jsonl
```

See also [`VERIFICATION.md`](VERIFICATION.md) and [`DEMO.md`](DEMO.md).
