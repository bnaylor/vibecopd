# Per-harness hook responses

**Status:** design
**Date:** 2026-05-08
**Tracks:** VCOP (next op-new)
**Supersedes:** the "Exit code contract" subsection in `docs/spec.md` and AGENTS.md invariant #3.

## Problem

`vibecop hook` currently signals verdicts purely via exit code:

| Verdict     | Exit | Stderr                          |
|-------------|------|---------------------------------|
| `approve`   | 0    | (silent)                        |
| `deny`      | 1    | `VibeCop [DENY]: <reason>`      |
| `escalate`  | 1    | `VibeCop [ESCALATE]: <reason>`  |

None of the supported harnesses honor that contract:

- **Claude Code, Codex, Copilot CLI:** exit 0 with no JSON falls through to the harness's normal permission flow; exit 1 is a logged warning, not a block; only exit 2 (or stdout JSON) actually decides anything.
- **Gemini CLI:** same — exit 1 is a warning, exit 2 blocks.

Result: vibecop's `approve` is a silent no-op (the harness still prompts the user under its own rules), and `deny` doesn't deny — the harness logs vibecop's stderr and proceeds. Earlier observations of working denials came from a now-absent local pass-through layer, not from the hook itself.

## Goal

Emit the correct **per-harness JSON response** on stdout so that `approve` actually skips the user prompt and `deny` actually blocks the tool — for all four supported harnesses (Claude Code, Codex, Gemini CLI, Copilot CLI). Preserve the existing fail-open invariant.

## Non-goals

- Changing the daemon ↔ hook IPC verdict shape (`{verdict, reason}` stays).
- Changing the LLM evaluator or activity-log behavior.
- Implementing a blocking-hold escalation flow (VCOP-11 ruled this out; still out of scope).
- Adding new top-level deps or replacing the JSON encoding stack.

## Decisions

### D1: Verdict → harness-response mapping

| Verdict    | Hook output                                                                                                                                                                  |
|------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `approve`  | Emit harness-native "allow" JSON. Skips the harness's user prompt.                                                                                                           |
| `deny`     | Emit harness-native "deny" JSON. Tool is blocked. Stderr `VibeCop [DENY]: <reason>` is preserved for operator visibility.                                                    |
| `escalate` | Emit no JSON, exit 0. The harness's normal permission flow runs (typically prompts the user). Internally, the audit logger queues the escalation for the TUI review queue (existing VCOP-11 path; unchanged). |

`escalate` deliberately does **not** map to a harness's "ask" decision: vibecop's preference is "no objection, defer to whatever the harness would have done" rather than forcing a prompt the harness might otherwise have skipped under a user-defined rule.

### D2: Per-harness JSON shapes

`WriteVerdict(harness, event, verdict, stdout, stderr) → exitCode`. All rows exit 0; the JSON is what the harness keys off.

| (harness, event)             | `approve`                                                                                                              | `deny`                                                                                                                       | `escalate`   |
|------------------------------|------------------------------------------------------------------------------------------------------------------------|------------------------------------------------------------------------------------------------------------------------------|--------------|
| `claude`, `PreToolUse`       | `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow","permissionDecisionReason":"…"}}`    | `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"…"}}` + stderr  | (no stdout)  |
| `codex`, `PreToolUse`        | (no stdout — Codex PreToolUse cannot allow; PermissionRequest is the approval channel)                                 | same JSON shape as Claude PreToolUse deny + stderr                                                                           | (no stdout)  |
| `codex`, `PermissionRequest` | `{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"allow"}}}`                         | `{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"deny","message":"…"}}}` + stderr         | (no stdout)  |
| `gemini`, `BeforeTool`       | `{"decision":"allow","reason":"…"}`                                                                                    | `{"decision":"deny","reason":"…"}` + stderr                                                                                  | (no stdout)  |
| `copilot`, `preToolUse`      | `{"permissionDecision":"allow"}`                                                                                       | `{"permissionDecision":"deny","permissionDecisionReason":"…"}` + stderr                                                      | (no stdout)  |

The `permissionDecisionReason` / `reason` / `message` fields are omitted from JSON when the daemon's reason is empty.

Unknown `(harness, event)` or unknown verdict → no stdout, `VibeCop: unrecognized harness=X event=Y, falling open` to stderr, exit 0. Fail-open is the contract.

### D3: Codex's two-event install

`PreToolUse` on Codex cannot approve (the `"allow"` value is parsed but unsupported and falls open). `PermissionRequest` is the only event Codex honors for silent approval. Therefore `installCodexHooks` registers the `vibecop hook` binary under **both** event keys. The single binary distinguishes via `hook_event_name` on stdin and dispatches via `WriteVerdict`'s `event` argument.

The dual registration is a Codex-specific quirk; Claude/Gemini/Copilot register a single hook each.

### D4: Pure-function dispatch in `internal/hooks/`

Decision-shaping logic lives in `internal/hooks/responder.go` as `WriteVerdict`. `cmd/hook.go` shrinks to:

```
parse → daemon dial → daemon decode → exitCode := WriteVerdict(...) → os.Exit(exitCode)
```

No JSON shaping in `cmd/`. Every `(harness, event, verdict)` row is table-tested against `bytes.Buffer` captures. No mocking framework.

