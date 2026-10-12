package interceptor

import (
	"fmt"

	"connectrpc.com/connect"
	"connectrpc.com/otelconnect"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// OTel builds a Connect interceptor emitting spans and metrics via otelconnect.
func OTel(tp trace.TracerProvider, mp metric.MeterProvider) (connect.Interceptor, error) {
	var opts []otelconnect.Option
	if tp != nil {
		opts = append(opts, otelconnect.WithTracerProvider(tp))
	}
	if mp != nil {
		opts = append(opts, otelconnect.WithMeterProvider(mp))
	}
	interceptor, err := otelconnect.NewInterceptor(opts...)
	if err != nil {
		return nil, fmt.Errorf("interceptor.OTel: %w", err)
	}
	return interceptor, nil
}
