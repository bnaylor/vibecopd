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

func TestServiceNameFallback(t *testing.T) {
	if got := serviceName(config.TelemetryConfig{}); got != config.DefaultServiceName {
		t.Errorf("empty service_name fallback: got %q, want %q", got, config.DefaultServiceName)
	}
	if got := serviceName(config.TelemetryConfig{ServiceName: "custom"}); got != "custom" {
		t.Errorf("explicit service_name: got %q", got)
	}
}
