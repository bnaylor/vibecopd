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
	ToolName    string `json:"tool_name"`
	ToolInput   map[string]any `json:"tool_input"`
	ProjectPath string `json:"project_path,omitempty"`
}

// GeminiCLIPayload is a Gemini CLI before_tool hook payload.
type GeminiCLIPayload struct {
	Tool    string `json:"tool"`
	Input   string `json:"input"`
	Project string `json:"project,omitempty"`
}

// NormalizedRequest is the harness-agnostic tool-use request.
type NormalizedRequest struct {
	Tool        string
	Input       string
	ProjectPath string
}

// Harness identifiers.
const (
	HarnessClaude  = "claude"
	HarnessGemini  = "gemini"
	HarnessUnknown = ""
)

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

	// Auto-detect: try Claude Code format first (has "tool_name").
	var ccPayload ClaudeCodePayload
	if err := json.Unmarshal([]byte(raw), &ccPayload); err == nil && ccPayload.ToolName != "" {
		return normalizeClaudeCode(ccPayload), HarnessClaude, nil
	}

	// Try Gemini CLI format.
	var gemPayload GeminiCLIPayload
	if err := json.Unmarshal([]byte(raw), &gemPayload); err == nil && gemPayload.Tool != "" {
		return normalizeGeminiCLI(gemPayload), HarnessGemini, nil
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
		if p.ToolName == "" { return nil, harness, fmt.Errorf("claude payload missing tool_name") }; return normalizeClaudeCode(p), harness, nil
	case HarnessGemini:
		var p GeminiCLIPayload
		if err := json.Unmarshal([]byte(raw), &p); err != nil {
			return nil, harness, fmt.Errorf("gemini payload: %w", err)
		}
		if p.Tool == "" { return nil, harness, fmt.Errorf("gemini payload missing tool") }; return normalizeGeminiCLI(p), harness, nil
	default:
		return nil, harness, fmt.Errorf("unsupported harness: %s", harness)
	}
}

func normalizeClaudeCode(p ClaudeCodePayload) *NormalizedRequest {
	nr := &NormalizedRequest{
		Tool:  p.ToolName,
		Input: toolInputSummary(p.ToolInput),
	}

	if p.ProjectPath != "" {
		nr.ProjectPath = p.ProjectPath
	} else {
		nr.ProjectPath = detectProjectDir()
	}

	return nr
}

func normalizeGeminiCLI(p GeminiCLIPayload) *NormalizedRequest {
	nr := &NormalizedRequest{
		Tool:  p.Tool,
		Input: p.Input,
	}

	if p.Project != "" {
		nr.ProjectPath = p.Project
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
