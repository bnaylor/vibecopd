# VibeCop Daemon (`vibecopd`) Design Specification

## Overview
`vibecopd` is a standalone, Golang-based background daemon and Terminal User Interface (TUI) that reimagines the Open Vibe Island AI second-opinion permission flow. It aims to provide an independent, context-free AI evaluation of routine tool-use requests to prevent "rubber-stamping" by humans, while eliminating the need for a heavy graphical interface. The daemon operates invisibly to intercept requests, while the TUI can be attached at any time to monitor activity, performance, and latency.

## Core Architecture

### 1. The Background Daemon (`vibecopd`)
- **Language**: Go.
- **Role**: A long-running background process that handles tool request interception, manages configuration, communicates with the LLM APIs, and applies Guardian/Baseline logic.
- **Configuration & Storage**: Uses a global configuration directory at `~/.vibecopd/`. This directory will store:
  - Global settings (API endpoints, keys, model selections).
  - The Unix Domain Socket file.
  - Per-project subdirectories containing `system-prompt.md`, `activity.jsonl`, and `audit/` logs.

### 2. IPC (Inter-Process Communication)
- **Protocol**: Newline-delimited JSON over a single Unix Domain Socket (`~/.vibecopd/daemon.sock`).
- **Multiplexing**: The socket supports multiplexed message types to handle two distinct flows:
  - `"type": "permission_request"`: Synchronous evaluations initiated by tool hooks.
  - `"type": "tui_subscribe"`: Asynchronous streaming of live activity metrics, latency updates, and logs to the TUI.

### 3. The TUI (Terminal User Interface)
- **Role**: A foreground monitoring interface.
- **Functionality**: Connects to the daemon's Unix socket via the `tui_subscribe` message type.
- **Display Elements**:
  - Live activity matrix (Approved/Escalated/Denied verdicts).
  - Real-time LLM round-trip latency reporting.
  - Audit logging tail output.
  - Project initialization status.

### 4. Hook Interception & Delegation (The Bridge)
- **Hooks**: Shell/Python scripts installed directly into the coding harnesses (Gemini CLI, Claude Code, Deepseek agents).
- **Flow**:
  1. The harness attempts to execute a tool.
  2. The installed hook intercepts the call and sends a `"type": "permission_request"` JSON payload to `~/.vibecopd/daemon.sock`.
  3. The daemon evaluates the request against its LLM configuration and returns a verdict.
- **Fallback Semantics**:
  - **Approve**: If the daemon returns `approve`, the hook exits `0`, silencing the prompt entirely.
  - **Escalate / Deny / Timeout**: The hook exits with a non-zero status code and writes the VibeCop reason/context to `stderr`. The host coding harness interprets the non-zero exit and automatically surfaces its native, built-in terminal permission UI, displaying the `stderr` context to the user. This elegantly solves the GUI-less escalation problem by utilizing the host harness.
- **Fail-Open**: If the daemon crashes, the socket is missing, or the LLM request times out, the hook instantly exits non-zero, immediately delegating the decision back to the human via the host harness.

## AI Modes & Initialization

### Guardian Mode (Per-Project)
- Projects are initialized by shelling out to a standard coding agent (e.g., Claude Code, Gemini CLI) using a built-in initialization prompt.
- The agent analyzes the workspace and writes a `system-prompt.md` to the project's subdirectory within `~/.vibecopd/<project-hash>/`.
- VibeCop uses this context to accurately evaluate routine vs. dangerous operations specific to the project's stack.

### Baseline Mode (Global Fallback)
- If a project is not initialized, VibeCop defaults to a built-in strict Baseline prompt.
- This prompt aggressively flags paths outside the project, modifications to user profiles, and outbound network requests to unexpected hosts.

## Model Support & Configuration
- **First-Class Models**: Gemini (via `generativelanguage.googleapis.com`), Claude (via `api.anthropic.com`), and Deepseek (via Ollama or direct API).
- **API Formats**: Native support for `OpenAI-compatible` and `Anthropic` formats.
- **Ollama Specifics**: Disables internal chain-of-thought generation (`"think": false`) to minimize token consumption and reduce latency, treating speed as a critical priority.

## Auditing & Telemetry
- **Activity Log**: Maintains a rolling window of recent evaluations (`activity.jsonl`) fed into the context window for Guardian mode decisions.
- **Audit Log**: Optional capability to write permanent historical records of all decisions (Approve, Deny, Escalate, human resolution) to `~/.vibecopd/<project-hash>/audit/YYYY-MM-DD.jsonl`.

## Development Roadmap
1. Scaffolding the Go project structure and CLI subcommands (`start`, `tui`, `install-hooks`).
2. Implementing the Unix Domain Socket server and newline-delimited JSON router.
3. Hook generation and installation logic for supported harnesses.
4. LLM integration (OpenAI, Anthropic clients) and Guardian/Baseline configuration logic.
5. Building the TUI (using Bubbletea/Tcell or similar Go TUI framework) to subscribe to socket metrics.
6. End-to-end testing with Gemini CLI and Claude Code.