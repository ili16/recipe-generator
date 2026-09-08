// Package telemetry emits OpenTelemetry metrics, traces, and logs so recipe
// generation/refinement volume, request/DB/LLM call latency, and application logs can
// all be graphed and searched in Grafana (Cloud or local) later. Every recording
// function and the package-level Tracer are safe to use even if Init was never invoked
// (or failed to find an endpoint) — they fall back to OTel's global no-op providers, so
// nothing here changes request behavior.
package telemetry

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/metric"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// Tracer is used for manual spans around expensive/external calls (LLM requests, etc.)
// that don't get a span for free from otelhttp/otelpgx. Safe to use pre-Init (no-op
// tracer until a real TracerProvider is installed).
var Tracer = otel.Tracer("recipe-generator")

// StartSpan is a small convenience wrapper around Tracer.Start for call sites that just
// want a named span around a block of code.
func StartSpan(ctx context.Context, name string) (context.Context, trace.Span) {
	return Tracer.Start(ctx, name)
}

// meter and instruments are bound at package-init time against whatever MeterProvider is
// current (initially OTel's global no-op). otel.SetMeterProvider (called from Init, if at
// all) upgrades them in place — no need to recreate instruments after configuring the
// real exporter.
var meter = otel.Meter("recipe-generator")

var (
	generateCount          = mustCounter("recipe.generate.count", "recipe generation calls")
	generateDuration       = mustHistogram("recipe.generate.duration_ms", "ms", "recipe generation latency")
	refineCount            = mustCounter("recipe.refine.count", "recipe refinement calls")
	refineDuration         = mustHistogram("recipe.refine.duration_ms", "ms", "recipe refinement latency")
	saveCount              = mustCounter("recipe.save.count", "recipe save calls")
	saveIterations         = mustHistogram("recipe.save.iterations", "1", "chat edits applied before a recipe was saved")
	generationOutcomeCount = mustCounter("recipe.generation_outcome.count", "generation funnel terminal outcome (saved/refined/declined/abandoned)")
	llmTokenUsage          = mustCounter("llm.tokens", "LLM tokens consumed, by prompt/completion")
	llmCallDuration        = mustHistogram("llm.call.duration_ms", "ms", "LLM call latency by model/call_type")
	judgeDecisions         = mustCounter("judge.decision.count", "cooking-relevance judge allow/deny decisions")
)

func mustCounter(name, desc string) metric.Int64Counter {
	c, err := meter.Int64Counter(name, metric.WithDescription(desc))
	if err != nil {
		slog.Warn("telemetry: failed to create counter", "name", name, "error", err)
	}
	return c
}

func mustHistogram(name, unit, desc string) metric.Int64Histogram {
	h, err := meter.Int64Histogram(name, metric.WithDescription(desc), metric.WithUnit(unit))
	if err != nil {
		slog.Warn("telemetry: failed to create histogram", "name", name, "error", err)
	}
	return h
}

