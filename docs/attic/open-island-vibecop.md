# VibeCop

Use a second, independent AI to handle permission approvals automatically.

## Rationale

Permission checks exist because a coding agent might get confused or manipulated by bad instructions. But in practice, most permissions are routine — the same `bash`, `write_file`, and `read_file` calls the agent always makes — and humans inevitably start rubber-stamping them. At that point the checks provide only the illusion of safety.

A second, *unrelated* AI evaluating the same requests changes this dynamic. It shares no context with the primary agent, so it cannot be poisoned by injected instructions or a confused conversation thread. If it is given knowledge of the project's normal patterns, it can make accurate, fast decisions on routine tool use and escalate the genuinely ambiguous cases to the human. The human reviews less and pays more attention when they do.

A capable mid-size model running locally via Ollama handles this for roughly zero cost and adds only a small latency overhead (typically < 500 ms on a modern MacBook Pro).

## Modes

VibeCop operates in two modes:

**Baseline mode** — No project initialization required. VibeCop pattern-matches tool use against a built-in list of obviously dangerous operations: recursive deletes of paths outside the project directory, network requests to unexpected external hosts, credential-file access, exfiltration patterns, and similar. Routine operations pass automatically. This mode is the fallback when VibeCop is enabled globally but a project has not been initialized.

**Guardian mode** — VibeCop has been initialized for the current project. It receives a system prompt that describes the project's tech stack, its expected tool-use patterns, and what kinds of operations would be surprising. It can make accurate contextual judgments ("this `swift build` is normal; this `rm -rf` targeting the project's own output directory is fine; this `curl` to an external IP is not"). Escalation rates are lower and verdicts are more trustworthy.

## Configuration

VibeCop is **off by default**. Enable it in Settings → VibeCop.

Required settings:
- **Endpoint** — chat completions URL for your chosen provider (e.g. `http://localhost:11434/v1/chat/completions` for Ollama, `https://api.anthropic.com/v1/messages` for Anthropic)
- **API Format** — `OpenAI-compatible` (default) or `Anthropic`. Controls the request/response shape and authentication headers sent to the endpoint. Anthropic format uses `x-api-key` + `anthropic-version` headers and the native Messages API shape (top-level `system` field, `content` array in the response).
- **Model** — model name to pass in the request (e.g. `qwen3:14b` for Ollama, `claude-haiku-4-5` for Anthropic)
- **API key** — optional for Ollama; required for cloud endpoints

Advanced settings (sensible defaults; adjustable in Settings → VibeCop → Advanced):
- **Timeout** — how long to wait for a VibeCop response before escalating to human (default: **5 seconds**)
- **Activity window** — number of recent tool-use decisions included as context in each request (default: **10**)

The configuration panel includes a **Test Connection** button that sends a minimal probe request and reports success or failure with the raw error if it fails. It respects the selected API format.

### Thinking control (Ollama)

For Ollama models with chain-of-thought reasoning (qwen3, deepseek-r1, etc.), VibeCop sends `"think": false` in the request body. This disables reasoning tokens, keeping the response fast and within token limits without needing a separate `-nothink` model tag.

### Recommended models

For local inference via Ollama, on a MacBook Pro with sufficient RAM:

| RAM    | Recommended model       | Notes |
|--------|-------------------------|-------|
| 16 GB  | `mistral-nemo:latest`   | Fast, good instruction following |
| 32 GB  | `qwen3:14b`     | Strong code/dev reasoning, recommended default. Thinking is disabled via the `think: false` API option. |
| 64 GB+ | `qwen3:32b` or `gemma3:27b` | Approaches cloud quality for this task |

For cloud fallback: `claude-haiku-4-5` via the Anthropic API format is fast, cheap, and has strong understanding of developer workflows. Set the endpoint to `https://api.anthropic.com/v1/messages`, API Format to `Anthropic`, and provide your API key.

Also recommended: `gemini-2.5-flash` via Gemini's OpenAI-compatible endpoint (`https://generativelanguage.googleapis.com/v1beta/openai/chat/completions`). Uses the standard OpenAI-compatible format — no special configuration needed beyond the endpoint URL and API key.

Speed matters here — VibeCop is in the critical path for a blocking permission check. Prefer smaller/faster models over larger/slower ones. A 14B model is more than capable for structured verdict generation.

### Latency monitoring

The settings pane shows the average round-trip latency from recent VibeCop evaluations (rolling window of the last 50 requests) with the sample count. This helps users tune their timeout setting and evaluate model performance. The display refreshes each time the VibeCop settings tab is visited.

