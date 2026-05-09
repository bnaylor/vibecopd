package telemetry

import (
	"testing"
	"time"

	"github.com/bnaylor/vibecop/internal/daemon"

	otellog "go.opentelemetry.io/otel/log"
)

func TestEventToLogRecordVerdict(t *testing.T) {
	evt := daemon.Event{
		Tool:      "Bash",
		Input:     "swift build",
		Verdict:   "approve",
		Reason:    "Routine build",
		LatencyMs: 312,
		Timestamp: "2026-05-06T10:00:00Z",
	}
	rec := EventToLogRecord(evt)

	if sev := rec.Severity(); sev != otellog.SeverityInfo {
		t.Errorf("approve should be INFO, got %v", sev)
	}
	wantTS, _ := time.Parse(time.RFC3339, evt.Timestamp)
	if ts := rec.Timestamp(); !ts.Equal(wantTS) {
		t.Errorf("timestamp: got %v, want %v", ts, wantTS)
	}

	got := collectAttrs(rec)
	if got["vibecop.tool"] != "Bash" {
		t.Errorf("vibecop.tool: %q", got["vibecop.tool"])
	}
	if got["vibecop.verdict"] != "approve" {
		t.Errorf("vibecop.verdict: %q", got["vibecop.verdict"])
	}
	if got["vibecop.latency_ms"] != int64(312) {
		t.Errorf("vibecop.latency_ms: %v", got["vibecop.latency_ms"])
	}
	if _, present := got["vibecop.input"]; present {
		t.Errorf("vibecop.input must not be exported to OTLP — tool inputs may contain secrets; got %q", got["vibecop.input"])
	}
}

func TestEventToLogRecordHarnessAndHookEvent(t *testing.T) {
	evt := daemon.Event{
		Verdict:   "deny",
		Reason:    "blocked",
		Harness:   "claude",
		HookEvent: "PreToolUse",
	}
	rec := EventToLogRecord(evt)
	got := collectAttrs(rec)

	if got["vibecop.harness"] != "claude" {
		t.Errorf("vibecop.harness: got %q, want %q", got["vibecop.harness"], "claude")
	}
	if got["vibecop.hook_event"] != "PreToolUse" {
		t.Errorf("vibecop.hook_event: got %q, want %q", got["vibecop.hook_event"], "PreToolUse")
	}
}

func TestEventToLogRecordEmptyHarnessDropped(t *testing.T) {
	evt := daemon.Event{Verdict: "approve"}
	rec := EventToLogRecord(evt)
	got := collectAttrs(rec)
	if _, present := got["vibecop.harness"]; present {
		t.Errorf("vibecop.harness must be dropped when empty, got %q", got["vibecop.harness"])
	}
	if _, present := got["vibecop.hook_event"]; present {
		t.Errorf("vibecop.hook_event must be dropped when empty, got %q", got["vibecop.hook_event"])
	}
}

func TestEventToLogRecordSeverity(t *testing.T) {
	cases := []struct {
		name string
		evt  daemon.Event
		want otellog.Severity
	}{
		{"deny verdict", daemon.Event{Verdict: "deny"}, otellog.SeverityError},
		{"escalate verdict", daemon.Event{Verdict: "escalate"}, otellog.SeverityWarn},
		{"error verdict", daemon.Event{Verdict: "error"}, otellog.SeverityWarn},
		{"approve verdict", daemon.Event{Verdict: "approve"}, otellog.SeverityInfo},
		{"explicit error level", daemon.Event{Level: "error", Message: "oops"}, otellog.SeverityError},
		{"explicit warn level", daemon.Event{Level: "warn", Message: "suspended"}, otellog.SeverityWarn},
		{"explicit info level overrides deny", daemon.Event{Level: "info", Verdict: "deny"}, otellog.SeverityInfo},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := EventToLogRecord(c.evt)
			if got := rec.Severity(); got != c.want {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestEventToLogRecordMessageBody(t *testing.T) {
	evt := daemon.Event{Level: "error", Message: "VibeCop suspended"}
	rec := EventToLogRecord(evt)
	body := rec.Body()
	if got := body.AsString(); got != "VibeCop suspended" {
		t.Errorf("body: got %q, want %q", got, "VibeCop suspended")
	}
}

func collectAttrs(rec otellog.Record) map[string]any {
	out := map[string]any{}
	rec.WalkAttributes(func(kv otellog.KeyValue) bool {
		val := kv.Value
		switch val.Kind() {
		case otellog.KindString:
			out[kv.Key] = val.AsString()
		case otellog.KindInt64:
			out[kv.Key] = val.AsInt64()
		}
		return true
	})
	return out
}
