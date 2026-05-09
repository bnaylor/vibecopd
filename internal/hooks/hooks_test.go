package hooks

import (
	"strings"
	"testing"
)

func TestDetectClaudeCode(t *testing.T) {
	input := `{
		"tool_name": "Bash",
		"tool_input": { "command": "swift build" },
		"project_path": "/Users/test/src/my-project"
	}`

	nr, harness, err := DetectAndParse(strings.NewReader(input), "")
	if err != nil {
		t.Fatal(err)
	}
	if harness != HarnessClaude {
		t.Errorf("expected claude harness, got %s", harness)
	}
	if nr.Tool != "Bash" {
		t.Errorf("expected tool Bash, got %s", nr.Tool)
	}
	if nr.Input != "swift build" {
		t.Errorf("expected input 'swift build', got %s", nr.Input)
	}
	if nr.ProjectPath != "/Users/test/src/my-project" {
		t.Errorf("expected /Users/test/src/my-project, got %s", nr.ProjectPath)
	}
	if nr.Event != EventPreToolUse {
		t.Errorf("expected default event %s, got %s", EventPreToolUse, nr.Event)
	}
}

func TestDetectClaudeCodeWithExplicitEvent(t *testing.T) {
	input := `{
		"hook_event_name": "PreToolUse",
		"tool_name": "Bash",
		"tool_input": { "command": "go test" }
	}`
	nr, harness, err := DetectAndParse(strings.NewReader(input), "")
	if err != nil {
		t.Fatal(err)
	}
	if harness != HarnessClaude {
		t.Errorf("expected claude, got %s", harness)
	}
	if nr.Event != EventPreToolUse {
		t.Errorf("expected event PreToolUse, got %s", nr.Event)
	}
}

func TestDetectClaudeCodeRead(t *testing.T) {
	input := `{
		"tool_name": "Read",
		"tool_input": { "file_path": "/Users/test/main.go" }
	}`

	nr, harness, err := DetectAndParse(strings.NewReader(input), "")
	if err != nil {
		t.Fatal(err)
	}
	if harness != HarnessClaude {
		t.Errorf("expected claude, got %s", harness)
	}
	if nr.Tool != "Read" {
		t.Errorf("expected Read, got %s", nr.Tool)
	}
	if nr.Input != "/Users/test/main.go" {
		t.Errorf("expected file path, got %s", nr.Input)
	}
}

func TestDetectGeminiCLI(t *testing.T) {
	input := `{
		"hook_event_name": "BeforeTool",
		"tool_name": "Bash",
		"tool_input": { "command": "go test ./..." },
		"cwd": "/Users/test/src/my-project"
	}`

	nr, harness, err := DetectAndParse(strings.NewReader(input), "")
	if err != nil {
		t.Fatal(err)
	}
	if harness != HarnessGemini {
		t.Errorf("expected gemini harness, got %s", harness)
	}
	if nr.Tool != "Bash" {
		t.Errorf("expected tool Bash, got %s", nr.Tool)
	}
	if nr.Input != "go test ./..." {
		t.Errorf("expected input 'go test ./...', got %s", nr.Input)
	}
	if nr.ProjectPath != "/Users/test/src/my-project" {
		t.Errorf("expected /Users/test/src/my-project, got %s", nr.ProjectPath)
	}
	if nr.Event != EventBeforeTool {
		t.Errorf("expected event BeforeTool, got %s", nr.Event)
	}
}

func TestDetectWithHint(t *testing.T) {
	// Valid Gemini payload forced as Claude — should still parse, since
	// both share the snake_case shape; the hint forces claude harness.
	input := `{ "hook_event_name": "BeforeTool", "tool_name": "Bash", "tool_input": { "command": "echo hi" } }`

	_, harness, err := DetectAndParse(strings.NewReader(input), HarnessClaude)
	if err != nil {
		t.Fatalf("hint parse failed: %v", err)
	}
	if harness != HarnessClaude {
		t.Errorf("hint should force claude, got %s", harness)
	}
}

