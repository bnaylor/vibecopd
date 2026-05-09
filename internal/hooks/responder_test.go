package hooks

import (
	"bytes"
	"strings"
	"testing"

	"github.com/bnaylor/vibecop/internal/daemon"
)

// writeVerdictCase is one row in the (harness, event, verdict) → response
// table. Empty wantStdout means "no JSON expected on stdout"; wantStderr is
// a substring match (or empty for "no stderr expected").
type writeVerdictCase struct {
	name       string
	harness    string
	event      string
	verdict    daemon.Verdict
	wantStdout string
	wantStderr string
	wantExit   int
}

func TestWriteVerdict(t *testing.T) {
	// Redirect hint markers to a per-test temp dir so the (copilot, approve)
	// row gets a deterministic stderr — fresh dir means the marker doesn't
	// exist, hint always emits.
	t.Cleanup(swapHintsDir(t.TempDir()))

	cases := []writeVerdictCase{
		// --- Claude PreToolUse ---
		{
			name:    "claude PreToolUse approve",
			harness: HarnessClaude, event: EventPreToolUse,
			verdict:    daemon.Verdict{Verdict: "approve", Reason: "routine build"},
			wantStdout: `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow","permissionDecisionReason":"routine build"}}`,
			wantExit:   0,
		},
		{
			name:    "claude PreToolUse approve no reason",
			harness: HarnessClaude, event: EventPreToolUse,
			verdict:    daemon.Verdict{Verdict: "approve"},
			wantStdout: `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow"}}`,
		},
		{
			name:    "claude PreToolUse deny",
			harness: HarnessClaude, event: EventPreToolUse,
			verdict:    daemon.Verdict{Verdict: "deny", Reason: "rm -rf root"},
			wantStdout: `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"rm -rf root"}}`,
			wantStderr: "VibeCop [DENY]: rm -rf root\n",
		},
		{
			name:    "claude PreToolUse escalate",
			harness: HarnessClaude, event: EventPreToolUse,
			verdict:    daemon.Verdict{Verdict: "escalate", Reason: "uncertain"},
			wantStdout: "",
			wantStderr: "VibeCop [ESCALATE]: uncertain\n",
		},
		{
			name:    "claude empty event defaults to PreToolUse",
			harness: HarnessClaude, event: "",
			verdict:    daemon.Verdict{Verdict: "approve", Reason: "ok"},
			wantStdout: `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow","permissionDecisionReason":"ok"}}`,
		},

		// --- Codex PreToolUse (cannot allow; only deny emits JSON) ---
		{
			name:    "codex PreToolUse approve emits no JSON",
			harness: HarnessCodex, event: EventPreToolUse,
			verdict:    daemon.Verdict{Verdict: "approve", Reason: "routine"},
			wantStdout: "",
		},
		{
			name:    "codex PreToolUse deny",
			harness: HarnessCodex, event: EventPreToolUse,
			verdict:    daemon.Verdict{Verdict: "deny", Reason: "danger"},
			wantStdout: `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"danger"}}`,
			wantStderr: "VibeCop [DENY]: danger\n",
		},
		{
			name:    "codex PreToolUse escalate",
			harness: HarnessCodex, event: EventPreToolUse,
			verdict:    daemon.Verdict{Verdict: "escalate", Reason: "uncertain"},
			wantStderr: "VibeCop [ESCALATE]: uncertain\n",
		},

		// --- Codex PermissionRequest ---
		{
			name:    "codex PermissionRequest approve",
			harness: HarnessCodex, event: EventPermissionRequest,
			verdict:    daemon.Verdict{Verdict: "approve"},
			wantStdout: `{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"allow"}}}`,
		},
		{
			name:    "codex PermissionRequest deny",
			harness: HarnessCodex, event: EventPermissionRequest,
			verdict:    daemon.Verdict{Verdict: "deny", Reason: "policy violation"},
			wantStdout: `{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"deny","message":"policy violation"}}}`,
			wantStderr: "VibeCop [DENY]: policy violation\n",
		},
		{
			name:    "codex PermissionRequest escalate",
			harness: HarnessCodex, event: EventPermissionRequest,
			verdict: daemon.Verdict{Verdict: "escalate"},
		},

		// --- Gemini BeforeTool ---
		{
			name:    "gemini BeforeTool approve",
			harness: HarnessGemini, event: EventBeforeTool,
			verdict:    daemon.Verdict{Verdict: "approve", Reason: "ok"},
			wantStdout: `{"decision":"allow","reason":"ok"}`,
		},
		{
			name:    "gemini BeforeTool approve no reason",
			harness: HarnessGemini, event: EventBeforeTool,
			verdict:    daemon.Verdict{Verdict: "approve"},
			wantStdout: `{"decision":"allow"}`,
		},
		{
			name:    "gemini BeforeTool deny",
			harness: HarnessGemini, event: EventBeforeTool,
			verdict:    daemon.Verdict{Verdict: "deny", Reason: "blocked"},
			wantStdout: `{"decision":"deny","reason":"blocked"}`,
			wantStderr: "VibeCop [DENY]: blocked\n",
		},
		{
			name:    "gemini empty event defaults",
			harness: HarnessGemini, event: "",
			verdict:    daemon.Verdict{Verdict: "approve"},
			wantStdout: `{"decision":"allow"}`,
		},

		// --- Copilot preToolUse ---
		{
			name:    "copilot preToolUse approve emits hint",
			harness: HarnessCopilot, event: EventCopilotPreToolUse,
			verdict: daemon.Verdict{Verdict: "approve", Reason: "ignored on allow"},
			// Documented Copilot allow form omits reason. We additionally
			// emit a stderr hint pointing the user at /allow-all on, since
			// Copilot does not currently honor permissionDecision="allow".
			wantStdout: `{"permissionDecision":"allow"}`,
			wantStderr: "/allow-all on",
		},
		{
			name:    "copilot preToolUse deny",
			harness: HarnessCopilot, event: EventCopilotPreToolUse,
			verdict:    daemon.Verdict{Verdict: "deny", Reason: "no"},
			wantStdout: `{"permissionDecision":"deny","permissionDecisionReason":"no"}`,
			wantStderr: "VibeCop [DENY]: no\n",
		},
		{
			name:    "copilot empty event defaults",
			harness: HarnessCopilot, event: "",
			verdict:    daemon.Verdict{Verdict: "deny", Reason: "x"},
			wantStdout: `{"permissionDecision":"deny","permissionDecisionReason":"x"}`,
			wantStderr: "VibeCop [DENY]: x\n",
		},

		// --- Fall-open cases ---
		{
			name:    "unknown harness falls open",
			harness: "deepseek", event: "anything",
			verdict:    daemon.Verdict{Verdict: "approve", Reason: "ok"},
			wantStdout: "",
			wantStderr: "VibeCop: unrecognized harness=\"deepseek\" event=\"anything\", falling open\n",
		},
		{
			name:    "claude with unknown event falls open",
			harness: HarnessClaude, event: "PostToolUse",
			verdict:    daemon.Verdict{Verdict: "approve", Reason: "ok"},
			wantStderr: "VibeCop: unrecognized harness=\"claude\" event=\"PostToolUse\", falling open\n",
		},
		{
			name:    "deny with unknown harness/event also falls open without DENY stderr",
			harness: "deepseek", event: "x",
			verdict:    daemon.Verdict{Verdict: "deny", Reason: "should-not-leak"},
			wantStderr: "VibeCop: unrecognized harness=\"deepseek\" event=\"x\", falling open\n",
		},
		{
			name:    "unknown verdict falls open",
			harness: HarnessClaude, event: EventPreToolUse,
			verdict:    daemon.Verdict{Verdict: "fizz", Reason: "irrelevant"},
			wantStderr: "VibeCop: unknown verdict=\"fizz\", falling open\n",
		},
		{
			name:    "escalate with empty reason emits no stderr",
			harness: HarnessClaude, event: EventPreToolUse,
			verdict: daemon.Verdict{Verdict: "escalate"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			got := WriteVerdict(tc.harness, tc.event, tc.verdict, &stdout, &stderr)
			if got != tc.wantExit {
				t.Errorf("exit = %d, want %d", got, tc.wantExit)
			}
			if stdout.String() != tc.wantStdout {
				t.Errorf("stdout = %q\n  want %q", stdout.String(), tc.wantStdout)
			}
			if tc.wantStderr == "" {
				if stderr.Len() != 0 {
					t.Errorf("stderr = %q, want empty", stderr.String())
				}
			} else if !strings.Contains(stderr.String(), tc.wantStderr) {
				t.Errorf("stderr = %q\n  want substring %q", stderr.String(), tc.wantStderr)
			}
		})
	}
}

