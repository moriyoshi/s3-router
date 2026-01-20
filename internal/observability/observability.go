package observability

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/mattn/go-isatty"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/exporters/stdout/stdoutmetric"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

type Metrics struct {
	RequestTotal     prometheus.CounterVec
	RequestLatency   prometheus.HistogramVec
	BackendErrors    prometheus.CounterVec
	RoutingDecisions prometheus.CounterVec
}

func NewLogger(level string) *slog.Logger {
	opts := &slog.HandlerOptions{}
	switch level {
	case "debug":
		opts.Level = slog.LevelDebug
	case "info":
		opts.Level = slog.LevelInfo
	case "warn":
		opts.Level = slog.LevelWarn
	case "error":
		opts.Level = slog.LevelError
	default:
		opts.Level = slog.LevelInfo
	}

	// Auto-detect: use text handler for terminals, JSON for piped output
	var handler slog.Handler
	if isatty.IsTerminal(os.Stdout.Fd()) {
		handler = slog.NewTextHandler(os.Stdout, opts)
	} else {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	}

	return slog.New(handler)
}

// NewLoggerWithFormat creates a logger with explicit format control
// format can be "text", "json", or "auto" (default behavior)
func NewLoggerWithFormat(level, format string) *slog.Logger {
	opts := &slog.HandlerOptions{}
	switch level {
	case "debug":
		opts.Level = slog.LevelDebug
	case "info":
		opts.Level = slog.LevelInfo
	case "warn":
		opts.Level = slog.LevelWarn
	case "error":
		opts.Level = slog.LevelError
	default:
		opts.Level = slog.LevelInfo
	}

	var handler slog.Handler
	switch format {
	case "json":
		handler = slog.NewJSONHandler(os.Stdout, opts)
	case "text":
		handler = slog.NewTextHandler(os.Stdout, opts)
	default: // "auto" or any other value
		if isatty.IsTerminal(os.Stdout.Fd()) {
			handler = slog.NewTextHandler(os.Stdout, opts)
		} else {
			handler = slog.NewJSONHandler(os.Stdout, opts)
		}
	}

	return slog.New(handler)
}

var loggerKey struct{}
var traceIDKey struct{}

func WithLogger(ctx context.Context, logger *slog.Logger) context.Context {
	return context.WithValue(ctx, &loggerKey, logger)
}

func GetLoggerFromContext(ctx context.Context) *slog.Logger {
	v := ctx.Value(&loggerKey)
	if v == nil {
		return nil
	}
	logger, _ := v.(*slog.Logger)
	return logger
}

// WithTraceContext wraps a logger to automatically inject trace context into all log entries
func WithTraceContext(ctx context.Context, logger *slog.Logger) *slog.Logger {
	if logger == nil {
		return nil
	}

	spanCtx := trace.SpanContextFromContext(ctx)
	var args []any

	// Add trace ID if available
	if spanCtx.HasTraceID() {
		args = append(args, "trace_id", spanCtx.TraceID().String())
	}

	// Add span ID if available
	if spanCtx.HasSpanID() {
		args = append(args, "span_id", spanCtx.SpanID().String())
	}

	// Add sampled flag
	if spanCtx.IsSampled() {
		args = append(args, "sampled", true)
	}

	// Return logger with arguments if any tracing context exists
	if len(args) > 0 {
		return logger.With(args...)
	}

	return logger
}

func NewMetrics() *Metrics {
	return &Metrics{
		RequestTotal: *promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "s3router_requests_total",
				Help: "Total number of requests",
			},
			[]string{"operation", "backend", "status"},
		),
		RequestLatency: *promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "s3router_request_duration_seconds",
				Help:    "Request latency in seconds",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"operation", "backend"},
		),
		BackendErrors: *promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "s3router_backend_errors_total",
				Help: "Total backend errors",
			},
			[]string{"backend", "error_type"},
		),
		RoutingDecisions: *promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "s3router_routing_decisions_total",
				Help: "Total routing decisions",
			},
			[]string{"bucket", "backend"},
		),
	}
}

// RequestLogger is middleware that logs requests with trace context
func RequestLogger(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			// Create logging response writer
			lrw := &loggingResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}

			next.ServeHTTP(lrw, r)

			duration := time.Since(start)

			// Get logger with trace context injected
			contextLogger := WithTraceContext(r.Context(), logger)
			if contextLogger == nil {
				contextLogger = logger
			}

			contextLogger.Info("request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", lrw.statusCode,
				"duration_ms", duration.Milliseconds(),
			)
		})
	}
}

type loggingResponseWriter struct {
	http.ResponseWriter
	statusCode int
	written    bool
	mu         sync.Mutex
}

