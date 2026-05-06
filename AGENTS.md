# AGENTS.md

Guidance for AI coding agents (Claude Code, Gemini CLI, Codex, etc.) working in this repo. Humans should read [README.md](README.md) and [docs/spec.md](docs/spec.md) first.

## What this project is

`vibecop` is a Go 1.22+ CLI + background daemon + tview TUI that sits between a coding agent and its permission prompts and asks a *second*, context-free LLM whether each tool-use request looks safe. It is itself a security tool — the bar for correctness is higher than for typical glue code.

The full design lives in [`docs/spec.md`](docs/spec.md). Read it before making non-trivial changes.

## Build, test, run

```sh
go build ./...                 # build all packages
go test ./...                  # run the whole suite
go test ./internal/<pkg>/...   # focused
go vet ./...                   # static checks
go run ./cmd/vibecop -- <subcmd>   # invoke the CLI without installing
```

There is no Makefile and no lint config beyond `go vet`. Keep it that way unless asked.

## Project layout

```
cmd/vibecop/main.go     # binary entry point
cmd/*.go                # one file per Cobra subcommand (start, hook, install, init, refine, …)
internal/
  daemon/               # UDS server, JSON router, broadcast events
  evaluator/            # LLM client (anthropic + openai formats), prompt resolution, init/refine
  hooks/                # harness payload parsing, settings.json patching for install/uninstall
  config/               # TOML loader, project hash (sha256 of abs path), storage paths
  audit/                # rolling activity.jsonl + permanent daily audit logs
  tui/                  # tview app: activity feed, latency, log tail
  telemetry/            # OTLP export (traces, metrics, logs); nil-safe
  setup/                # interactive first-run wizard
  attic/                # quarantined / experimental code; do not import
docs/spec.md            # canonical design spec (long, authoritative)
docs/test-matrix.md     # what is tested where
```

## Critical invariants — DO NOT BREAK

These are load-bearing. Spec sections in parentheses.

1. **Fail-open everywhere** ("Failure Handling"). If anything in vibecop's own code fails — bad config, daemon down, parse error, LLM 500 — the hook MUST exit 0 so the user's coding agent is never blocked. The one exception: a *successful* `deny` or `escalate` verdict from the LLM exits 1.
2. **Three consecutive evaluator failures → suspended pass-through** for the rest of the daemon's life (`cmd/start.go`, `maxConsecutiveFailures`). Resume requires `vibecop test` (or restart). Do not change this without updating spec + test (`cmd/handler_test.go`).
3. **Exit-code contract** ("Exit code contract" table). `approve→0`, `deny→1`, `escalate→1`, timeout→1, daemon unreachable→0. Stderr text is part of the contract — Claude Code and Gemini CLI display it to the user.
4. **Project identity = SHA256 of absolute path** (`config.ProjectHash`). Do not hash the basename, do not normalize symlinks. Per-project storage at `~/.vibecop/projects/<hash>/`.
5. **Activity log is ephemeral, audit log is permanent.** `activity.jsonl` is a rolling window of last `activity_window` verdicts (default 10) used as LLM context. `audit/YYYY-MM-DD.jsonl` is the permanent record, only written when `audit_enabled = true`. Never read audit logs back into prompts.
6. **`think: false` for Ollama CoT models.** Local endpoints with reasoning models (`qwen3`, `deepseek-r1`) need this in the request body to avoid 30s+ latencies. The injection lives in `internal/evaluator/`.
7. **Hook script must be a one-liner** that delegates to `vibecop hook`. Don't add logic to the installed shim.
8. **No GUI, no Electron, single static binary.** Reject any dep that drags in CGO or graphical frameworks.
9. **Telemetry is fail-open** (`internal/telemetry`). OTLP init failures, dropped events, or exporter timeouts MUST NOT block a permission check. All `*telemetry.Provider` helpers are nil-safe — the disabled state is a nil receiver, not a feature flag everywhere.
10. **Subsystem packages are domain-named, not protocol-named.** `internal/telemetry/` (not `internal/otlp/`); `internal/evaluator/` (not `internal/llm/`). The transport is an implementation detail of the subsystem.

