package evaluator

import (
	"strings"
	"testing"
)

func TestInitializationPrompt(t *testing.T) {
	if InitializationPrompt == "" {
		t.Fatal("initialization prompt should not be empty")
	}
	if !strings.Contains(InitializationPrompt, "You are generating") {
		t.Error("should start with the expected opener")
	}
	if !strings.Contains(InitializationPrompt, "VibeCop") {
		t.Error("should mention VibeCop")
	}
	if !strings.Contains(InitializationPrompt, "go.mod") {
		t.Error("should mention go.mod as a project marker")
	}
}

func TestInitializationPromptAllowsToolchain(t *testing.T) {
	// The prompt should explicitly permit the project's build toolchain.
	if !strings.Contains(InitializationPrompt, "go") &&
		!strings.Contains(InitializationPrompt, "build toolchain") {
		t.Error("prompt should instruct the agent to allow the project's build toolchain")
	}
}

func TestGeneratePromptUnsupportedHarness(t *testing.T) {
	_, err := GeneratePrompt("unsupported", "")
	if err == nil {
		t.Fatal("expected error for unsupported harness")
	}
}

func TestRefineContext(t *testing.T) {
	current := "You are VibeCop, guardian of this project."
	activity := `{"tool":"Bash","verdict":"approve"}
{"tool":"Read","verdict":"approve"}`

	ctx := RefineContext(current, activity)
	if !strings.Contains(ctx, current) {
		t.Error("refine context should include current prompt")
	}
	if !strings.Contains(ctx, "Recent activity") {
		t.Error("refine context should include activity section")
	}
	if !strings.Contains(ctx, "approve") {
		t.Error("refine context should include activity data")
	}
}

func TestRefineContextNoActivity(t *testing.T) {
	ctx := RefineContext("You are VibeCop.", "")
	if !strings.Contains(ctx, "no recent activity") {
		t.Error("should indicate no activity when empty")
	}
}

func TestInitializationPromptStartsWithCorrectDirective(t *testing.T) {
	lines := strings.Split(InitializationPrompt, "\n")
	if len(lines) > 0 {
		first := strings.TrimSpace(lines[0])
		if !strings.HasPrefix(first, "You are generating") {
			t.Errorf("expected first line to start with 'You are generating', got %q", first)
		}
	}
}

func TestRefineContextFormat(t *testing.T) {
	// Verify the refine context structure has the right sections.
	ctx := RefineContext("prompt text", "activity data")
	sections := []string{
		"Current system prompt:",
		"prompt text",
		"Recent activity",
		"activity data",
		"improve the system prompt",
	}
	for _, s := range sections {
		if !strings.Contains(ctx, s) {
			t.Errorf("refine context should contain %q", s)
		}
	}
}
