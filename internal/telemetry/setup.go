// Package telemetry exports vibecopd permission-check spans, verdict metrics,
// and event logs over OTLP to 0-n configured collectors.
//
// All entry points are nil-safe. A nil *Provider is the disabled state: every
// helper short-circuits, allowing callers to skip a guard before each call.
// Telemetry is fail-open — init or export failures must never block a
// permission check.
package telemetry

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/bnaylor/vibecop/internal/config"

	"go.opentelemetry.io/otel"
	otellog "go.opentelemetry.io/otel/log"
	otellogglobal "go.opentelemetry.io/otel/log/global"
	"go.opentelemetry.io/otel/metric"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

const (
	// InstrumentationName is the OTel scope for vibecopd-emitted telemetry.
	InstrumentationName = "github.com/bnaylor/vibecop"

	// shutdownTimeout caps how long Shutdown waits per provider.
	shutdownTimeout = 3 * time.Second
)

// Provider owns the SDK lifecycles for traces, metrics, and logs. A nil
// *Provider is the no-op / disabled state.
type Provider struct {
	tp      *sdktrace.TracerProvider
	mp      *sdkmetric.MeterProvider
	lp      *sdklog.LoggerProvider
	tracer  trace.Tracer
	meter   metric.Meter
	logger  otellog.Logger
	metrics *Metrics
}

// Setup initialises a Provider from the given telemetry config. Returns
// (nil, nil) when telemetry is disabled or no targets are configured. Any
// per-target init failure is logged and the target is dropped — Setup only
// returns an error when *every* configured target fails to initialise, and
// even then callers should treat the error as advisory and proceed with a
// nil provider.
func Setup(ctx context.Context, cfg config.TelemetryConfig) (*Provider, error) {
	if !cfg.Enabled || len(cfg.Targets) == 0 {
		return nil, nil
	}

	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName(serviceName(cfg)),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("telemetry resource: %w", err)
	}

	spanProcs, metricReaders, logProcs := buildPerTargetPipelines(ctx, cfg.Targets)
	if len(spanProcs) == 0 && len(metricReaders) == 0 && len(logProcs) == 0 {
		return nil, fmt.Errorf("telemetry: all %d targets failed to initialise", len(cfg.Targets))
	}

	tpOpts := []sdktrace.TracerProviderOption{sdktrace.WithResource(res)}
	for _, p := range spanProcs {
		tpOpts = append(tpOpts, sdktrace.WithSpanProcessor(p))
	}
	tp := sdktrace.NewTracerProvider(tpOpts...)
	// Globals must be set: otelhttp's transport hook reads
	// otel.GetTracerProvider() / GetMeterProvider() implicitly. Without these
	// SetX calls, evaluator HTTP spans (and any other otelhttp-instrumented
	// transport) silently fall back to the no-op provider. This makes Setup
	// non-reentrant — tests that call it must restore globals on cleanup.
	otel.SetTracerProvider(tp)

	mpOpts := []sdkmetric.Option{sdkmetric.WithResource(res)}
	for _, r := range metricReaders {
		mpOpts = append(mpOpts, sdkmetric.WithReader(r))
	}
	mp := sdkmetric.NewMeterProvider(mpOpts...)
	otel.SetMeterProvider(mp)

	lpOpts := []sdklog.LoggerProviderOption{sdklog.WithResource(res)}
	for _, p := range logProcs {
		lpOpts = append(lpOpts, sdklog.WithProcessor(p))
	}
	lp := sdklog.NewLoggerProvider(lpOpts...)
	otellogglobal.SetLoggerProvider(lp)

	p := &Provider{
		tp:     tp,
		mp:     mp,
		lp:     lp,
		tracer: tp.Tracer(InstrumentationName),
		meter:  mp.Meter(InstrumentationName),
		logger: lp.Logger(InstrumentationName),
	}
	m, err := newMetrics(p.meter)
	if err != nil {
		log.Printf("telemetry: metric instrument init: %v (continuing without metrics)", err)
	} else {
		p.metrics = m
	}
	return p, nil
}

// Shutdown flushes pending telemetry and tears down the providers. Safe to
// call on a nil receiver.
func (p *Provider) Shutdown(ctx context.Context) error {
	if p == nil {
		return nil
	}
	var firstErr error
	if p.tp != nil {
		c, cancel := context.WithTimeout(ctx, shutdownTimeout)
		if err := p.tp.Shutdown(c); err != nil && firstErr == nil {
			firstErr = err
		}
		cancel()
	}
	if p.mp != nil {
		c, cancel := context.WithTimeout(ctx, shutdownTimeout)
		if err := p.mp.Shutdown(c); err != nil && firstErr == nil {
			firstErr = err
		}
		cancel()
	}
	if p.lp != nil {
		c, cancel := context.WithTimeout(ctx, shutdownTimeout)
		if err := p.lp.Shutdown(c); err != nil && firstErr == nil {
			firstErr = err
		}
		cancel()
	}
	return firstErr
}

func serviceName(cfg config.TelemetryConfig) string {
	if cfg.ServiceName == "" {
		return config.DefaultServiceName
	}
	return cfg.ServiceName
}
