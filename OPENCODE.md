# Using Prism with OpenCode

OpenCode is configured with a **variety of OpenRouter + Cerebras models** through Prism.

## Default

**`prism/openai/gpt-5.6-luna`** — good all-round coding/chat model (OpenAI Luna via OpenRouter).

## Models in the picker

| OpenCode id | Notes |
|---|---|
| `openai/gpt-5.6-luna` | Default — strong & cheap |
| `openai/gpt-5.6-luna-pro` | Stronger Luna |
| `openai/gpt-5.6-sol` / `sol` | GPT-5.6 Sol flagship |
| `anthropic/claude-fable-5` / `fable` | Claude Fable 5 |
| `openai/gpt-5.4` / `gpt-5.4-mini` | OpenAI 5.4 family |
| `openai/gpt-4o` / `gpt-4o-mini` | Classic OpenAI |
| `anthropic/claude-sonnet-5` / `claude-haiku-4.5` | Anthropic |
| `google/gemini-3.6-flash` | Gemini |
| `qwen/qwen3-coder` / `qwen3-coder-plus` | Coding specialists |
| `mistralai/codestral-2508` | Codestral |
| `deepseek/deepseek-v4-flash` | DeepSeek |
| `zai-glm-4.7` | Cerebras GLM 4.7 |
| `z-ai/glm-5.3-flash` / `glm` | GLM 5.3 Flash |
| `fast` / `smart` / `code` / `sol` / `fable` / `glm` / `auto` | Prism aliases |

## Launch

```bash
cd /Volumes/new/web/assignment/prism-gateway
opencode
```

Or:

```bash
opencode run --model prism/openai/gpt-5.6-luna "Summarize this repo in 5 bullets"
opencode run --model prism/code "Review scripts/evaluate.py"
```

Config: [`opencode.json`](opencode.json) and `~/.config/opencode/opencode.json`.