func TestDetectUnknownPayload(t *testing.T) {
	_, _, err := DetectAndParse(strings.NewReader(`{ "unknown": "data" }`), "")
	if err == nil {
		t.Fatal("expected error for unknown payload")
	}
}

func TestDetectEmptyStdin(t *testing.T) {
	_, _, err := DetectAndParse(strings.NewReader(""), "")
	if err == nil {
		t.Fatal("expected error for empty stdin")
	}
}

func TestToolInputSummaryBash(t *testing.T) {
	m := map[string]any{"command": "go build ./..."}
	if s := toolInputSummary(m); s != "go build ./..." {
		t.Errorf("expected 'go build ./...', got %s", s)
	}
}

func TestToolInputSummaryRead(t *testing.T) {
	m := map[string]any{"file_path": "/tmp/test.txt"}
	if s := toolInputSummary(m); s != "/tmp/test.txt" {
		t.Errorf("expected '/tmp/test.txt', got %s", s)
	}
}

func TestToolInputSummaryFallback(t *testing.T) {
	m := map[string]any{"url": "https://example.com"}
	s := toolInputSummary(m)
	if s == "" {
		t.Error("expected fallback serialization")
	}
}

func TestFindProjectRoot(t *testing.T) {
	// Starting from the hooks package dir, should find go.mod.
	root := findProjectRoot(".")
	if root == "" {
		t.Fatal("expected to find a project root")
	}
}

func TestInputSummary(t *testing.T) {
	nr := &NormalizedRequest{Input: "line1\nline2\nline3"}
	if s := nr.InputSummary(); s != "line1" {
		t.Errorf("expected 'line1', got %s", s)
	}
}

func TestDetectCodexPreToolUse(t *testing.T) {
	input := `{
		"hook_event_name": "PreToolUse",
		"session_id": "sess-1",
		"transcript_path": "/tmp/t.json",
		"cwd": "/Users/test/src/codex-proj",
		"model": "gpt-5",
		"turn_id": "turn-1",
		"tool_name": "Bash",
		"tool_use_id": "use-1",
		"tool_input": { "command": "ls" }
	}`
	nr, harness, err := DetectAndParse(strings.NewReader(input), "")
	if err != nil {
		t.Fatal(err)
	}
	if harness != HarnessCodex {
		t.Errorf("expected codex harness, got %s", harness)
	}
	if nr.Event != EventPreToolUse {
		t.Errorf("expected event PreToolUse, got %s", nr.Event)
	}
	if nr.Tool != "Bash" || nr.Input != "ls" {
		t.Errorf("unexpected normalization: tool=%q input=%q", nr.Tool, nr.Input)
	}
	if nr.ProjectPath != "/Users/test/src/codex-proj" {
		t.Errorf("expected cwd as project path, got %s", nr.ProjectPath)
	}
}

func TestDetectCodexPermissionRequest(t *testing.T) {
	input := `{
		"hook_event_name": "PermissionRequest",
		"session_id": "sess-1",
		"cwd": "/proj",
		"tool_name": "Bash",
		"tool_input": { "command": "rm -rf /", "description": "danger" }
	}`
	nr, harness, err := DetectAndParse(strings.NewReader(input), "")
	if err != nil {
		t.Fatal(err)
	}
	if harness != HarnessCodex {
		t.Errorf("expected codex, got %s", harness)
	}
	if nr.Event != EventPermissionRequest {
		t.Errorf("expected PermissionRequest, got %s", nr.Event)
	}
}

