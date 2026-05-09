package hooks

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ClaudeCodePayload is a Claude Code PreToolUse hook payload.
type ClaudeCodePayload struct {
	HookEventName string         `json:"hook_event_name,omitempty"`
	ToolName      string         `json:"tool_name"`
	ToolInput     map[string]any `json:"tool_input"`
	ProjectPath   string         `json:"project_path,omitempty"`
	Cwd           string         `json:"cwd,omitempty"`
}

// GeminiCLIPayload is a Gemini CLI BeforeTool hook payload.
//
// Same snake_case shape as Claude — the canonical reference is
// https://github.com/google-gemini/gemini-cli/blob/main/docs/hooks/reference.md.
type GeminiCLIPayload struct {
	HookEventName string         `json:"hook_event_name,omitempty"`
	ToolName      string         `json:"tool_name"`
	ToolInput     map[string]any `json:"tool_input"`
	Cwd           string         `json:"cwd,omitempty"`
	SessionID     string         `json:"session_id,omitempty"`
}

// CodexPayload is a Codex CLI PreToolUse / PermissionRequest hook payload.
// Same snake_case shape as Claude with extra Codex-specific fields and a
// required hook_event_name.
type CodexPayload struct {
	HookEventName  string         `json:"hook_event_name"`
	SessionID      string         `json:"session_id,omitempty"`
	TranscriptPath string         `json:"transcript_path,omitempty"`
	Cwd            string         `json:"cwd,omitempty"`
	Model          string         `json:"model,omitempty"`
	TurnID         string         `json:"turn_id,omitempty"`
	ToolName       string         `json:"tool_name"`
	ToolUseID      string         `json:"tool_use_id,omitempty"`
	ToolInput      map[string]any `json:"tool_input"`
}

// CopilotPayload is a GitHub Copilot CLI preToolUse hook payload.
// camelCase fields, no hook_event_name on the wire, toolArgs is a
// stringified JSON value rather than an object.
type CopilotPayload struct {
	Timestamp int64  `json:"timestamp,omitempty"`
	Cwd       string `json:"cwd,omitempty"`
	ToolName  string `json:"toolName"`
	ToolArgs  string `json:"toolArgs,omitempty"`
}

// NormalizedRequest is the harness-agnostic tool-use request.
type NormalizedRequest struct {
	Tool        string
	Input       string
	ProjectPath string
	Event       string
}

// Harness identifiers.
const (
	HarnessClaude  = "claude"
	HarnessGemini  = "gemini"
	HarnessCodex   = "codex"
	HarnessCopilot = "copilot"
	HarnessUnknown = ""
)

// Hook event names. Casing matches each harness's wire format.
const (
	EventPreToolUse        = "PreToolUse"        // Claude, Codex
	EventPermissionRequest = "PermissionRequest" // Codex
	EventBeforeTool        = "BeforeTool"        // Gemini
	EventCopilotPreToolUse = "preToolUse"        // Copilot (camelCase)
)

// SanitizeHarness returns harness only if it is one of the recognized
// harness constants; otherwise empty. Used at the daemon ↔ telemetry
// boundary to clamp metric/span label cardinality so a malicious local
// UDS client can't blow up the dashboard with arbitrary harness strings.
func SanitizeHarness(harness string) string {
	switch harness {
	case HarnessClaude, HarnessGemini, HarnessCodex, HarnessCopilot:
		return harness
	}
	return ""
}

// SanitizeHookEvent returns event only if it is one of the recognized
// per-harness event names; otherwise empty. Same purpose as SanitizeHarness.
func SanitizeHookEvent(event string) string {
	switch event {
	case EventPreToolUse, EventPermissionRequest, EventBeforeTool, EventCopilotPreToolUse:
		return event
	}
	return ""
}

// defaultEventFor returns the wire-format event name when a payload omits
// hook_event_name. Codex always sends one; absence there is a parse error.
func defaultEventFor(harness string) string {
	switch harness {
	case HarnessClaude:
		return EventPreToolUse
	case HarnessGemini:
		return EventBeforeTool
	case HarnessCopilot:
		return EventCopilotPreToolUse
	default:
		return ""
	}
}