## Per-project initialization

VibeCop must be initialized once per project before Guardian mode activates. Initialization generates a project-specific system prompt that tells VibeCop what is normal for this codebase.

**Trigger**: When VibeCop is globally enabled and Open Island receives a `SessionStart` event for a workspace it has not seen before, it shows a prompt: "New project detected — initialize VibeCop for Guardian mode?" with options to initialize now, initialize later, or skip (keeps Baseline mode for this project indefinitely).

**How initialization works**: Open Island shells out to a coding agent the user already has available (Claude Code, Gemini CLI, etc.) using its non-interactive / print mode, passing a baked-in initialization prompt. The agent reads the project, generates a VibeCop system prompt, and prints it to stdout. Open Island captures that output and saves it. No special harness or tool-calling infrastructure is needed inside Open Island itself — it just spawns a subprocess and reads stdout.

Example invocation (Claude Code):
```
claude -p "<initialization prompt>" --output-format text
```

The user selects which agent to use for initialization in Settings → VibeCop. The initialization prompt is built-in to Open Island (see below). The generated system prompt is saved to the project's VibeCop directory and displayed in a review editor before being committed, so the user can inspect and edit it.

**Storage**: Per-project data lives at:
```
~/.open-island/vibecop/<sha256-of-project-path>/
  system-prompt.md      # generated by initialization, editable
  activity.jsonl        # rolling activity log
  .skip-init            # marker file to keep Baseline mode permanently
  audit/                # created when audit logging is enabled
    YYYY-MM-DD.jsonl    # daily audit records (one JSON line per evaluation)
```

The project path SHA256 provides a stable, collision-free key.

**Prompt refinement**: Occasionally (on user request, or after a configurable number of escalations), VibeCop can be asked to refine its own system prompt based on accumulated activity. The same subprocess invocation pattern is used: pass the current system prompt and a sample of recent activity, get an improved prompt back.

## Request protocol

When a `PermissionRequest` hook event arrives and VibeCop is active for the session's project, Open Island fires an async evaluation alongside the normal permission flow. The permission request is surfaced to the user immediately (so the UI is never blocked), and VibeCop's verdict arrives asynchronously.

**Request body** (sent as the user turn):
```json
{
  "tool": "<tool name>",
  "input": "<tool input summary>",
  "recent_activity": [
    /* last N tool-use verdicts in this session, N = configured activity window (default 10) */
    /* each entry: { tool, verdict } */
  ]
}
```

The system prompt is the project-specific prompt from Guardian mode, or the built-in baseline prompt in Baseline mode. For Ollama models with chain-of-thought reasoning, `"think": false` is included to skip reasoning overhead.

**Expected response** (VibeCop must reply with valid JSON, no prose):
```json
{ "verdict": "approve" | "deny" | "escalate", "reason": "..." }
```

- `approve` — pass through silently
- `deny` — treated as `escalate`: surfaces Open Island's normal approval popup with the VibeCop reason and a **Denied** badge. The human always has the final say; VibeCop's `deny` is a strong recommendation, not a unilateral block.
- `escalate` — surfaces Open Island's normal approval popup with the VibeCop reason prepended so the human has context

Both `deny` and `escalate` funnel through the same human-facing popup. The distinction is visible in the UI (badge color and label) and in the activity log, so the user can tell whether VibeCop was uncertain or actively concerned.

The `reason` field is required for `deny` and `escalate`; optional but welcome for `approve`.

**Timeout**: If VibeCop does not respond within the configured timeout (default: 5 seconds), treat as `escalate` with reason "VibeCop timed out."

## Activity log

The control center includes a VibeCop activity panel showing recent decisions in the current session:

- Tool name and a one-line input summary
- Verdict badge: **Approved** (auto-passed) / **Escalated** (VibeCop uncertain) / **Denied** (VibeCop flagged) / **Human: Approved** / **Human: Blocked** (human resolved an escalation or denial)
- VibeCop's reason (shown on expand for any non-approve verdict)
- Timestamp

This lets the user stay aware of what VibeCop is doing and spot systematic mistakes in the generated system prompt.

## Failure handling

If the VibeCop endpoint is unreachable, returns a non-200 status, or returns a response that cannot be parsed as the expected JSON:

1. Log the error in the activity panel
2. Fall back to Open Island's normal behavior for that request (surface to human as usual)
3. After 3 consecutive failures, temporarily suspend VibeCop for the session and show a status indicator in the island UI

VibeCop **never blocks an agent** due to its own malfunction. Fail-open, consistent with the rest of Open Island.

