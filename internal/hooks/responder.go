package hooks

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/bnaylor/vibecop/internal/daemon"
)

// WriteVerdict emits the per-harness JSON response for a daemon verdict on
// stdout, writes any operator-visible stderr line, and returns the process
// exit code. Always returns 0 — the JSON is what the harness keys off, not
// the exit code, and fail-open is the contract.
//
// Unknown (harness, event) or unknown verdict → no stdout, single-line
// stderr diagnostic, exit 0.
func WriteVerdict(harness, event string, v daemon.Verdict, stdout, stderr io.Writer) int {
	switch v.Verdict {
	case "approve":
		return writeApprove(harness, event, v.Reason, stdout, stderr)
	case "deny":
		return writeDeny(harness, event, v.Reason, stdout, stderr)
	case "escalate":
		return writeEscalate(harness, event, v.Reason, stderr)
	default:
		fmt.Fprintf(stderr, "VibeCop: unknown verdict=%q, falling open\n", v.Verdict)
		return 0
	}
}

// writeEscalate emits the operator-visible `[ESCALATE]` stderr line only when
// the (harness, event) combo is one we know — same fail-open gate the deny
// path uses. Without this gate, the daemon's reason text (which can quote
// the agent's command line back at us) leaks to stderr even when we don't
// know what harness sent the request.
func writeEscalate(harness, event, reason string, stderr io.Writer) int {
	if !recognizedCombo(harness, event) {
		fmt.Fprintf(stderr, "VibeCop: unrecognized harness=%q event=%q, falling open\n", harness, event)
		return 0
	}
	if reason != "" {
		fmt.Fprintf(stderr, "VibeCop [ESCALATE]: %s\n", reason)
	}
	return 0
}

// recognizedCombo reports whether (harness, event) is one of the rows in the
// per-harness response table — i.e. one for which approve/deny/escalate has
// a defined behavior. Used to gate stderr output that contains user-visible
// reason text.
func recognizedCombo(harness, event string) bool {
	switch harness {
	case HarnessClaude:
		return event == "" || event == EventPreToolUse
	case HarnessCodex:
		return event == EventPreToolUse || event == EventPermissionRequest
	case HarnessGemini:
		return event == "" || event == EventBeforeTool
	case HarnessCopilot:
		return event == "" || event == EventCopilotPreToolUse
	}
	return false
}

func writeApprove(harness, event, reason string, stdout, stderr io.Writer) int {
	payload, known := approveJSON(harness, event, reason)
	if !known {
		fmt.Fprintf(stderr, "VibeCop: unrecognized harness=%q event=%q, falling open\n", harness, event)
		return 0
	}
	emitHintOnce(harness, event, "approve", stderr)
	if payload != nil {
		_, _ = stdout.Write(payload)
	}
	return 0
}

func writeDeny(harness, event, reason string, stdout, stderr io.Writer) int {
	payload, known := denyJSON(harness, event, reason)
	if !known {
		fmt.Fprintf(stderr, "VibeCop: unrecognized harness=%q event=%q, falling open\n", harness, event)
		return 0
	}
	fmt.Fprintf(stderr, "VibeCop [DENY]: %s\n", reason)
	if payload != nil {
		_, _ = stdout.Write(payload)
	}
	return 0
}

// approveJSON returns the bytes to write to stdout for an `approve` verdict.
// Two-value return:
//   - (data, true): write data to stdout
//   - (nil,  true): combo is recognized but no stdout is emitted (e.g. Codex
//     PreToolUse cannot allow — the harness's normal permission flow runs)
//   - (nil,  false): combo is not recognized — caller emits a diagnostic and
//     falls open
func approveJSON(harness, event, reason string) ([]byte, bool) {
	switch harness {
	case HarnessClaude:
		if event == "" || event == EventPreToolUse {
			return marshalSafe(claudePreToolDecision("allow", reason)), true
		}
	case HarnessCodex:
		switch event {
		case EventPreToolUse:
			// Codex PreToolUse cannot allow; falls through to harness's
			// normal permission flow.
			return nil, true
		case EventPermissionRequest:
			return marshalSafe(codexPermReqAllow()), true
		}
	case HarnessGemini:
		if event == "" || event == EventBeforeTool {
			return marshalSafe(geminiDecision("allow", reason)), true
		}
	case HarnessCopilot:
		if event == "" || event == EventCopilotPreToolUse {
			// Copilot's documented allow shape is the bare 1-key form.
			return marshalSafe(copilotAllow()), true
		}
	}
	return nil, false
}