// Init points global metrics, traces, and logs providers at OTLP/HTTP exporters,
// configured entirely via the standard OTEL_EXPORTER_OTLP_* env vars (endpoint, headers,
// protocol) — the same vars work whether the target is Grafana Cloud's OTLP endpoint or
// a local OTel Collector. All three signals stay no-op unless OTEL_ENABLED=true.
//
// Once logs are enabled, slog's default logger is replaced with one that ships every
// existing slog.* call site to the configured OTLP endpoint (e.g. Loki via the
// collector). Call sites using slog.*Context(ctx, ...) get their log lines correlated
// with the active trace/span; plain slog.Info/Debug/Error calls (no context) still ship,
// just without trace correlation.
func Init(ctx context.Context) (shutdown func(context.Context) error, err error) {
	if os.Getenv("OTEL_ENABLED") != "true" {
		slog.Info("telemetry: OTEL_ENABLED not true, telemetry disabled")
		return func(context.Context) error { return nil }, nil
	}
	if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") == "" {
		return nil, fmt.Errorf("telemetry: OTEL_ENABLED=true but OTEL_EXPORTER_OTLP_ENDPOINT is unset")
	}

	serviceName := os.Getenv("OTEL_SERVICE_NAME")
	if serviceName == "" {
		serviceName = "recipe-generator-backend"
	}
	res, err := resource.Merge(resource.Default(), resource.NewSchemaless(
		attribute.String("service.name", serviceName),
	))
	if err != nil {
		return nil, fmt.Errorf("telemetry: build resource: %w", err)
	}

	metricExporter, err := otlpmetrichttp.New(ctx)
	if err != nil {
		return nil, fmt.Errorf("telemetry: create otlp metric exporter: %w", err)
	}
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExporter)),
		sdkmetric.WithResource(res),
	)
	otel.SetMeterProvider(mp)

	traceExporter, err := otlptracehttp.New(ctx)
	if err != nil {
		return nil, fmt.Errorf("telemetry: create otlp trace exporter: %w", err)
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traceExporter),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	Tracer = tp.Tracer("recipe-generator")

	logExporter, err := otlploghttp.New(ctx)
	if err != nil {
		return nil, fmt.Errorf("telemetry: create otlp log exporter: %w", err)
	}
	lp := sdklog.NewLoggerProvider(
		sdklog.WithProcessor(sdklog.NewBatchProcessor(logExporter)),
		sdklog.WithResource(res),
	)
	otelHandler := otelslog.NewHandler("recipe-generator", otelslog.WithLoggerProvider(lp))
	// Fan out to both a stderr handler (keeps LOG_LEVEL/`docker logs` visibility, and is
	// the only place a log line survives if the process exits before the OTLP batch
	// processor's next flush) and the OTel handler (ships to Loki).
	//
	// Deliberately NOT slog.Default().Handler(): that's Go's special zero-value
	// "defaultHandler", which bridges to the stdlib `log` package's default Logger. If
	// captured and re-installed via slog.SetDefault (as part of a wrapper like this
	// fan-out), logging through it recurses back into slog's (now-changed) default and
	// self-deadlocks on log.Logger's non-reentrant mutex. A freshly constructed handler
	// has no such bridge.
	level := slog.LevelInfo
	if os.Getenv("LOG_LEVEL") == "debug" {
		level = slog.LevelDebug
	}
	stderrHandler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})
	slog.SetDefault(slog.New(fanoutHandler{handlers: []slog.Handler{stderrHandler, otelHandler}}))

	slog.Info("telemetry: OTLP metrics/traces/logs exporters configured")
	return func(shutdownCtx context.Context) error {
		return joinShutdown(shutdownCtx, mp.Shutdown, tp.Shutdown, lp.Shutdown)
	}, nil
}

// fanoutHandler dispatches each log record to every wrapped handler, so logs keep going
// to their original destination (stderr) while also shipping to OTLP.
type fanoutHandler struct {
	handlers []slog.Handler
}

