package telemetry

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// newProviderWithRecorders builds a Provider wired to in-memory exporters so
// tests can assert recorded spans/metrics without touching the network or
// global state.
func newProviderWithRecorders(t *testing.T) (*Provider, *tracetest.SpanRecorder, sdkmetric.Reader) {
	t.Helper()

	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))

	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

	p := &Provider{
		tp:     tp,
		mp:     mp,
		tracer: tp.Tracer(InstrumentationName),
		meter:  mp.Meter(InstrumentationName),
	}
	m, err := newMetrics(p.meter)
	if err != nil {
		t.Fatalf("newMetrics: %v", err)
	}
	p.metrics = m
	return p, sr, reader
}

// TestProviderRecordsSpanAndMetrics drives a non-nil Provider through the
// same call sequence the permission handler uses (root span → eval child →
// SetAttributes → SetStatus → RecordVerdict → RecordEvaluatorLatency) and
// asserts the trace + metric pipelines captured the right data.
func TestProviderRecordsSpanAndMetrics(t *testing.T) {
	p, sr, reader := newProviderWithRecorders(t)

	ctx, root := p.StartPermissionSpan(context.Background(), "Bash", "abcd1234")
	_, eval := p.StartEvaluatorSpan(ctx, "test-model", "anthropic")
	eval.End()
	root.SetAttributes(
		attribute.String("vibecop.verdict", "deny"),
		attribute.Int64("vibecop.latency_ms", 42),
	)
	root.SetStatus(codes.Error, "blocked: rm -rf")
	p.RecordVerdict(ctx, "deny", "Bash")
	p.RecordEvaluatorLatency(ctx, 42, "deny")
	root.End()

	// --- spans ---
	spans := sr.Ended()
	if got := len(spans); got != 2 {
		t.Fatalf("recorded spans: got %d, want 2 (root + evaluator)", got)
	}
	var rootSpan, evalSpan sdktrace.ReadOnlySpan
	for _, s := range spans {
		switch s.Name() {
		case "permission.check":
			rootSpan = s
		case "evaluator.llm_call":
			evalSpan = s
		}
	}
	if rootSpan == nil {
		t.Fatal("permission.check span not recorded")
	}
	if evalSpan == nil {
		t.Fatal("evaluator.llm_call span not recorded")
	}
	if got := rootSpan.Status().Code; got != codes.Error {
		t.Errorf("root span status code: got %v, want Error", got)
	}
	if got := attrString(rootSpan, "vibecop.tool"); got != "Bash" {
		t.Errorf("root vibecop.tool: got %q, want Bash", got)
	}
	if got := attrString(rootSpan, "vibecop.verdict"); got != "deny" {
		t.Errorf("root vibecop.verdict: got %q, want deny", got)
	}
	if rootSpan.Parent().IsValid() {
		t.Error("root span unexpectedly has a parent")
	}
	if !evalSpan.Parent().IsValid() {
		t.Error("evaluator span has no parent — context not propagated")
	}

	// --- metrics ---
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("metric collect: %v", err)
	}
	verdictsCount := findCounterValue(t, rm, "vibecop.verdicts_total")
	if verdictsCount != 1 {
		t.Errorf("verdicts_total: got %d, want 1", verdictsCount)
	}
	if !findHistogramHasRecord(rm, "vibecop.evaluator_latency_ms") {
		t.Error("evaluator_latency_ms: no record found")
	}
}

func attrString(s sdktrace.ReadOnlySpan, key string) string {
	for _, a := range s.Attributes() {
		if string(a.Key) == key {
			return a.Value.AsString()
		}
	}
	return ""
}

func findCounterValue(t *testing.T, rm metricdata.ResourceMetrics, name string) int64 {
	t.Helper()
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				t.Fatalf("metric %s: unexpected data type %T", name, m.Data)
			}
			var total int64
			for _, dp := range sum.DataPoints {
				total += dp.Value
			}
			return total
		}
	}
	t.Fatalf("metric %s not found", name)
	return 0
}

func findHistogramHasRecord(rm metricdata.ResourceMetrics, name string) bool {
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			h, ok := m.Data.(metricdata.Histogram[int64])
			if !ok {
				return false
			}
			for _, dp := range h.DataPoints {
				if dp.Count > 0 {
					return true
				}
			}
		}
	}
	return false
}
