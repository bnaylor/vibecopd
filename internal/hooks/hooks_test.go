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
		"tool": "Bash",
		"input": "go test ./...",
		"project": "/Users/test/src/my-project"
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
}

func TestDetectWithHint(t *testing.T) {
	// Valid Gemini payload forced as Claude — should fail.
	input := `{ "tool": "Bash", "input": "echo hi" }`

	_, _, err := DetectAndParse(strings.NewReader(input), HarnessClaude)
	if err == nil {
		t.Fatal("expected error when parsing gemini payload as claude")
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