func (f fanoutHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, h := range f.handlers {
		if h.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (f fanoutHandler) Handle(ctx context.Context, record slog.Record) error {
	var errs []error
	for _, h := range f.handlers {
		if h.Enabled(ctx, record.Level) {
			if err := h.Handle(ctx, record.Clone()); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

func (f fanoutHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := make([]slog.Handler, len(f.handlers))
	for i, h := range f.handlers {
		next[i] = h.WithAttrs(attrs)
	}
	return fanoutHandler{handlers: next}
}

func (f fanoutHandler) WithGroup(name string) slog.Handler {
	next := make([]slog.Handler, len(f.handlers))
	for i, h := range f.handlers {
		next[i] = h.WithGroup(name)
	}
	return fanoutHandler{handlers: next}
}

func joinShutdown(ctx context.Context, fns ...func(context.Context) error) error {
	var errs []error
	for _, fn := range fns {
		if err := fn(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("telemetry: shutdown errors: %v", errs)
}

func outcomeAttr(ok bool) string {
	if ok {
		return "success"
	}
	return "error"
}

// RecordGenerate reports one /generate call.
func RecordGenerate(ctx context.Context, sourceType string, durMs int64, ok bool) {
	generateCount.Add(ctx, 1, metric.WithAttributes(
		attribute.String("source_type", sourceType),
		attribute.String("outcome", outcomeAttr(ok)),
	))
	generateDuration.Record(ctx, durMs, metric.WithAttributes(attribute.String("source_type", sourceType)))
}

// RecordRefine reports one /refine-recipe call. status is "applied", "rejected",
// "needs_choice", or "error" (call failed before a status was produced).
func RecordRefine(ctx context.Context, durMs int64, status string) {
	refineCount.Add(ctx, 1, metric.WithAttributes(attribute.String("status", status)))
	refineDuration.Record(ctx, durMs)
}

// RecordSave reports one /add-recipe call for a structured recipe. iterations is the
// number of accepted chat edits applied before this save (0 = saved the first
// generation unedited).
func RecordSave(ctx context.Context, iterations int, ok bool) {
	if !ok {
		saveCount.Add(ctx, 1, metric.WithAttributes(attribute.String("outcome", "error")))
		return
	}
	edited := "unedited"
	if iterations > 0 {
		edited = "edited"
	}
	saveCount.Add(ctx, 1, metric.WithAttributes(
		attribute.String("outcome", "success"),
		attribute.String("edited", edited),
	))
	saveIterations.Record(ctx, int64(iterations))
}

// RecordGenerationOutcome reports n generation_events rows resolved to outcome
// ("saved"/"refined"/"declined"/"abandoned") in one call — the funnel's numerator.
// recipe.generate.count (RecordGenerate) is the denominator: conversion ratio is
// computed downstream (e.g. in PromQL) as outcome-count / generate-count.
func RecordGenerationOutcome(ctx context.Context, outcome string, n int64) {
	if n <= 0 {
		return
	}
	generationOutcomeCount.Add(ctx, n, metric.WithAttributes(attribute.String("outcome", outcome)))
}

// RecordLLMCall reports latency and outcome for one LLM call, broken out by call_type
// (generate/refine/classify/judge/suggest_input/suggest_prefs/cooking_chat) and model —
// use this to compare model swaps and spot which call type is slow or erroring.
func RecordLLMCall(ctx context.Context, callType, model string, durMs int64, ok bool) {
	llmCallDuration.Record(ctx, durMs, metric.WithAttributes(
		attribute.String("call_type", callType),
		attribute.String("model", model),
		attribute.String("outcome", outcomeAttr(ok)),
	))
}

// RecordJudgeDecision reports one isRecipeRelated verdict. hasContext distinguishes
// judging a fresh input (generate/suggest/cooking-chat, no existing recipe) from judging
// a change against an existing recipe (refine) — deny rates often differ between the two.
func RecordJudgeDecision(ctx context.Context, hasContext, allowed bool) {
	decision := "deny"
	if allowed {
		decision = "allow"
	}
	judgeDecisions.Add(ctx, 1, metric.WithAttributes(
		attribute.String("decision", decision),
		attribute.Bool("has_context", hasContext),
	))
}

// RecordLLMUsage reports token usage for one LLM call.
func RecordLLMUsage(ctx context.Context, callType, model string, promptTokens, completionTokens int64) {
	llmTokenUsage.Add(ctx, promptTokens, metric.WithAttributes(
		attribute.String("call_type", callType),
		attribute.String("model", model),
		attribute.String("kind", "prompt"),
	))
	llmTokenUsage.Add(ctx, completionTokens, metric.WithAttributes(
		attribute.String("call_type", callType),
		attribute.String("model", model),
		attribute.String("kind", "completion"),
	))
}