## Patterns and conventions

- **Cobra subcommands** are one-per-file in `cmd/`. New subcommand → new file → register in `init()` with `rootCmd.AddCommand`.
- **Errors:** `fmt.Errorf("context: %w", err)` for wrapping. Daemon-internal errors go to `log.Printf`; user-facing errors go to `os.Stderr`.
- **Config access:** `VibeCopConfig()` reads the loaded global. Don't re-read config inside request handlers.
- **Concurrency:** the daemon uses `sync.Mutex` (not channels) for the per-project store map and the failure counter. TUI subscribers receive events on buffered channels with a `default:` drop case (silent backpressure — known limitation).
- **JSON IPC:** newline-terminated. Use `json.NewEncoder(conn).Encode(...)` and `json.NewDecoder(conn).Decode(...)`. Don't buffer.
- **Tests** live next to code (`foo_test.go`). Fakes are local types like `fakeEvaluator` in `cmd/handler_test.go` — no mocking framework. Keep it that way.
- **No new top-level dirs** without spec update.

## Adding a new harness (e.g. Deepseek)

1. Add a payload struct + parser in `internal/hooks/hooks.go` (mirror `ClaudeCodePayload` / `GeminiCLIPayload`).
2. Extend `DetectAndParse` and `parseWithFormat`.
3. Add subprocess invocation in `internal/evaluator/init.go` for the Guardian-prompt generation step.
4. Add idempotent settings-file patching in `internal/hooks/install.go`.
5. Update spec.md, README.md, this file, and `cmd/install.go`'s `--harness` enum.
6. Tests: payload parsing, install/uninstall round-trip.

## Adding a new LLM provider

1. Extend `Client.Evaluate` in `internal/evaluator/evaluator.go` with a new `api_format` branch.
2. Build request + parse response symmetrically with the existing `openai` / `anthropic` cases.
3. Honor `timeout_ms`, recent-activity context, and the JSON-only verdict response contract.
4. Add a row to the recommended-models table in spec.md and README.md.
5. Tests against captured fixture responses.

## Things that have bitten us before

Preserve these — they were fixed in commit `d839c76` ("code review: fix settings key loss, TUI freeze, think:false injection, baseline prompt gaps"):

- **`settings.json` patching** must not drop existing keys. Read → modify the relevant subtree → write the whole object back. Never `os.WriteFile` a partial JSON.
- **TUI `app.QueueUpdateDraw`** from a goroutine is required; calling tview directly from the socket goroutine deadlocks.
- **`think: false`** must be on the *request body* for Ollama, not the system prompt.
- **Baseline prompt** must enumerate denials/escalations explicitly — vague guidance produces inconsistent verdicts.

## Pre-commit checklist

Before claiming a task done:
- [ ] `go test ./...` is green
- [ ] `go vet ./...` is clean
- [ ] If you touched the spec'd contract (exit codes, IPC, config schema), spec.md is updated
- [ ] If you touched README-visible UX, README.md is updated
- [ ] No new top-level deps without justification (`go mod tidy` shows the diff)
- [ ] Manual smoke: `vibecop test` against your configured endpoint still succeeds

## House style

- Short comments only. The code should explain itself; reserve comments for non-obvious *why*.
- No `TODO:` / `FIXME:` left behind unless tied to a tracked issue.
- No emoji in code, commits, or stderr output.
- No premature abstraction. The codebase is small; flat is fine.

## Where to look first

- New here? Read `docs/spec.md` end-to-end, then trace a single request from `cmd/hook.go` → `internal/daemon/daemon.go:handleConn` → `cmd/start.go:makePermissionHandler` → `internal/evaluator/evaluator.go:Evaluate` → back out to exit code.
- Want to see what's tested? `docs/test-matrix.md`.
- Stuck? `git log --oneline -- <path>` shows recent intent; commit messages are descriptive.
