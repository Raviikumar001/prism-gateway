# Prism Verification Report

Generated: 2026-08-07 (UTC window still 2026-08-06 at capture time)

Environment: Docker Compose, `PROVIDER_MODE=live` (Cerebras + OpenRouter), Postgres + Redis healthy.

## Summary

| Check | Result |
|---|---|
| Unit tests (`go test ./...`) | Pass |
| Pack validate (`scripts/validate_pack.py`) | Pass (prior run) |
| Smoke (`scripts/smoke_test.py`) | **25 passed, 1 soft WARN** |
| Load + RPM (`scripts/load_test.py`) | **10 accepted / 20 rate-limited**, no over-admission |
| Usage API reconciliation | Requests/tokens match load totals exactly |
| Routing eval (`cmd/routeval`) | Feature **20/20 (1.00)** vs length **8/20 (0.40)** |
| Coding-agent contract | Pass (models, tools, stream, max_tokens) |
| Time-sensitive cache bypass | Pass (`req_no_cache` style stays miss) |
| Rejection logging | Pass (`rejected_auth`, `rejected_malformed`) |

Artifacts under [`verification/`](verification/).

## Smoke

Command:

```bash
python3 scripts/smoke_test.py \
  --url http://localhost:8080 \
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

Full transcript: [`verification/smoke_output.txt`](verification/smoke_output.txt)

## Load test + usage reconciliation

Command:

```bash
python3 scripts/load_test.py \
  --url http://localhost:8080 \
  --key prism-sk-free-7g8h9i \
  --model fast --requests 30 --concurrency 10 --rpm-limit 10 --max-tokens 64
```

| Metric | Client (load) | Admin usage Δ | Match |
|---|---:|---:|---|
| Accepted requests | 10 | 10 | yes |
| Prompt tokens | 258 | 258 | yes |
| Completion tokens | 640 | 640 | yes |
| Cost USD | ~0.000024 | 0.00002409 | yes (header rounding) |
| Cache hits | — | 0 | expected (unique prompts) |

Latency (accepted): avg **2223 ms**, p95 **3390 ms** on live OpenRouter/Cerebras path.

RPM: free-tier limit 10; 20×`429`; no over-admission inside the window.

Raw: [`verification/load_output.txt`](verification/load_output.txt),
[`verification/usage_before.json`](verification/usage_before.json),
[`verification/usage_after.json`](verification/usage_after.json)

## Routing eval

```bash
go run ./cmd/routeval -json verification/routing_eval_results.json ./data/routing_eval.jsonl
```

```
feature_accuracy=1.00 (20/20)
length_accuracy=0.40 (8/20)
```

Feature-weighted `auto` classifier beats length-only baseline. Per-case JSON:
[`verification/routing_eval_results.json`](verification/routing_eval_results.json)

## Semantic cache notes

Process snapshot after smoke+coding checks:
[`verification/cache_stats.json`](verification/cache_stats.json)

- Exact + semantic hits observed during smoke
- Near-miss / soft miss on paraphrase pair B is threshold-dependent (logged as WARN, not FAIL)
- Tool calls, multi-turn, and time-sensitive prompts bypass cache by design

## Coding-agent compatibility

```bash
python3 scripts/coding_agent_test.py \
  --url http://localhost:8080 \
  --key prism-sk-research-4d5e6f \
  --model openai/gpt-4o-mini
```

All checks passed (model list/retrieve, tools, tool-result turn, streaming tool deltas, max_tokens).
Transcript: [`verification/coding_agent_output.txt`](verification/coding_agent_output.txt)

## Known limitations

1. **Explainer/demo video** is not in-repo — see [`DEMO.md`](DEMO.md) for the recording script.
2. **Semantic paraphrase** quality depends on embedding threshold; some near-paraphrases may miss.
3. **Usage `from`/`to`** aggregates whole UTC months overlapping the window (storage is monthly rollups), not day-level slices inside a month.
4. **Live latency** varies with upstream vendors; mock mode is much faster for local demos.
5. **Unauthenticated rejects** are logged under virtual key `_unauthenticated`.
6. Local design notes under `docs/` remain gitignored; product docs live in `README.md` + this report.

## Demo video

Record separately using [`DEMO.md`](DEMO.md). Link the finished video from the assignment submission (Drive/YouTube/Loom).
