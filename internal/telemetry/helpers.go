package telemetry

import (
	"context"

	"go.opentelemetry.io/otel/trace"
)

type otelSpanInfo struct {
	spanID  trace.SpanID
	traceID trace.TraceID
}

func GetOtelSpanInfoFromContext(ctx context.Context) otelSpanInfo {
	s := trace.SpanFromContext(ctx)

	return otelSpanInfo{
		spanID:  s.SpanContext().SpanID(),
		traceID: s.SpanContext().TraceID(),
	}
}

func (o otelSpanInfo) SpanIDValid() bool {
	return o.spanID.IsValid()
}

func (o otelSpanInfo) SpanID() string {
	return o.spanID.String()
}

func (o otelSpanInfo) TraceID() string {
	return o.traceID.String()
}