// toolInputSummary extracts a one-line summary from a tool_input map.
// Handles common patterns like {"command": "..."} or {"file_path": "...", "content": "..."}.
func toolInputSummary(m map[string]any) string {
	// Bash: {"command": "swift build"}
	if cmd, ok := m["command"]; ok {
		if s, ok := cmd.(string); ok {
			return s
		}
	}

	// Read/Write: {"file_path": "...", "content": "..."}
	if fp, ok := m["file_path"]; ok {
		if s, ok := fp.(string); ok {
			return s
		}
	}

	// Fallback: serialize the whole map.
	data, _ := json.Marshal(m)
	return string(data)
}

// DetectAndParse reads a harness payload from r, auto-detects the harness,
// and returns a normalized request along with the detected harness name.
// If harnessHint is non-empty, auto-detection is skipped.
func DetectAndParse(r io.Reader, harnessHint string) (*NormalizedRequest, string, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, HarnessUnknown, fmt.Errorf("read stdin: %w", err)
	}

	raw := string(data)

	// Check harness hint first.
	if harnessHint != "" {
		return parseWithFormat(raw, harnessHint)
	}

	// Peek at distinguishing fields without locking in a struct shape.
	var probe map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &probe); err != nil {
		return nil, HarnessUnknown, fmt.Errorf("unrecognized payload format: %s", truncate(raw, 200))
	}

	// Copilot: camelCase toolName distinguishes it from the snake_case
	// payloads. No hook_event_name on the wire.
	if _, ok := probe["toolName"]; ok {
		return parseWithFormat(raw, HarnessCopilot)
	}

	// Claude / Codex / Gemini all use snake_case tool_name + tool_input.
	// Disambiguate by hook_event_name, with a Codex-vs-Claude fallback for
	// PreToolUse (which both honor) keyed on turn_id — the one Codex-only
	// field. transcript_path and model overlap with Claude in newer versions.
	if _, ok := probe["tool_name"]; ok {
		switch stringField(probe, "hook_event_name") {
		case EventPermissionRequest:
			return parseWithFormat(raw, HarnessCodex)
		case EventBeforeTool:
			return parseWithFormat(raw, HarnessGemini)
		case EventPreToolUse:
			if _, ok := probe["turn_id"]; ok {
				return parseWithFormat(raw, HarnessCodex)
			}
			return parseWithFormat(raw, HarnessClaude)
		default:
			return parseWithFormat(raw, HarnessClaude)
		}
	}

	return nil, HarnessUnknown, fmt.Errorf("unrecognized payload format: %s", truncate(raw, 200))
}

func parseWithFormat(raw, harness string) (*NormalizedRequest, string, error) {
	switch harness {
	case HarnessClaude:
		var p ClaudeCodePayload
		if err := json.Unmarshal([]byte(raw), &p); err != nil {
			return nil, harness, fmt.Errorf("claude payload: %w", err)
		}
		if p.ToolName == "" {
			return nil, harness, fmt.Errorf("claude payload missing tool_name")
		}
		return normalizeClaudeCode(p), harness, nil
	case HarnessGemini:
		var p GeminiCLIPayload
		if err := json.Unmarshal([]byte(raw), &p); err != nil {
			return nil, harness, fmt.Errorf("gemini payload: %w", err)
		}
		if p.ToolName == "" {
			return nil, harness, fmt.Errorf("gemini payload missing tool_name")
		}
		return normalizeGeminiCLI(p), harness, nil
	case HarnessCodex:
		var p CodexPayload
		if err := json.Unmarshal([]byte(raw), &p); err != nil {
			return nil, harness, fmt.Errorf("codex payload: %w", err)
		}
		if p.ToolName == "" {
			return nil, harness, fmt.Errorf("codex payload missing tool_name")
		}
		return normalizeCodex(p), harness, nil
	case HarnessCopilot:
		var p CopilotPayload
		if err := json.Unmarshal([]byte(raw), &p); err != nil {
			return nil, harness, fmt.Errorf("copilot payload: %w", err)
		}
		if p.ToolName == "" {
			return nil, harness, fmt.Errorf("copilot payload missing toolName")
		}
		return normalizeCopilot(p), harness, nil
	default:
		return nil, harness, fmt.Errorf("unsupported harness: %s", harness)
	}
}

