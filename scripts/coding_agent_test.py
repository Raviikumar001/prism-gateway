#!/usr/bin/env python3
"""Live OpenAI Chat Completions compatibility check for coding-agent clients."""

import argparse
import json
import urllib.parse
import urllib.request


def call(base, key, method, path, body=None):
    request = urllib.request.Request(
        base.rstrip("/") + "/v1" + path,
        data=None if body is None else json.dumps(body).encode(),
        headers={
            "Authorization": f"Bearer {key}",
            "Content-Type": "application/json",
        },
        method=method,
    )
    with urllib.request.urlopen(request, timeout=90) as response:
        return (
            response.status,
            {k.lower(): v for k, v in response.headers.items()},
            response.read().decode(),
        )


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--url", default="http://localhost:8080")
    parser.add_argument("--key", required=True)
    parser.add_argument("--model", default="openai/gpt-4o-mini")
    args = parser.parse_args()

    status, _, raw = call(args.url, args.key, "GET", "/models")
    model_ids = {item["id"] for item in json.loads(raw)["data"]}
    assert status == 200 and args.model in model_ids
    print(f"PASS model discovery ({len(model_ids)} visible models)")

    encoded_model = urllib.parse.quote(args.model, safe="")
    status, _, raw = call(args.url, args.key, "GET", f"/models/{encoded_model}")
    assert status == 200 and json.loads(raw)["id"] == args.model
    print("PASS model retrieve")

    tool = {
        "type": "function",
        "function": {
            "name": "read_file",
            "description": "Read a repository file",
            "parameters": {
                "type": "object",
                "properties": {"path": {"type": "string"}},
                "required": ["path"],
            },
        },
    }
    first = {
        "model": args.model,
        "messages": [{"role": "user", "content": "Read main.go using the tool."}],
        "tools": [tool],
        "tool_choice": {"type": "function", "function": {"name": "read_file"}},
        "temperature": 0,
        "max_tokens": 128,
    }
    status, headers, raw = call(args.url, args.key, "POST", "/chat/completions", first)
    message = json.loads(raw)["choices"][0]["message"]
    tool_calls = message.get("tool_calls") or []
    assert status == 200 and tool_calls
    assert tool_calls[0]["function"]["name"] == "read_file"
    assert headers.get("x-prism-provider")
    print("PASS non-stream tool call")

    followup = {
        "model": args.model,
        "messages": [
            first["messages"][0],
            message,
            {
                "role": "tool",
                "tool_call_id": tool_calls[0]["id"],
                "content": "package main\nfunc main() {}",
            },
        ],
        "tools": [tool],
        "tool_choice": "none",
        "temperature": 0,
        "max_tokens": 128,
    }
    status, _, raw = call(args.url, args.key, "POST", "/chat/completions", followup)
    answer = json.loads(raw)["choices"][0]["message"].get("content")
    assert status == 200 and answer
    print("PASS tool-result continuation")

    streaming = dict(first)
    streaming["stream"] = True
    status, headers, raw = call(args.url, args.key, "POST", "/chat/completions", streaming)
    assert status == 200 and '"tool_calls"' in raw and "data: [DONE]" in raw
    assert headers.get("x-prism-cost-usd")
    print("PASS streaming tool deltas and [DONE]")

    limited = {
        "model": args.model,
        "messages": [{"role": "user", "content": "Write ten words."}],
        "max_tokens": 1,
    }
    status, _, raw = call(args.url, args.key, "POST", "/chat/completions", limited)
    usage = json.loads(raw).get("usage") or {}
    assert status == 200 and usage.get("completion_tokens", 2) <= 1
    print("PASS max_tokens passthrough")


if __name__ == "__main__":
    main()