### D5: Telemetry plumbing

`cmd/hook.go:39` currently discards the detected harness (`_ = detected`). That becomes load-bearing.

- `daemon.Request` gains `Harness string` and `HookEvent string` fields. The hook fills both before sending.
- `daemon.Event` gains the same two fields; the existing OTLP log subscriber inherits them automatically.
- `permission.check` span gets `vibecop.harness` and `vibecop.hook_event` attributes.
- `vibecop.verdicts_total` counter gains a `vibecop.harness` label.
- `vibecop.evaluator_latency_ms` histogram gains a `vibecop.harness` label.
- No new spans, instruments, or severity rules.

Cardinality: harness ∈ {4} × verdict ∈ {3} × tool (already unbounded but accepted). Net new cardinality is bounded.

The hook subprocess itself does not initialize OTLP — it remains daemon-side only. AGENTS.md invariant #9 (telemetry fail-open / nil-safe Provider) is preserved: new attributes go through the same nil-safe helpers.

## Files touched

- `internal/hooks/hooks.go` — `NormalizedRequest` gains `Event string`. `DetectAndParse` reads `hook_event_name` from stdin (or defaults per harness when absent: Claude → `PreToolUse`, Gemini → `BeforeTool`, Copilot → `preToolUse`; Codex requires it). New `CodexPayload` and `CopilotPayload` structs with parsers; `HarnessCodex` and `HarnessCopilot` constants.
- `internal/hooks/responder.go` *(new)* — `WriteVerdict(harness, event string, v daemon.Verdict, stdout, stderr io.Writer) int`. Pure function. Handles every row in the §D2 table; falls open on anything unknown.
- `internal/hooks/responder_test.go` *(new)* — table-driven coverage of every row + fall-open cases. Asserts exact stdout bytes, exact stderr line, exit code.
- `internal/hooks/install.go` — add `installCodexHooks` / `installCopilotHooks` and uninstall counterparts. Codex registers under `PreToolUse` **and** `PermissionRequest`.
- `internal/hooks/install_test.go` — extend with Codex (both events) and Copilot install/uninstall round-trips.
- `internal/hooks/hooks_test.go` — extend with Codex (both events) and Copilot payload-parsing fixtures.
- `internal/daemon/daemon.go` — add `Harness` and `HookEvent` to `Request` and `Event` types.
- `internal/daemon/daemon_test.go` — verify the new fields round-trip through the JSON router.
- `internal/telemetry/` — add `vibecop.harness` / `vibecop.hook_event` span attributes; add `vibecop.harness` label to existing instruments. Keep helpers nil-safe.
- `cmd/hook.go` — replace verdict `switch` with `os.Exit(hooks.WriteVerdict(harness, nr.Event, resp, os.Stdout, os.Stderr))`. Plumb `Harness` and `HookEvent` into the daemon request.
- `cmd/handler_test.go` — extend to verify `Request.Harness` / `Request.HookEvent` propagate to the verdict event.
- `cmd/install.go` — extend `--harness` enum to `claude|gemini|codex|copilot`.
- `docs/spec.md` — replace "Exit code contract" subsection with the §D2 table; replace "Hook scripts" subsection with all four harness configs.
- `AGENTS.md` — replace invariant #3 with the JSON-response contract.

## Open detail

Exact settings-file paths and JSON shapes for Codex and Copilot need a docs verification pass before install/uninstall code lands. The response-emission code (`responder.go`, `WriteVerdict`, dispatch) does not depend on knowing them — it can land and be tested independently. The first sub-task on the issue should be a short docs pass to nail down those paths.

## Test plan

1. **Unit:** `responder_test.go` covers every `(harness, event, verdict)` row plus fall-open. ~25 rows.
2. **Unit:** `hooks_test.go` parses fixtures for Codex (both events) and Copilot payloads.
3. **Unit:** `install_test.go` round-trips install/uninstall for the two new harnesses without dropping unrelated keys.
4. **Unit:** `daemon_test.go` confirms `Harness` / `HookEvent` round-trip the JSON router.
5. **Integration:** `vibecop test` probe still passes against the configured endpoint.
6. **Manual:** real-Claude smoke — install hook, force a synthetic deny via the test endpoint, observe Claude actually blocks the tool and renders the reason.
7. **Manual (deferred to install sub-task):** repeat smoke for Codex (PreToolUse deny, PermissionRequest allow), Gemini, Copilot once their install paths land.

## Failure modes preserved

- **Daemon unreachable** — same as today: hook exits 0 silently.
- **JSON marshal failure in `WriteVerdict`** — fall open: no stdout, exit 0.
- **Unknown harness or event** — fall open: no stdout, stderr diagnostic, exit 0.
- **Three-consecutive-failures pass-through** — daemon-side, unchanged.
- **Telemetry exporter failure** — daemon-side, unchanged.

## Out of scope (explicit)

- Blocking-hold escalation (deferred per VCOP-11).
- "Ask" mapping for `escalate` (D1 deliberately rejects this).
- `defer` decisions (Claude/Codex feature; not used).
- `updatedInput` / `modifiedArgs` rewriting (security-relevant; future issue).
- Adding new harnesses beyond the four named here.
