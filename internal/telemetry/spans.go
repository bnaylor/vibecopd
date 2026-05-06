package telemetry

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

var noopTracer trace.Tracer = noop.NewTracerProvider().Tracer(InstrumentationName)

// Tracer returns the provider's tracer, or a no-op tracer when p is nil.
func (p *Provider) Tracer() trace.Tracer {
	if p == nil || p.tracer == nil {
		return noopTracer
	}
	return p.tracer
}

// StartPermissionSpan opens the root span for one permission check. The caller
// MUST End() the returned span. Tool name and project hash are recorded as
// attributes; verdict, reason, and latency are recorded later via SetAttributes.
func (p *Provider) StartPermissionSpan(ctx context.Context, tool, projectHash string) (context.Context, trace.Span) {
	return p.Tracer().Start(ctx, "permission.check",
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(
			attribute.String("vibecop.tool", tool),
			attribute.String("vibecop.project_hash", projectHash),
		),
	)
}

// StartEvaluatorSpan opens the child span covering the LLM evaluator call.
// Used by the permission handler around the ec.Evaluate invocation; the
// HTTP-client span underneath is added automatically by otelhttp.
func (p *Provider) StartEvaluatorSpan(ctx context.Context, model, apiFormat string) (context.Context, trace.Span) {
	return p.Tracer().Start(ctx, "evaluator.llm_call",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("vibecop.model", model),
			attribute.String("vibecop.api_format", apiFormat),
		),
	)
}
