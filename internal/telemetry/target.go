package telemetry

import (
	"context"
	"log"
	"strings"

	"github.com/bnaylor/vibecop/internal/config"

	otlploggrpc "go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	otlploghttp "go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	otlpmetricgrpc "go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	otlpmetrichttp "go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	otlptracegrpc "go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	otlptracehttp "go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// buildPerTargetPipelines constructs span processors, metric readers, and log
// processors for each configured target. A target whose exporter init fails is
// logged and skipped — its absence does not abort sibling targets.
func buildPerTargetPipelines(
	ctx context.Context,
	targets []config.TelemetryTarget,
) (
	[]sdktrace.SpanProcessor,
	[]sdkmetric.Reader,
	[]sdklog.Processor,
) {
	var (
		spans   []sdktrace.SpanProcessor
		readers []sdkmetric.Reader
		logs    []sdklog.Processor
	)

	for i, t := range targets {
		proto := strings.ToLower(t.Protocol)
		if proto != "grpc" && proto != "http" {
			log.Printf("telemetry: target[%d] %q: unknown protocol %q (want grpc|http) — skipping", i, t.Endpoint, t.Protocol)
			continue
		}

		if se, err := newSpanExporter(ctx, t, proto); err != nil {
			log.Printf("telemetry: target[%d] %q: span exporter: %v — skipping spans", i, t.Endpoint, err)
		} else {
			spans = append(spans, sdktrace.NewBatchSpanProcessor(se))
		}

		if me, err := newMetricExporter(ctx, t, proto); err != nil {
			log.Printf("telemetry: target[%d] %q: metric exporter: %v — skipping metrics", i, t.Endpoint, err)
		} else {
			readers = append(readers, sdkmetric.NewPeriodicReader(me))
		}

		if le, err := newLogExporter(ctx, t, proto); err != nil {
			log.Printf("telemetry: target[%d] %q: log exporter: %v — skipping logs", i, t.Endpoint, err)
		} else {
			logs = append(logs, sdklog.NewBatchProcessor(le))
		}
	}

	return spans, readers, logs
}

func newSpanExporter(ctx context.Context, t config.TelemetryTarget, proto string) (sdktrace.SpanExporter, error) {
	if proto == "grpc" {
		opts := []otlptracegrpc.Option{otlptracegrpc.WithEndpoint(t.Endpoint)}
		if t.Insecure {
			opts = append(opts, otlptracegrpc.WithInsecure())
		}
		return otlptracegrpc.New(ctx, opts...)
	}
	opts := []otlptracehttp.Option{otlptracehttp.WithEndpoint(t.Endpoint)}
	if t.Insecure {
		opts = append(opts, otlptracehttp.WithInsecure())
	}
	return otlptracehttp.New(ctx, opts...)
}

func newMetricExporter(ctx context.Context, t config.TelemetryTarget, proto string) (sdkmetric.Exporter, error) {
	if proto == "grpc" {
		opts := []otlpmetricgrpc.Option{otlpmetricgrpc.WithEndpoint(t.Endpoint)}
		if t.Insecure {
			opts = append(opts, otlpmetricgrpc.WithInsecure())
		}
		return otlpmetricgrpc.New(ctx, opts...)
	}
	opts := []otlpmetrichttp.Option{otlpmetrichttp.WithEndpoint(t.Endpoint)}
	if t.Insecure {
		opts = append(opts, otlpmetrichttp.WithInsecure())
	}
	return otlpmetrichttp.New(ctx, opts...)
}

func newLogExporter(ctx context.Context, t config.TelemetryTarget, proto string) (sdklog.Exporter, error) {
	if proto == "grpc" {
		opts := []otlploggrpc.Option{otlploggrpc.WithEndpoint(t.Endpoint)}
		if t.Insecure {
			opts = append(opts, otlploggrpc.WithInsecure())
		}
		return otlploggrpc.New(ctx, opts...)
	}
	opts := []otlploghttp.Option{otlploghttp.WithEndpoint(t.Endpoint)}
	if t.Insecure {
		opts = append(opts, otlploghttp.WithInsecure())
	}
	return otlploghttp.New(ctx, opts...)
}
