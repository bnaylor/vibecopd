# vibecopd

**A second AI watches your coding agent so you don't have to.**

You've turned on permission checks in Claude Code or Gemini CLI. You've been approving `bash`, `read_file`, and `write_file` for the last two hours without reading them. You are the rubber stamp. You are the weak link.

`vibecopd` fixes this. It's a small Go daemon that sits between your coding agent and its permission prompts. When the agent wants to run something, `vibecopd` asks a separate, independent AI — one that has never seen your conversation and can't be manipulated by whatever's in your context window — whether it looks okay. Routine stuff passes silently. Weird stuff gets escalated to you with context. You only see the interesting ones.

No Electron. No GUI. Runs in the background. Optional TUI if you want to watch the decisions fly by.

---

## How it works

```
coding agent  →  PreToolUse hook  →  vibecopd daemon  →  LLM
                                           ↓
                                    approve: silent ✓
                                    escalate: your terminal gets a prompt with context
                                    deny: same, but louder
```

The hook is a one-liner installed into your harness config. The daemon runs in the background. The LLM can be a local Ollama model (near-zero cost, ~300ms latency) or a cloud endpoint. If the daemon crashes or times out, the hook fails open — your agent is never blocked by `vibecopd`'s own problems.

---

## Modes

**Baseline** (default) — No setup required. Catches obviously bad patterns: recursive deletes outside the project, writing to `~/.ssh`, shell startup file modifications, suspicious outbound network calls. Everything else passes.

**Guardian** — Run `vibecopd init` once per project. A coding agent analyzes your codebase and generates a project-specific system prompt. Now `vibecopd` knows that `swift build` is routine here but `curl http://...` probably isn't. Lower escalation rates, more trustworthy verdicts.

---

## Supported harnesses

- Claude Code
- Gemini CLI
- Deepseek (via Ollama or direct API)

---

## Recommended models

Speed matters — `vibecopd` is in your agent's critical path.

**Local (Ollama):**

| RAM | Model | Notes |
|-----|-------|-------|
| 16 GB | `mistral-nemo:latest` | Fast, solid |
| 32 GB | `qwen3:14b` | Recommended default. CoT disabled automatically. |
| 64 GB+ | `qwen3:32b` or `gemma3:27b` | Approaches cloud quality |

**Cloud:**
- `claude-haiku-4-5` (Anthropic API) — fast, cheap, understands developer workflows
- `gemini-2.5-flash` (Gemini OpenAI-compatible endpoint) — also excellent

---

## Quickstart

```sh
# Install
go install github.com/bnaylor/vibecopd@latest

# Configure (~/.vibecopd/config.toml)
vibecopd init --harness claude   # generate Guardian prompt for current project

# Install hooks into your coding harness
vibecopd install --harness claude

# Start the daemon
vibecopd start

# Watch what's happening (optional)
vibecopd tui
```

---

## Config

`~/.vibecopd/config.toml`:

```toml
[daemon]
enabled         = true
timeout_ms      = 5000
activity_window = 10
audit_enabled   = false

[model]
endpoint   = "http://localhost:11434/v1/chat/completions"
api_format = "openai"    # or "anthropic"
model      = "qwen3:14b"
api_key    = ""
```

Test your connection: `vibecopd test`

---

## TUI

```
vibecopd tui
```

Attaches to a running daemon and shows:
- Live verdict feed (Approved / Escalated / Denied) with tool name and reason
- Rolling average latency (last 50 requests)
- Daemon status and active mode (Baseline / Guardian)

---

## Commands

```
vibecopd start              Start the background daemon
vibecopd stop               Stop it
vibecopd status             Show daemon status and config
vibecopd tui                Attach the live TUI
vibecopd init               Generate Guardian prompt for current project
vibecopd install            Install hooks into harness config
vibecopd uninstall          Remove hooks
vibecopd test               Test connection to the configured LLM endpoint
vibecopd refine             Regenerate the Guardian prompt (uses recent activity as context)
```

---

## Why not just use Open Vibe Island?

[Open Vibe Island](https://github.com/nichochar/open-vibe-island) has a VibeCop feature that does the same thing. It's great. But it's an Electron app, which means you can't run it in locked-down corporate environments, and it requires a GUI. `vibecopd` is a single static binary with no dependencies that runs anywhere Go runs.

The core AI second-opinion logic is identical. The audit log schema is compatible.

---

## Status

Pre-release. The spec is finalized; implementation is underway.

See [`docs/spec.md`](docs/spec.md) for the full design specification.
