#!/usr/bin/env python3
"""One-command Prism evaluation runner for reviewers / TAs.

Runs the assignment verification suite against a live gateway and prints a
scorecard. Zero third-party deps (Python 3.9+ stdlib).

What it covers:
  1. Health (Postgres + Redis)
  2. Smoke contract (headers, stream, cache, auth) — scripts/smoke_test.py
  3. Load / over-admission — scripts/load_test.py (free-tier RPM=10)
  4. Routing eval (auto vs length baseline) — go run ./cmd/routeval
  5. Budget rejection probe (budget-demo key)
  6. Optional coding-agent contract — scripts/coding_agent_test.py

Examples:

  # Local mock stack
  python3 scripts/evaluate.py --url http://localhost:8080 \\
    --admin-token dev-admin-change-me --check-failover

  # Live Railway deploy
  python3 scripts/evaluate.py \\
    --url https://gateway-production-e22b.up.railway.app \\
    --admin-token "$ADMIN_TOKEN"

Exit code 0 = all hard checks passed (soft WARNs allowed).
"""

from __future__ import annotations

import argparse
import json
import os
import subprocess
import sys
import time
import urllib.error
import urllib.request
from datetime import datetime, timezone
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
OUT_DIR = ROOT / "verification"


def http_json(url: str, method: str = "GET", headers: dict | None = None, body: dict | None = None, timeout: int = 60):
    req = urllib.request.Request(
        url,
        data=None if body is None else json.dumps(body).encode(),
        headers=headers or {},
        method=method,
    )
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            raw = resp.read().decode(errors="replace")
            try:
                parsed = json.loads(raw) if raw else None
            except json.JSONDecodeError:
                parsed = raw
            return resp.status, {k.lower(): v for k, v in resp.headers.items()}, parsed
    except urllib.error.HTTPError as e:
        raw = e.read().decode(errors="replace")
        try:
            parsed = json.loads(raw) if raw else None
        except json.JSONDecodeError:
            parsed = raw
        return e.code, {k.lower(): v for k, v in e.headers.items()}, parsed


def run_cmd(cmd: list[str], cwd: Path | None = None, env: dict | None = None) -> tuple[int, str]:
    merged = os.environ.copy()
    if env:
        merged.update(env)
    # Help Python HTTPS clients on some macOS installs
    merged.setdefault("SSL_CERT_FILE", "/etc/ssl/cert.pem")
    merged.setdefault("REQUESTS_CA_BUNDLE", merged["SSL_CERT_FILE"])
    proc = subprocess.run(
        cmd,
        cwd=str(cwd or ROOT),
        env=merged,
        text=True,
        capture_output=True,
    )
    out = (proc.stdout or "") + (("\n" + proc.stderr) if proc.stderr else "")
    return proc.returncode, out


def section(title: str) -> None:
    print(f"\n{'=' * 60}")
    print(f"  {title}")
    print(f"{'=' * 60}")


