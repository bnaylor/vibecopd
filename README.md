# vibecop

**A second AI watches your coding agent so you don't have to.**

You've turned on permission checks in Claude Code or Gemini CLI. You've been approving `bash`, `read_file`, and `write_file` for the last two hours without reading them. You are the rubber stamp. You are the weak link.

`vibecop` fixes this. It's a small Go daemon that sits between your coding agent and its permission prompts. When the agent wants to run something, `vibecop` asks a separate, independent AI — one that has never seen your conversation and can't be manipulated by whatever's in your context window — whether it looks okay. Routine stuff passes silently. Weird stuff gets escalated to you with context. You only see the interesting ones.

No Electron. No GUI. Runs in the background. Optional TUI if you want to watch the decisions fly by.

---

## How it works

```
coding agent  →  PreToolUse hook  →  vibecop daemon  →  LLM
                                           ↓
                                    approve: silent ✓
                                    escalate: your terminal gets a prompt with context
                                    deny: same, but louder
```

The hook is a one-liner installed into your harness config. The daemon runs in the background. The LLM can be a local Ollama model (near-zero cost, ~13s latency on M4 Pro) or a cloud endpoint like Haiku (~1.7s). If the daemon crashes or times out, the hook fails open — your agent is never blocked by `vibecop`'s own problems.

---

## Modes

**Baseline** (default) — No setup required. Catches obviously bad patterns: recursive deletes outside the project, writing to `~/.ssh`, shell startup file modifications, suspicious outbound network calls. Everything else passes.

**Guardian** — Run `vibecop init` once per project. A coding agent analyzes your codebase and generates a project-specific system prompt. Now `vibecop` knows that `swift build` is routine here but `curl http://...` probably isn't. Lower escalation rates, more trustworthy verdicts.

---

## Supported harnesses

- Claude Code
- Gemini CLI
- Deepseek (via Ollama or direct API)

---

## Recommended models

Speed matters — `vibecop` is in your agent's critical path. Measured on M4 Pro 48GB:

**Cloud (recommended):**
| Model | Latency | Notes |
|-------|---------|-------|
| `claude-haiku-4-5` | ~1.7s | **Recommended default.** Fast enough to beat you to the prompt. Anthropic API. |
| `gemini-2.5-flash` | TBD | Natural choice if you're running Gemini CLI. Google AI API. |

**Local (Ollama) — slower than cloud on typical hardware:**
| RAM | Model | Latency | Notes |
|-----|-------|---------|-------|
| 16 GB | `mistral-nemo:latest` | — | Untested |
| 32 GB | `qwen3:14b` | ~12.9s | CoT disabled automatically. Noticeably slow. |
| 64 GB+ | `qwen3:32b` or `gemma3:27b` | — | Untested |

---

## Quickstart

**First run —** just run any command. There's nothing to install first:

```sh
vibecop status       # no config? vibecop runs setup automatically
```

The setup wizard walks you through provider, model, timeout, and API key.
After that it offers to install hooks and test the connection. All in one
session, zero docs required.

If you prefer to do it step by step:

```sh
vibecop setup              # interactive wizard
vibecop test               # verify the LLM endpoint works
vibecop start              # boot the daemon (foreground; Ctrl+C to stop)
vibecop tui                # (another terminal) watch verdicts in real-time
```

Then for full integration with coding agents:

```sh
vibecop install --all      # wire hooks into Claude Code and Gemini CLI
vibecop init --harness claude   # generate Guardian prompt for this project
```

By default the installed hook calls `vibecop hook` and resolves the binary
through `$PATH`. Pass `--vibecop-path` to either `vibecop setup` or
`vibecop install` to point the hook at a specific binary instead — handy when
testing a local build without overwriting the system install:

```sh
go build -o ./vibecop ./cmd/vibecop
vibecop install --all --vibecop-path ./vibecop
```

The path is resolved to absolute, so the hook works regardless of the agent's
working directory. Re-running with a different `--vibecop-path` updates the
existing entry in place rather than appending a duplicate.

## Config

`~/.vibecop/config.toml` — created by `vibecop setup`, or write it yourself:

**Haiku (recommended):**
```toml
[daemon]
enabled         = true
timeout_ms      = 5000
activity_window = 10
audit_enabled   = false

[model]
endpoint   = "https://api.anthropic.com/v1/messages"
api_format = "anthropic"
model      = "claude-haiku-4-5"
api_key    = "sk-ant-..."
```

**Gemini Flash (for Gemini CLI users):**
```toml
[model]
endpoint   = "https://generativelanguage.googleapis.com/v1beta/openai/chat/completions"
api_format = "openai"
model      = "gemini-2.5-flash"
api_key    = "AIza..."
```

**Local Ollama (no API key needed):**
```toml
[model]
endpoint   = "http://localhost:11434/v1/chat/completions"
api_format = "openai"
model      = "qwen3:14b"
api_key    = ""
```

`vibecop setup` auto-detects Ollama models if Ollama is running locally.

---

## TUI

```
vibecop tui
```

Attaches to a running daemon and shows:
- Live verdict feed (Approved / Escalated / Denied) with tool name and reason
- Rolling average latency (last 50 requests)
- Daemon status and active mode (Baseline / Guardian)

---

## Commands

```
vibecop setup              Interactive first-time setup wizard
vibecop start              Start the background daemon
vibecop stop               Stop it
vibecop status             Show daemon status and config
vibecop tui                Attach the live TUI
vibecop init               Generate Guardian prompt for current project
vibecop install            Install hooks into harness config
vibecop uninstall          Remove hooks
vibecop test               Test connection to the configured LLM endpoint
vibecop refine             Regenerate the Guardian prompt (uses recent activity as context)
```

---

## Background

This project grew out of experimentation with the AI second-opinion concept — the idea that a second, context-free model evaluating tool-use requests is more trustworthy than a human who's been clicking Approve for two hours. `vibecop` is the standalone, no-GUI version: a single static binary that runs anywhere Go runs, with no Electron and no dependencies on any particular coding harness's UI.

---

## Status

Implementation complete. See [`docs/spec.md`](docs/spec.md) for the full design specification.

See [`docs/spec.md`](docs/spec.md) for the full design specification.
