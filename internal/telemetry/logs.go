package telemetry

import (
	"context"
	"sync"
	"time"

	"github.com/bnaylor/vibecop/internal/daemon"

	otellog "go.opentelemetry.io/otel/log"
)

// SubscribeEvents starts a goroutine that drains daemon events from ch and
// emits each as an OTel log record. Returns immediately; the goroutine exits
// when ch is closed or ctx is cancelled.
//
// Safe on a nil receiver: the channel is still drained so the daemon's
// fan-out doesn't back up, but no log records are emitted.
func (p *Provider) SubscribeEvents(ctx context.Context, ch <-chan daemon.Event) *sync.WaitGroup {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case evt, ok := <-ch:
				if !ok {
					return
				}
				p.emitEvent(ctx, evt)
			}
		}
	}()
	return &wg
}

func (p *Provider) emitEvent(ctx context.Context, evt daemon.Event) {
	if p == nil || p.logger == nil {
		return
	}
	rec := EventToLogRecord(evt)
	p.logger.Emit(ctx, rec)
}

// EventToLogRecord converts a daemon event to an OTel log record. Exported so
// tests can verify field fidelity without standing up a Provider.
func EventToLogRecord(evt daemon.Event) otellog.Record {
	var rec otellog.Record

	if ts, err := time.Parse(time.RFC3339, evt.Timestamp); err == nil {
		rec.SetTimestamp(ts)
	} else {
		rec.SetTimestamp(time.Now().UTC())
	}
	rec.SetObservedTimestamp(time.Now().UTC())

	rec.SetSeverity(severityFor(evt))
	rec.SetSeverityText(severityTextFor(evt))

	if evt.Message != "" {
		rec.SetBody(otellog.StringValue(evt.Message))
	} else if evt.Verdict != "" {
		rec.SetBody(otellog.StringValue(evt.Verdict + ": " + evt.Reason))
	}

	addStr := func(k, v string) {
		if v != "" {
			rec.AddAttributes(otellog.String(k, v))
		}
	}
	addStr("vibecop.tool", evt.Tool)
	// vibecop.input is intentionally NOT exported. Tool inputs are bash
	// command lines, file paths, etc. — they routinely contain secrets
	// (API keys passed as CLI flags, env-var values, file contents echoed
	// via Bash). The audit log keeps inputs locally under user control;
	// OTLP targets are external collectors with a different trust boundary.
	// Operators who need input visibility for incident triage should use
	// the local audit log (~/.vibecop/audit/<project>/).
	addStr("vibecop.verdict", evt.Verdict)
	addStr("vibecop.reason", evt.Reason)
	addStr("vibecop.harness", evt.Harness)
	addStr("vibecop.hook_event", evt.HookEvent)
	if evt.LatencyMs != 0 {
		rec.AddAttributes(otellog.Int64("vibecop.latency_ms", evt.LatencyMs))
	}

	return rec
}

func severityFor(evt daemon.Event) otellog.Severity {
	switch evt.Level {
	case "error":
		return otellog.SeverityError
	case "warn":
		return otellog.SeverityWarn
	case "info":
		return otellog.SeverityInfo
	}
	switch evt.Verdict {
	case "deny":
		return otellog.SeverityError
	case "escalate", "error":
		return otellog.SeverityWarn
	default:
		return otellog.SeverityInfo
	}
}

func severityTextFor(evt daemon.Event) string {
	switch severityFor(evt) {
	case otellog.SeverityError:
		return "ERROR"
	case otellog.SeverityWarn:
		return "WARN"
	default:
		return "INFO"
	}
}