func TestDetectCopilot(t *testing.T) {
	input := `{
		"timestamp": 1704614600000,
		"cwd": "/path/to/project",
		"toolName": "bash",
		"toolArgs": "{\"command\":\"rm -rf dist\"}"
	}`
	nr, harness, err := DetectAndParse(strings.NewReader(input), "")
	if err != nil {
		t.Fatal(err)
	}
	if harness != HarnessCopilot {
		t.Errorf("expected copilot, got %s", harness)
	}
	if nr.Event != EventCopilotPreToolUse {
		t.Errorf("expected event preToolUse, got %s", nr.Event)
	}
	if nr.Tool != "bash" {
		t.Errorf("expected tool bash, got %s", nr.Tool)
	}
	if nr.Input != "rm -rf dist" {
		t.Errorf("expected unmarshalled command, got %q", nr.Input)
	}
	if nr.ProjectPath != "/path/to/project" {
		t.Errorf("expected /path/to/project, got %s", nr.ProjectPath)
	}
}

func TestDetectCopilotMalformedToolArgs(t *testing.T) {
	input := `{
		"toolName": "bash",
		"toolArgs": "not-json-at-all"
	}`
	nr, harness, err := DetectAndParse(strings.NewReader(input), "")
	if err != nil {
		t.Fatal(err)
	}
	if harness != HarnessCopilot {
		t.Errorf("expected copilot, got %s", harness)
	}
	if nr.Input != "not-json-at-all" {
		t.Errorf("expected raw fallback, got %q", nr.Input)
	}
}

func TestDetectGeminiSetsBeforeToolEvent(t *testing.T) {
	input := `{ "hook_event_name": "BeforeTool", "tool_name": "Bash", "tool_input": {} }`
	nr, harness, err := DetectAndParse(strings.NewReader(input), "")
	if err != nil {
		t.Fatal(err)
	}
	if harness != HarnessGemini {
		t.Errorf("expected gemini, got %s", harness)
	}
	if nr.Event != EventBeforeTool {
		t.Errorf("expected default BeforeTool, got %s", nr.Event)
	}
}

func TestDetectClaudeWhenNoHookEventName(t *testing.T) {
	// snake_case payload with tool_name and no hook_event_name → Claude
	// (Claude's payload sometimes omits the field on older versions).
	input := `{ "tool_name": "Bash", "tool_input": { "command": "ls" } }`
	_, harness, err := DetectAndParse(strings.NewReader(input), "")
	if err != nil {
		t.Fatal(err)
	}
	if harness != HarnessClaude {
		t.Errorf("expected claude default, got %s", harness)
	}
}

func TestSanitizeHarnessKnownAndUnknown(t *testing.T) {
	for _, h := range []string{HarnessClaude, HarnessGemini, HarnessCodex, HarnessCopilot} {
		if got := SanitizeHarness(h); got != h {
			t.Errorf("SanitizeHarness(%q) = %q; want %q", h, got, h)
		}
	}
	for _, h := range []string{"", "deepseek", "../../etc/passwd", "claude\x00", "CLAUDE"} {
		if got := SanitizeHarness(h); got != "" {
			t.Errorf("SanitizeHarness(%q) = %q; want empty (clamped)", h, got)
		}
	}
}

func TestSanitizeHookEventKnownAndUnknown(t *testing.T) {
	for _, e := range []string{EventPreToolUse, EventPermissionRequest, EventBeforeTool, EventCopilotPreToolUse} {
		if got := SanitizeHookEvent(e); got != e {
			t.Errorf("SanitizeHookEvent(%q) = %q; want %q", e, got, e)
		}
	}
	for _, e := range []string{"", "PostToolUse", "fizz", "preToolUseV2"} {
		if got := SanitizeHookEvent(e); got != "" {
			t.Errorf("SanitizeHookEvent(%q) = %q; want empty (clamped)", e, got)
		}
	}
}

func TestParseWithFormatCodexMissingEvent(t *testing.T) {
	// Codex always sends hook_event_name, but if it's missing we fall to
	// the PreToolUse default rather than fail.
	input := `{ "tool_name": "Bash", "tool_input": { "command": "go test" } }`
	nr, _, err := DetectAndParse(strings.NewReader(input), HarnessCodex)
	if err != nil {
		t.Fatal(err)
	}
	if nr.Event != EventPreToolUse {
		t.Errorf("expected PreToolUse fallback, got %s", nr.Event)
	}
}