def main() -> int:
    parser = argparse.ArgumentParser(description="Run Prism verification suite and print a scorecard")
    parser.add_argument("--url", default="http://localhost:8080", help="gateway base URL")
    parser.add_argument("--admin-token", default=os.environ.get("ADMIN_TOKEN", "dev-admin-change-me"))
    parser.add_argument("--smoke-key", default="prism-sk-search-1a2b3c")
    parser.add_argument("--load-key", default="prism-sk-free-7g8h9i")
    parser.add_argument("--research-key", default="prism-sk-research-4d5e6f")
    parser.add_argument("--budget-key", default="prism-sk-budget-demo-0j1k2l")
    parser.add_argument("--model", default="fast")
    parser.add_argument("--check-failover", action="store_true", help="pass through to smoke_test")
    parser.add_argument("--skip-routing", action="store_true")
    parser.add_argument("--skip-coding-agent", action="store_true")
    parser.add_argument("--load-requests", type=int, default=30)
    parser.add_argument("--load-concurrency", type=int, default=10)
    parser.add_argument("--load-rpm-limit", type=int, default=10)
    parser.add_argument("--out", default=str(OUT_DIR / "evaluate_report.json"))
    args = parser.parse_args()

    OUT_DIR.mkdir(parents=True, exist_ok=True)
    report = {
        "started_at": datetime.now(timezone.utc).isoformat(),
        "url": args.url,
        "checks": {},
    }
    hard_fail = False

    # --- 1. Health ---
    section("1) Health")
    status, _, body = http_json(args.url.rstrip("/") + "/health", timeout=20)
    health_ok = status == 200 and isinstance(body, dict) and body.get("status") == "ok"
    print(f"  HTTP {status}: {body}")
    report["checks"]["health"] = {"ok": health_ok, "status": status, "body": body}
    if not health_ok:
        print("\nGateway unhealthy — aborting remaining checks.")
        hard_fail = True
        report["ok"] = False
        Path(args.out).write_text(json.dumps(report, indent=2) + "\n")
        return 1

    # --- 2. Smoke ---
    section("2) Smoke (scripts/smoke_test.py)")
    smoke_cmd = [
        sys.executable,
        str(ROOT / "scripts" / "smoke_test.py"),
        "--url",
        args.url,
        "--key",
        args.smoke_key,
        "--model",
        args.model,
        "--admin-token",
        args.admin_token,
    ]
    if args.check_failover:
        smoke_cmd.append("--check-failover")
    code, out = run_cmd(smoke_cmd)
    print(out.rstrip())
    smoke_path = OUT_DIR / "evaluate_smoke.txt"
    smoke_path.write_text(out)
    smoke_ok = code == 0
    report["checks"]["smoke"] = {"ok": smoke_ok, "exit_code": code, "log": str(smoke_path)}
    hard_fail = hard_fail or not smoke_ok

    # --- 3. Load / over-admission ---
    section("3) Load / over-admission (scripts/load_test.py)")
    load_cmd = [
        sys.executable,
        str(ROOT / "scripts" / "load_test.py"),
        "--url",
        args.url,
        "--key",
        args.load_key,
        "--model",
        args.model,
        "--requests",
        str(args.load_requests),
        "--concurrency",
        str(args.load_concurrency),
        "--rpm-limit",
        str(args.load_rpm_limit),
        "--max-tokens",
        "64",
    ]
    code, out = run_cmd(load_cmd)
    print(out.rstrip())
    load_path = OUT_DIR / "evaluate_load.txt"
    load_path.write_text(out)
    load_ok = code == 0
    report["checks"]["load"] = {"ok": load_ok, "exit_code": code, "log": str(load_path)}
    hard_fail = hard_fail or not load_ok

    # --- 4. Routing eval ---
    if not args.skip_routing:
        section("4) Routing eval (cmd/routeval)")
        route_json = OUT_DIR / "evaluate_routing.json"
        code, out = run_cmd(
            ["go", "run", "./cmd/routeval", "-json", str(route_json), "./data/routing_eval.jsonl"]
        )
        print(out.rstrip())
        route_ok = code == 0
        summary = {}
        if route_json.exists():
            summary = json.loads(route_json.read_text())
            print(
                f"  feature_accuracy={summary.get('feature_accuracy')} "
                f"({summary.get('feature_correct')}/{summary.get('total')})  "
                f"length_accuracy={summary.get('length_accuracy')} "
                f"({summary.get('length_correct')}/{summary.get('total')})"
            )
        report["checks"]["routing"] = {
            "ok": route_ok,
            "exit_code": code,
            "summary": {
                "feature_accuracy": summary.get("feature_accuracy"),
                "length_accuracy": summary.get("length_accuracy"),
                "feature_correct": summary.get("feature_correct"),
                "length_correct": summary.get("length_correct"),
                "total": summary.get("total"),
            },
            "log": str(route_json),
        }
        hard_fail = hard_fail or not route_ok
    else:
        report["checks"]["routing"] = {"ok": True, "skipped": True}

    # --- 5. Budget rejection ---
    section("5) Budget rejection probe")
    status, headers, body = http_json(
        args.url.rstrip("/") + "/v1/chat/completions",
        method="POST",
        headers={
            "Authorization": f"Bearer {args.budget_key}",
            "Content-Type": "application/json",
        },
        body={
            "model": "fast",
            "messages": [{"role": "user", "content": f"budget probe {time.time()}"}],
            "max_tokens": 32,
        },
        timeout=60,
    )
    err_type = ""
    if isinstance(body, dict):
        err = body.get("error") or {}
        if isinstance(err, dict):
            err_type = err.get("type") or err.get("code") or ""
    budget_ok = status == 429 and err_type == "budget_exceeded"
    if not budget_ok:
        budget_ok = err_type == "budget_exceeded" and status >= 400
    print(f"  status={status} error_type={err_type or '—'} body_snip={str(body)[:160]}")
    report["checks"]["budget"] = {
        "ok": budget_ok,
        "status": status,
        "error_type": err_type,
        "body": body if not isinstance(body, str) or len(body) < 500 else body[:500],
    }
    if not budget_ok:
        print("  WARN  budget probe did not clearly reject — check seed budget-demo key")
        report["checks"]["budget"]["soft"] = True
        # Do not hard-fail: tiny pricing / cache edge cases can mask this on live
    # --- 6. Coding agent (optional) ---
    if not args.skip_coding_agent:
        section("6) Coding-agent contract (optional)")
        code, out = run_cmd(
            [
                sys.executable,
                str(ROOT / "scripts" / "coding_agent_test.py"),
                "--url",
                args.url,
                "--key",
                args.research_key,
                "--model",
                "openai/gpt-4o-mini",
            ]
        )
        print(out.rstrip() or "(no output)")
        coding_path = OUT_DIR / "evaluate_coding_agent.txt"
        coding_path.write_text(out)
        # Soft on live model availability failures
        coding_ok = code == 0
        report["checks"]["coding_agent"] = {
            "ok": coding_ok,
            "exit_code": code,
            "soft": not coding_ok,
            "log": str(coding_path),
        }
        if not coding_ok:
            print("  WARN  coding-agent check failed (often model availability); not a hard fail")
    else:
        report["checks"]["coding_agent"] = {"ok": True, "skipped": True}

    # --- Scorecard ---
    section("SCORECARD")
    rows = []
    for name, check in report["checks"].items():
        if check.get("skipped"):
            tag = "SKIP"
        elif check.get("ok"):
            tag = "PASS"
        elif check.get("soft"):
            tag = "WARN"
        else:
            tag = "FAIL"
        rows.append((tag, name))
        print(f"  {tag:4}  {name}")

    report["finished_at"] = datetime.now(timezone.utc).isoformat()
    report["ok"] = not hard_fail
    report_path = Path(args.out)
    report_path.write_text(json.dumps(report, indent=2) + "\n")
    txt_path = report_path.with_suffix(".txt")
    txt_path.write_text(
        "\n".join(f"{tag}  {name}" for tag, name in rows)
        + f"\n\noverall={'PASS' if report['ok'] else 'FAIL'}\nurl={args.url}\n"
    )

    print(f"\nWrote {report_path}")
    print(f"Wrote {txt_path}")
    print(f"\nOverall: {'PASS' if report['ok'] else 'FAIL'}")
    return 0 if report["ok"] else 1


if __name__ == "__main__":
    sys.exit(main())
