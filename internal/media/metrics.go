package media

import (
	"context"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

type workerMetrics struct {
	cycleTotal        metric.Int64Counter
	streamerFailures  metric.Int64Counter
	stageResultsTotal metric.Int64Counter
	stageLatencyMS    metric.Float64Histogram
	stageTokensTotal  metric.Int64Counter
	chunkLagSeconds   metric.Float64Histogram
}

func newWorkerMetrics() *workerMetrics {
	meter := otel.Meter("github.com/funpot/funpot-go-core/internal/media")

	cycleTotal, _ := meter.Int64Counter(
		"funpot_stream_processing_cycles_total",
		metric.WithDescription("Total number of streamer processing cycles by result."),
	)
	streamerFailures, _ := meter.Int64Counter(
		"funpot_stream_streamer_failures_total",
		metric.WithDescription("Total number of streamer processing failures grouped by streamer and operation."),
	)
	stageResultsTotal, _ := meter.Int64Counter(
		"funpot_stream_stage_results_total",
		metric.WithDescription("Total number of processed LLM stage results by stage and normalized outcome."),
	)
	stageLatencyMS, _ := meter.Float64Histogram(
		"funpot_stream_stage_latency_ms",
		metric.WithDescription("Observed latency for LLM stage classification in milliseconds."),
	)
	stageTokensTotal, _ := meter.Int64Counter(
		"funpot_stream_stage_tokens_total",
		metric.WithDescription("Total token usage emitted by LLM stage processing."),
	)
	chunkLagSeconds, _ := meter.Float64Histogram(
		"funpot_stream_chunk_lag_seconds",
		metric.WithDescription("Delay between chunk capture time and decision persistence in seconds."),
	)

	return &workerMetrics{
		cycleTotal:        cycleTotal,
		streamerFailures:  streamerFailures,
		stageResultsTotal: stageResultsTotal,
		stageLatencyMS:    stageLatencyMS,
		stageTokensTotal:  stageTokensTotal,
		chunkLagSeconds:   chunkLagSeconds,
	}
}

func (m *workerMetrics) recordCycle(ctx context.Context, streamerID, result string) {
	if m == nil {
		return
	}
	m.cycleTotal.Add(ctx, 1, metric.WithAttributes(
		attribute.String("streamer_id", streamerID),
		attribute.String("result", result),
	))
}

func (m *workerMetrics) recordFailure(ctx context.Context, streamerID, operation string) {
	if m == nil {
		return
	}
	m.streamerFailures.Add(ctx, 1, metric.WithAttributes(
		attribute.String("streamer_id", streamerID),
		attribute.String("operation", operation),
	))
}

func (m *workerMetrics) recordStageResult(ctx context.Context, stage, label string, latency time.Duration, tokensIn, tokensOut int) {
	if m == nil {
		return
	}
	m.stageResultsTotal.Add(ctx, 1, metric.WithAttributes(
		attribute.String("stage", stage),
		attribute.String("label", label),
	))
	m.stageLatencyMS.Record(ctx, float64(latency.Milliseconds()), metric.WithAttributes(
		attribute.String("stage", stage),
	))
	m.stageTokensTotal.Add(ctx, int64(tokensIn), metric.WithAttributes(
		attribute.String("stage", stage),
		attribute.String("direction", "in"),
	))
	m.stageTokensTotal.Add(ctx, int64(tokensOut), metric.WithAttributes(
		attribute.String("stage", stage),
		attribute.String("direction", "out"),
	))
}

func (m *workerMetrics) recordChunkLag(ctx context.Context, stage string, capturedAt time.Time, now time.Time) {
	if m == nil || capturedAt.IsZero() {
		return
	}
	lag := now.Sub(capturedAt)
	if lag < 0 {
		lag = 0
	}
	m.chunkLagSeconds.Record(ctx, lag.Seconds(), metric.WithAttributes(
		attribute.String("stage", stage),
	))
}
