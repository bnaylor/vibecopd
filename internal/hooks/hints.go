package hooks

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// HintCooldown is how long to wait between emissions of the same
// (harness, event, verdict) hint. Exposed as a var (not a const) so tests
// can shrink it.
var HintCooldown = 1 * time.Hour

// hintsDirFn is the function that resolves the marker directory. Override
// in tests via the package-private variable to redirect filesystem state.
var hintsDirFn = defaultHintsDir

func defaultHintsDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".vibecop", "hints")
}

// HarnessHint returns a one-line operator-facing advisory for combos
// where vibecop's verdict has limited effect on the harness. Empty
// string means no hint applies.
//
// Today only (copilot, preToolUse, approve) qualifies: Copilot CLI does
// not honor `permissionDecision: "allow"` — the harness's normal
// permission flow runs anyway. Users wanting "skip the prompt for
// approved tools" need Copilot's `/allow-all on` mode, which still
// respects vibecop's deny.
func HarnessHint(harness, event, verdict string) string {
	if harness == HarnessCopilot && verdict == "approve" {
		return `VibeCop: Copilot does not currently honor permissionDecision="allow"; this approve is a no-op. Run ` + "`/allow-all on`" + ` inside Copilot for harness-side auto-approval — vibecop deny still blocks.`
	}
	return ""
}

// emitHintOnce writes msg to stderr when no marker exists for this
// (harness, event, verdict) within HintCooldown. Touches the marker on
// every emission so subsequent invocations within the cooldown stay
// silent. If the marker dir can't be resolved or written we still emit
// the hint — preferring noise over swallowing the message — because the
// hint is operator-actionable.
func emitHintOnce(harness, event, verdict string, stderr io.Writer) {
	msg := HarnessHint(harness, event, verdict)
	if msg == "" {
		return
	}
	dir := hintsDirFn()
	if dir == "" {
		fmt.Fprintln(stderr, msg)
		return
	}
	marker := filepath.Join(dir, harness+"-"+event+"-"+verdict)
	if info, err := os.Stat(marker); err == nil && time.Since(info.ModTime()) < HintCooldown {
		return
	}
	if err := os.MkdirAll(dir, 0755); err == nil {
		_ = os.WriteFile(marker, nil, 0644)
	}
	fmt.Fprintln(stderr, msg)
}