## Audit logging

VibeCop can optionally save structured audit records to each project's directory. Enable this in Settings → VibeCop → "Save audit logs to project directory" (off by default).

When enabled, each evaluation writes one JSON record to a daily log file:
```
~/.open-island/vibecop/<sha256>/audit/YYYY-MM-DD.jsonl
```

**Record schema** (one JSON object per line):
```json
{
  "timestamp": "2026-05-04T12:34:56Z",
  "toolName": "Bash",
  "toolInput": "swift build",
  "verdict": "approve",
  "reason": "Routine build command for this project.",
  "humanDecision": null,
  "latencyMs": null
}
```

**Write timing**:
- **approve / deny** — record is written immediately with `humanDecision: null` (no human involvement)
- **escalate / timeout** — partial record is held pending until the human decides, then completed with `humanDecision: "approved"` or `"blocked"`
- **error** — recorded as `verdict: "error"` and held for human resolution, same as escalate

This provides a complete, auditable trail of every VibeCop evaluation and its outcome, independent of the in-memory activity log.

**How audit logs differ from activity.jsonl**: `activity.jsonl` is a short rolling window (default 10 entries) sent to VibeCop as context for its next decision — it helps the model see what it recently decided but old entries are discarded. Audit logs are a permanent, never-pruned historical record written only when the toggle is on. They include human decisions (which `activity.jsonl` does not) and are never read back by VibeCop.

## Built-in initialization prompt

This prompt is passed verbatim to the user's chosen agent during project initialization. The agent is expected to print a ready-to-use system prompt to stdout and nothing else.

```
You are generating a system prompt for VibeCop, a lightweight AI that reviews
tool-use requests from a coding agent in real time and decides whether to approve,
deny, or escalate them to a human.

Your task: analyze this project and produce a VibeCop system prompt that will
help it make accurate, conservative decisions about what tool use is normal and
expected here.

To do this:
1. Read the project root: look for README.md, CLAUDE.md, AGENTS.md, package.json,
   Package.swift, Cargo.toml, pyproject.toml, go.mod, or any other top-level
   config that reveals the tech stack and project purpose.
2. Note the language(s), build tools, test frameworks, and any external services
   or APIs the project uses.
3. Note any existing agent configuration (hooks, permissions) that hints at what
   kinds of tool use are already expected.

Then write a system prompt for VibeCop. The prompt must:
- Explain VibeCop's role: second-opinion AI for tool-use approvals, shared
  context with the primary agent, conservative by design.
- Describe this specific project: what it is, its tech stack, its build/test
  workflow, and what kinds of commands are routine.
- Give examples of what should be approved automatically for this project.
- Give examples of what should trigger escalation or denial.
- Specify the response format VibeCop must always use:
    { "verdict": "approve" | "deny" | "escalate", "reason": "..." }
- Instruct VibeCop: when in doubt, escalate rather than deny; never approve
  operations that touch files or network resources clearly outside the project.

CRITICAL: Your output will be saved verbatim as the VibeCop system prompt file.
Start with "You are VibeCop" as the very first line. Do NOT include any
introductory text, commentary, meta-commentary (e.g. "Now I have enough context"),
markdown fences, or closing remarks. Output ONLY the system prompt text,
beginning immediately with its first line and ending after its last.
No preamble of any kind.
```

## Built-in baseline system prompt

Used in Baseline mode (no project initialization). Focused on catching obviously dangerous patterns regardless of project context.

```
You are VibeCop, a safety reviewer for coding agent tool use. You receive tool
invocations one at a time and decide whether to approve, deny, or escalate them
to a human.

You have no specific knowledge of the current project. Apply conservative
baseline rules:

DENY immediately (these are almost always unintentional or malicious):
- Recursive deletion of paths at or above the home directory
- Commands that read from or write to ~/.ssh, ~/.gnupg, credential stores, or
  keychain databases
- Network requests to IP addresses or domains that look like exfiltration targets
  (non-local IPs from a shell command that also reads project files)
- Commands that modify shell startup files (.bashrc, .zshrc, .profile, etc.)
- Package installs that add globally visible binaries outside a known package
  manager workflow

ESCALATE (uncertain — surface to human):
- Any operation you cannot categorize confidently
- Unusual combinations of file reads and outbound network activity
- Operations on paths well outside the apparent working directory
- Any destructive operation (delete, overwrite) on files not created in this
  session

APPROVE everything else automatically.

Always respond with valid JSON only:
{ "verdict": "approve" | "deny" | "escalate", "reason": "..." }
```