// Regression test for review finding C-H1: an escalate verdict on an
// unrecognized (harness, event) combo must NOT leak the daemon's reason
// to stderr — the reason text can quote agent input. Same fail-open gate
// as the deny path.
func TestWriteVerdictEscalateDoesNotLeakReasonOnUnknownCombo(t *testing.T) {
	t.Cleanup(swapHintsDir(t.TempDir()))
	var stdout, stderr bytes.Buffer
	WriteVerdict("nope", "nope", daemon.Verdict{Verdict: "escalate", Reason: "secret-from-agent"}, &stdout, &stderr)
	if strings.Contains(stderr.String(), "[ESCALATE]") {
		t.Errorf("stderr leaked ESCALATE line on unknown combo: %q", stderr.String())
	}
	if strings.Contains(stderr.String(), "secret-from-agent") {
		t.Errorf("reason text leaked on unknown-combo escalate: %q", stderr.String())
	}
}

func TestWriteVerdictDenyDoesNotLeakDenyStderrOnUnknownCombo(t *testing.T) {
	// Regression: an unknown (harness, event) for a deny must NOT also emit
	// the "VibeCop [DENY]:" line — the verdict was real but we couldn't
	// shape the response, so we fall fully open.
	var stdout, stderr bytes.Buffer
	WriteVerdict("nope", "nope", daemon.Verdict{Verdict: "deny", Reason: "secret"}, &stdout, &stderr)
	if strings.Contains(stderr.String(), "[DENY]") {
		t.Errorf("stderr leaked DENY line on unknown combo: %q", stderr.String())
	}
}
