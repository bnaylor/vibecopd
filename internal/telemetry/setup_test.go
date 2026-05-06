package telemetry

import (
	"context"
	"testing"

	"github.com/bnaylor/vibecop/internal/config"
)

func TestSetupDisabledReturnsNil(t *testing.T) {
	cfg := config.TelemetryConfig{Enabled: false}
	p, err := Setup(context.Background(), cfg)
	if err != nil {
		t.Fatalf("disabled setup should not error: %v", err)
	}
	if p != nil {
		t.Fatal("disabled setup should return nil provider")
	}
}

func TestSetupNoTargetsReturnsNil(t *testing.T) {
	cfg := config.TelemetryConfig{Enabled: true, ServiceName: "test"}
	p, err := Setup(context.Background(), cfg)
	if err != nil {
		t.Fatalf("no-targets setup should not error: %v", err)
	}
	if p != nil {
		t.Fatal("no-targets setup should return nil provider")
	}
}

func TestNilProviderIsNoOp(t *testing.T) {
	var p *Provider
	ctx, span := p.StartPermissionSpan(context.Background(), "Bash", "abcd")
	span.End()
	if ctx == nil {
		t.Error("StartPermissionSpan returned nil context")
	}
	ctx2, span2 := p.StartEvaluatorSpan(ctx, "model", "anthropic")
	span2.End()
	if ctx2 == nil {
		t.Error("StartEvaluatorSpan returned nil context")
	}
	p.RecordVerdict(ctx, "approve", "Bash")
	p.RecordEvaluatorLatency(ctx, 42, "approve")
	if err := p.Shutdown(context.Background()); err != nil {
		t.Errorf("nil shutdown should not error: %v", err)
	}
}

// TestPartialTargetFailure exercises the multi-target sibling-isolation
// guarantee: a target with an unsupported protocol is skipped entirely while
// a sibling target's pipelines are still constructed. This is the only
// deterministically-failing branch in buildPerTargetPipelines — exporter
// constructors defer their network connection, so they don't fail on bogus
// endpoints in unit tests.
func TestPartialTargetFailure(t *testing.T) {
	targets := []config.TelemetryTarget{
		{Endpoint: "bad.example:9999", Protocol: "carrier-pigeon", Insecure: true},
		{Endpoint: "localhost:4318", Protocol: "http", Insecure: true},
	}
	spans, readers, logs := buildPerTargetPipelines(context.Background(), targets)
	if got := len(spans); got != 1 {
		t.Errorf("span processors: got %d, want 1 (bad target should be dropped, sibling kept)", got)
	}
	if got := len(readers); got != 1 {
		t.Errorf("metric readers: got %d, want 1", got)
	}
	if got := len(logs); got != 1 {
		t.Errorf("log processors: got %d, want 1", got)
	}
}

func TestServiceNameFallback(t *testing.T) {
	if got := serviceName(config.TelemetryConfig{}); got != config.DefaultServiceName {
		t.Errorf("empty service_name fallback: got %q, want %q", got, config.DefaultServiceName)
	}
	if got := serviceName(config.TelemetryConfig{ServiceName: "custom"}); got != "custom" {
		t.Errorf("explicit service_name: got %q", got)
	}
}
