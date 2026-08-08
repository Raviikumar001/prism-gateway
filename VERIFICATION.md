# Prism Verification Report

Generated: 2026-08-07

## Production deploy (Railway)

Public URL: **https://gateway-production-e22b.up.railway.app**

| Piece | What we use |
|---|---|
| Gateway | This repo (`Dockerfile`), service `gateway`, `PROVIDER_MODE=live` |
| Postgres | Custom image **`pgvector/pgvector:pg17`** (not plain Railway Postgres) + volume at `/var/lib/postgresql/data` — required for semantic cache `vector` / HNSW |
| Redis | Railway managed **Redis** plugin — RPM/TPM windows + budget reservations |

Health: `{"status":"ok","checks":{"postgres":"up","redis":"up"},"provider_mode":"live"}`

Railway smoke/load/coding-agent: [`verification/railway_smoke_output.txt`](verification/railway_smoke_output.txt), [`verification/railway_load_output.txt`](verification/railway_load_output.txt), [`verification/railway_coding_agent_output.txt`](verification/railway_coding_agent_output.txt)

| Check (Railway) | Result |
|---|---|
| Smoke | **25 passed, 1 soft WARN** |
| Coding-agent | Pass |
| Load (free-tier RPM 10) | **10 accepted / 10 rate-limited**, no over-admission |

Ops console: https://gateway-production-e22b.up.railway.app/console/

## Summary (local + Railway)

| Check | Result |
|---|---|
| Unit tests (`go test ./...`) | Pass |
| Pack validate (`scripts/validate_pack.py`) | Pass |
| Smoke (`scripts/smoke_test.py`) | **25 passed, 1 soft WARN** |
| Load + RPM (`scripts/load_test.py`) | Pass (no over-admission) |
| Usage API reconciliation | Requests/tokens match load totals exactly |
| Routing eval (`cmd/routeval`) | Feature **20/20 (1.00)** vs length **8/20 (0.40)** |
| Coding-agent contract | Pass |
| Time-sensitive cache bypass | Pass |
| Rejection logging | Pass |
| Railway deploy | Pass |

Artifacts under [`verification/`](verification/).

## Smoke

Command:

```bash
python3 scripts/smoke_test.py \
  --url https://gateway-production-e22b.up.railway.app \
  --key prism-sk-search-1a2b3c \
  --model fast \
  --admin-token "$ADMIN_TOKEN"
```

Notable outcomes:

- Non-stream + stream header contract OK (`x-prism-provider`, `x-prism-cache`, `x-prism-cost-usd`)
- Exact repeat → cache **hit**; paraphrase pair A hit / pair B miss (WARN); unrelated miss
- Time-sensitive “current status…” repeat → cache **miss**
- Admin `usage?from=&to=` returns window bounds; logs include `route_reason` + `retries`
- Invalid auth + empty `messages` persist as `rejected_auth` / `rejected_malformed`

Full transcript: [`verification/smoke_output.txt`](verification/smoke_output.txt) / [`verification/railway_smoke_output.txt`](verification/railway_smoke_output.txt)

## Load test + usage reconciliation

See [`verification/load_output.txt`](verification/load_output.txt) and Railway [`verification/railway_load_output.txt`](verification/railway_load_output.txt).

## Routing eval

```
feature_accuracy=1.00 (20/20)
length_accuracy=0.40 (8/20)
```

Per-case JSON: [`verification/routing_eval_results.json`](verification/routing_eval_results.json)

## Known limitations

1. **Explainer/demo video** is not in-repo — see [`DEMO.md`](DEMO.md).
2. Semantic paraphrase quality is threshold-dependent (pair B soft WARN).
3. Usage `from`/`to` aggregates overlapping UTC months (monthly rollups).
4. Seed virtual keys are public demo credentials — rotate for long-lived public use.
5. Local design notes under `docs/` remain gitignored.

## Demo video

Record separately using [`DEMO.md`](DEMO.md).