func (w *loggingResponseWriter) WriteHeader(code int) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if !w.written {
		w.statusCode = code
		w.written = true
		w.ResponseWriter.WriteHeader(code)
	}
}

func (w *loggingResponseWriter) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if !w.written {
		w.statusCode = http.StatusOK
		w.written = true
	}
	return w.ResponseWriter.Write(b)
}

// TraceIDMiddleware adds trace ID to context
func TraceIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		traceID := r.Header.Get("X-Trace-ID")
		if traceID == "" {
			traceID = fmt.Sprintf("%d", time.Now().UnixNano())
		}

		ctx := context.WithValue(r.Context(), &traceIDKey, traceID)
		w.Header().Set("X-Trace-ID", traceID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// context helper
func GetTraceID(ctx context.Context) string {
	if traceID, ok := ctx.Value(&traceIDKey).(string); ok {
		return traceID
	}
	return ""
}

// OpenTelemetryProvider manages OTel trace and metric providers
type OpenTelemetryProvider struct {
	TracerProvider *sdktrace.TracerProvider
	MeterProvider  *sdkmetric.MeterProvider
	Tracer         trace.Tracer
	shutdown       []func(context.Context) error
}

func buildTracesExporter(ctx context.Context, exporterName string) (tracesExporter sdktrace.SpanExporter, err error) {
	switch exporterName {
	case "stdout", "console":
		tracesExporter, err = stdouttrace.New()
	case "otlp":
		tracesExporter, err = otlptracehttp.New(ctx)
	case "none", "":
		break
	default:
		err = fmt.Errorf("unsupported traces exporter: %s", exporterName)
	}
	return
}

func buildMetricsExporter(ctx context.Context, exporterName string) (metricsExporter sdkmetric.Exporter, err error) {
	switch exporterName {
	case "stdout", "console":
		metricsExporter, err = stdoutmetric.New()
	case "otlp":
		metricsExporter, err = otlpmetrichttp.New(ctx)
	case "none", "":
		break
	default:
		err = fmt.Errorf("unsupported metrics exporter: %s", exporterName)
	}
	return
}

// InitOpenTelemetry initializes OpenTelemetry providers
// Supports both OTLP and stdout exporters via OTEL_TRACES_EXPORTER and OTEL_METRICS_EXPORTER env vars
func InitOpenTelemetry(ctx context.Context) (*OpenTelemetryProvider, error) {
	otp := new(OpenTelemetryProvider)

	{
		tracesExporter, err := buildTracesExporter(ctx, os.Getenv("OTEL_TRACES_EXPORTER"))
		if err != nil {
			return nil, fmt.Errorf("failed to create trace exporter: %w", err)
		}
		if tracesExporter != nil {
			otp.shutdown = append(otp.shutdown, tracesExporter.Shutdown)
			otp.TracerProvider = sdktrace.NewTracerProvider(
				sdktrace.WithBatcher(tracesExporter),
			)
		} else {
			otp.TracerProvider = sdktrace.NewTracerProvider()
		}
		otp.shutdown = append(otp.shutdown, otp.TracerProvider.Shutdown)
		otel.SetTracerProvider(otp.TracerProvider)
	}

	{
		metricsExporter, err := buildMetricsExporter(ctx, os.Getenv("OTEL_METRICS_EXPORTER"))
		if err != nil {
			return nil, fmt.Errorf("failed to create metric exporter: %w", err)
		}
		if metricsExporter != nil {
			otp.shutdown = append(otp.shutdown, metricsExporter.Shutdown)
			otp.MeterProvider = sdkmetric.NewMeterProvider(sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricsExporter)))
			otp.shutdown = append(otp.shutdown, otp.MeterProvider.Shutdown)
		} else {
			otp.MeterProvider = sdkmetric.NewMeterProvider()
		}
		// Set global providers
	}
	otel.SetMeterProvider(otp.MeterProvider)

	// Set global propagators for trace context propagation
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	// Create tracer for this package
	otp.Tracer = otp.TracerProvider.Tracer("github.com/moriyoshi/s3-router")

	return otp, nil
}

// Shutdown gracefully shuts down all OTel providers
func (otp *OpenTelemetryProvider) Shutdown(ctx context.Context) error {
	var errs error
	for _, fn := range otp.shutdown {
		if err := fn(ctx); err != nil {
			errs = fmt.Errorf("%v; %w", errs, err)
		}
	}
	return errs
}

// OTelMiddleware wraps an HTTP handler with OpenTelemetry tracing
func OTelMiddleware(tracer trace.Tracer) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return otelhttp.NewHandler(next, "http", otelhttp.WithTracerProvider(otel.GetTracerProvider()))
	}
}
