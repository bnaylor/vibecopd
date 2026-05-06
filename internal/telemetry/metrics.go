package telemetry

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Metrics holds the named instruments emitted by the permission handler.
type Metrics struct {
	verdicts metric.Int64Counter
	latency  metric.Int64Histogram
}

func newMetrics(m metric.Meter) (*Metrics, error) {
	verdicts, err := m.Int64Counter(
		"vibecop.verdicts_total",
		metric.WithDescription("Permission-check verdicts emitted by vibecop"),
		metric.WithUnit("{verdict}"),
	)
	if err != nil {
		return nil, fmt.Errorf("verdicts counter: %w", err)
	}
	latency, err := m.Int64Histogram(
		"vibecop.evaluator_latency_ms",
		metric.WithDescription("Round-trip latency of evaluator LLM calls"),
		metric.WithUnit("ms"),
	)
	if err != nil {
		return nil, fmt.Errorf("latency histogram: %w", err)
	}
	return &Metrics{verdicts: verdicts, latency: latency}, nil
}

// RecordVerdict increments the verdict counter for one permission check.
// Safe on a nil receiver.
func (p *Provider) RecordVerdict(ctx context.Context, verdict, tool string) {
	if p == nil || p.metrics == nil {
		return
	}
	p.metrics.verdicts.Add(ctx, 1, metric.WithAttributes(
		attribute.String("vibecop.verdict", verdict),
		attribute.String("vibecop.tool", tool),
	))
}

// RecordEvaluatorLatency records the LLM round-trip latency in milliseconds.
// Safe on a nil receiver.
func (p *Provider) RecordEvaluatorLatency(ctx context.Context, latencyMs int64, verdict string) {
	if p == nil || p.metrics == nil {
		return
	}
	p.metrics.latency.Record(ctx, latencyMs, metric.WithAttributes(
		attribute.String("vibecop.verdict", verdict),
	))
}
