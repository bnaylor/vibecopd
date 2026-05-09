package hooks

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHarnessHintCopilotApprove(t *testing.T) {
	hint := HarnessHint(HarnessCopilot, EventCopilotPreToolUse, "approve")
	if !strings.Contains(hint, "/allow-all on") {
		t.Errorf("expected hint to mention /allow-all on, got: %s", hint)
	}
	if !strings.Contains(hint, "no-op") {
		t.Errorf("expected hint to flag no-op behavior, got: %s", hint)
	}
}

func TestHarnessHintNoneForOtherCombos(t *testing.T) {
	cases := []struct {
		harness, event, verdict string
	}{
		{HarnessClaude, EventPreToolUse, "approve"},
		{HarnessClaude, EventPreToolUse, "deny"},
		{HarnessCodex, EventPermissionRequest, "approve"},
		{HarnessGemini, EventBeforeTool, "approve"},
		{HarnessCopilot, EventCopilotPreToolUse, "deny"},
		{HarnessCopilot, EventCopilotPreToolUse, "escalate"},
	}
	for _, tc := range cases {
		if got := HarnessHint(tc.harness, tc.event, tc.verdict); got != "" {
			t.Errorf("HarnessHint(%s,%s,%s) = %q, want empty", tc.harness, tc.event, tc.verdict, got)
		}
	}
}

func TestEmitHintOnceThrottlesViaMarker(t *testing.T) {
	tmp := t.TempDir()
	t.Cleanup(swapHintsDir(tmp))

	var stderr bytes.Buffer
	emitHintOnce(HarnessCopilot, EventCopilotPreToolUse, "approve", &stderr)
	first := stderr.String()
	if first == "" {
		t.Fatal("first call should emit a hint")
	}
	if !strings.Contains(first, "/allow-all on") {
		t.Errorf("first call missing expected text: %q", first)
	}

	// Marker should exist now.
	marker := filepath.Join(tmp, "copilot-preToolUse-approve")
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("marker not created: %v", err)
	}

	stderr.Reset()
	emitHintOnce(HarnessCopilot, EventCopilotPreToolUse, "approve", &stderr)
	if stderr.Len() != 0 {
		t.Errorf("second call should be throttled; got: %q", stderr.String())
	}
}

func TestEmitHintOnceReemitsAfterCooldown(t *testing.T) {
	tmp := t.TempDir()
	t.Cleanup(swapHintsDir(tmp))

	// Pre-touch a marker with a stale mtime.
	marker := filepath.Join(tmp, "copilot-preToolUse-approve")
	if err := os.WriteFile(marker, nil, 0644); err != nil {
		t.Fatal(err)
	}
	stale := time.Now().Add(-2 * HintCooldown)
	if err := os.Chtimes(marker, stale, stale); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	emitHintOnce(HarnessCopilot, EventCopilotPreToolUse, "approve", &stderr)
	if stderr.Len() == 0 {
		t.Error("expected re-emission after cooldown elapsed")
	}
}

func TestEmitHintOnceNoOpWhenNoHintApplies(t *testing.T) {
	tmp := t.TempDir()
	t.Cleanup(swapHintsDir(tmp))

	var stderr bytes.Buffer
	emitHintOnce(HarnessClaude, EventPreToolUse, "approve", &stderr)
	if stderr.Len() != 0 {
		t.Errorf("Claude approve should produce no hint; got %q", stderr.String())
	}
}

// swapHintsDir replaces hintsDirFn for the duration of a test and returns
// a cleanup that restores the original.
func swapHintsDir(dir string) func() {
	orig := hintsDirFn
	hintsDirFn = func() string { return dir }
	return func() { hintsDirFn = orig }
}
