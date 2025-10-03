package telemetry

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	metricSdk "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
	"go.uber.org/zap"
)

// Provider encapsulates telemetry exporters that require shutdown.
type Provider struct {
	tracerProvider *trace.TracerProvider
	meterProvider  *metricSdk.MeterProvider
	metricsHandler http.Handler
}

// Setup initializes tracing and metrics exporters for the service.
func Setup(logger *zap.Logger, serviceName, environment string, enableMetrics bool) (*Provider, error) {
	exporter, err := stdouttrace.New(stdouttrace.WithPrettyPrint())
	if err != nil {
		return nil, fmt.Errorf("create trace exporter: %w", err)
	}

	tp := trace.NewTracerProvider(
		trace.WithBatcher(exporter),
		trace.WithResource(resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName(serviceName),
			semconv.DeploymentEnvironment(environment),
		)),
	)
	otel.SetTracerProvider(tp)

	var (
		meterProvider  *metricSdk.MeterProvider
		metricsHandler http.Handler
	)

	if enableMetrics {
		promExporter, err := prometheus.New()
		if err != nil {
			return nil, fmt.Errorf("create prometheus exporter: %w", err)
		}
		meterProvider = metricSdk.NewMeterProvider(metricSdk.WithReader(promExporter))
		otel.SetMeterProvider(meterProvider)
		metricsHandler = promhttp.Handler()
		logger.Info("prometheus metrics enabled")
	} else {
		logger.Info("prometheus metrics disabled")
	}

	return &Provider{
		tracerProvider: tp,
		meterProvider:  meterProvider,
		metricsHandler: metricsHandler,
	}, nil
}

// MetricsHandler returns the HTTP handler for scraping metrics.
func (p *Provider) MetricsHandler() http.Handler {
	if p == nil {
		return nil
	}
	return p.metricsHandler
}

// Shutdown flushes exporters and releases telemetry resources.
func (p *Provider) Shutdown(ctx context.Context) error {
	if p == nil {
		return nil
	}
	var errs error
	if p.meterProvider != nil {
		if err := p.meterProvider.Shutdown(ctx); err != nil {
			errs = errors.Join(errs, err)
		}
	}
	if p.tracerProvider != nil {
		if err := p.tracerProvider.Shutdown(ctx); err != nil {
			errs = errors.Join(errs, err)
		}
	}
	return errs
}