func denyJSON(harness, event, reason string) ([]byte, bool) {
	switch harness {
	case HarnessClaude:
		if event == "" || event == EventPreToolUse {
			return marshalSafe(claudePreToolDecision("deny", reason)), true
		}
	case HarnessCodex:
		switch event {
		case EventPreToolUse:
			// Codex PreToolUse uses Claude's flat permissionDecision shape.
			return marshalSafe(claudePreToolDecision("deny", reason)), true
		case EventPermissionRequest:
			return marshalSafe(codexPermReqDeny(reason)), true
		}
	case HarnessGemini:
		if event == "" || event == EventBeforeTool {
			return marshalSafe(geminiDecision("deny", reason)), true
		}
	case HarnessCopilot:
		if event == "" || event == EventCopilotPreToolUse {
			return marshalSafe(copilotDeny(reason)), true
		}
	}
	return nil, false
}

// marshalSafe is json.Marshal that returns nil on encoder error so the
// caller can fall open silently. Our payload types have no fields that
// can fail to marshal in practice; this is belt-and-braces for the
// fail-open contract.
func marshalSafe(v any) []byte {
	data, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return data
}

// --- Claude / Codex PreToolUse shape ---
//
// Claude Code:  https://code.claude.com/docs/en/hooks
// Codex CLI:    https://developers.openai.com/codex/hooks

type claudePreToolHookOutput struct {
	HookSpecificOutput claudePreToolInner `json:"hookSpecificOutput"`
}

type claudePreToolInner struct {
	HookEventName            string `json:"hookEventName"`
	PermissionDecision       string `json:"permissionDecision"`
	PermissionDecisionReason string `json:"permissionDecisionReason,omitempty"`
}

func claudePreToolDecision(decision, reason string) claudePreToolHookOutput {
	return claudePreToolHookOutput{
		HookSpecificOutput: claudePreToolInner{
			HookEventName:            EventPreToolUse,
			PermissionDecision:       decision,
			PermissionDecisionReason: reason,
		},
	}
}

// --- Codex PermissionRequest shape ---
//
// https://developers.openai.com/codex/hooks

type codexPermReqOutput struct {
	HookSpecificOutput codexPermReqInner `json:"hookSpecificOutput"`
}

type codexPermReqInner struct {
	HookEventName string                  `json:"hookEventName"`
	Decision      codexPermReqDecisionVal `json:"decision"`
}

type codexPermReqDecisionVal struct {
	Behavior string `json:"behavior"`
	Message  string `json:"message,omitempty"`
}

func codexPermReqAllow() codexPermReqOutput {
	return codexPermReqOutput{
		HookSpecificOutput: codexPermReqInner{
			HookEventName: EventPermissionRequest,
			Decision:      codexPermReqDecisionVal{Behavior: "allow"},
		},
	}
}

func codexPermReqDeny(reason string) codexPermReqOutput {
	return codexPermReqOutput{
		HookSpecificOutput: codexPermReqInner{
			HookEventName: EventPermissionRequest,
			Decision:      codexPermReqDecisionVal{Behavior: "deny", Message: reason},
		},
	}
}

// --- Gemini BeforeTool shape ---
//
// https://geminicli.com/docs/hooks/reference/

type geminiOutput struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason,omitempty"`
}

func geminiDecision(decision, reason string) geminiOutput {
	return geminiOutput{Decision: decision, Reason: reason}
}

// --- Copilot preToolUse shape ---
//
// https://docs.github.com/en/copilot/reference/hooks-configuration

type copilotAllowOutput struct {
	PermissionDecision string `json:"permissionDecision"`
}

type copilotDenyOutput struct {
	PermissionDecision       string `json:"permissionDecision"`
	PermissionDecisionReason string `json:"permissionDecisionReason,omitempty"`
}

func copilotAllow() copilotAllowOutput {
	return copilotAllowOutput{PermissionDecision: "allow"}
}

func copilotDeny(reason string) copilotDenyOutput {
	return copilotDenyOutput{PermissionDecision: "deny", PermissionDecisionReason: reason}
}