func normalizeClaudeCode(p ClaudeCodePayload) *NormalizedRequest {
	nr := &NormalizedRequest{
		Tool:  p.ToolName,
		Input: toolInputSummary(p.ToolInput),
		Event: p.HookEventName,
	}
	if nr.Event == "" {
		nr.Event = defaultEventFor(HarnessClaude)
	}
	switch {
	case p.ProjectPath != "":
		nr.ProjectPath = p.ProjectPath
	case p.Cwd != "":
		nr.ProjectPath = p.Cwd
	default:
		nr.ProjectPath = detectProjectDir()
	}
	return nr
}

func normalizeGeminiCLI(p GeminiCLIPayload) *NormalizedRequest {
	nr := &NormalizedRequest{
		Tool:  p.ToolName,
		Input: toolInputSummary(p.ToolInput),
		Event: p.HookEventName,
	}
	if nr.Event == "" {
		nr.Event = defaultEventFor(HarnessGemini)
	}
	if p.Cwd != "" {
		nr.ProjectPath = p.Cwd
	} else {
		nr.ProjectPath = detectProjectDir()
	}
	return nr
}

func normalizeCodex(p CodexPayload) *NormalizedRequest {
	nr := &NormalizedRequest{
		Tool:  p.ToolName,
		Input: toolInputSummary(p.ToolInput),
		Event: p.HookEventName,
	}
	// Codex documents hook_event_name as always present
	// (developers.openai.com/codex/hooks); we still default to PreToolUse on
	// absence rather than rejecting, to honor fail-open.
	if nr.Event == "" {
		nr.Event = EventPreToolUse
	}
	if p.Cwd != "" {
		nr.ProjectPath = p.Cwd
	} else {
		nr.ProjectPath = detectProjectDir()
	}
	return nr
}

func normalizeCopilot(p CopilotPayload) *NormalizedRequest {
	nr := &NormalizedRequest{
		Tool:  p.ToolName,
		Event: defaultEventFor(HarnessCopilot),
	}
	// toolArgs is a stringified JSON value. Try to decode and run the same
	// summarizer as Claude/Codex; fall back to the raw string.
	if p.ToolArgs != "" {
		var m map[string]any
		if err := json.Unmarshal([]byte(p.ToolArgs), &m); err == nil {
			nr.Input = toolInputSummary(m)
		} else {
			nr.Input = p.ToolArgs
		}
	}
	if p.Cwd != "" {
		nr.ProjectPath = p.Cwd
	} else {
		nr.ProjectPath = detectProjectDir()
	}
	return nr
}

// detectProjectDir uses the CWD as the project path, walking up to find a
// recognizable project root marker.
func detectProjectDir() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return findProjectRoot(cwd)
}

// findProjectRoot walks up from dir looking for project markers.
func findProjectRoot(dir string) string {
	markers := []string{
		"go.mod", "package.json", "Package.swift", "Cargo.toml",
		"pyproject.toml", "Gemfile", "README.md",
	}

	d := dir
	for {
		for _, m := range markers {
			if _, err := os.Stat(filepath.Join(d, m)); err == nil {
				return d
			}
		}
		parent := filepath.Dir(d)
		if parent == d {
			// Reached filesystem root — fall back to original dir.
			return dir
		}
		d = parent
	}
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

// ProjectPath returns the resolved project path from the request,
// falling back to the CWD if empty.
func (nr *NormalizedRequest) ProjectPathResolved() string {
	if nr.ProjectPath != "" {
		return nr.ProjectPath
	}
	cwd, _ := os.Getwd()
	return cwd
}

// InputSummary returns a single-line summary of the tool input.
func (nr *NormalizedRequest) InputSummary() string {
	return strings.SplitN(nr.Input, "\n", 2)[0]
}

func stringField(m map[string]json.RawMessage, key string) string {
	raw, ok := m[key]
	if !ok {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return ""
	}
	return s
}
