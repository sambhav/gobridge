package gobridge

import (
	"context"
	"log/slog"
	"time"
)

// CallEvent contains metadata only: no arguments, results or constructor secrets.
// Err is the original Go error for trusted host-side diagnostics.
type CallEvent struct {
	Method    string
	RequestID string
	Duration  time.Duration
	Code      string
	Err       error
}
type requestIDKey struct{}

// RequestID returns the protocol correlation ID, or empty for a direct Go call.
func RequestID(ctx context.Context) string { id, _ := ctx.Value(requestIDKey{}).(string); return id }

// WithLogger emits structured completion events. Configure the handler on stderr;
// stdout belongs to the protocol. Log messages do not contain payloads or raw errors.
func WithLogger(logger *slog.Logger) Option { return func(r *Registry) { r.logger = logger } }

// WithObserver installs a concurrency-safe, nonblocking host callback for metrics
// and traces. Callback panics are isolated from application requests.
func WithObserver(observer func(context.Context, CallEvent)) Option {
	return func(r *Registry) { r.observer = observer }
}
func (r *Registry) observe(ctx context.Context, method string, started time.Time, err error) {
	if r.logger == nil && r.observer == nil {
		return
	}
	defer func() { _ = recover() }()
	event := CallEvent{Method: method, RequestID: RequestID(ctx), Duration: time.Since(started), Code: "ok", Err: err}
	if err != nil {
		event.Code = wireError(err).Code
	}
	if r.logger != nil {
		level := slog.LevelDebug
		if err != nil {
			level = slog.LevelWarn
		}
		r.logger.Log(ctx, level, "gobridge call", "method", method, "request_id", event.RequestID, "duration", event.Duration, "code", event.Code)
	}
	if r.observer != nil {
		r.observer(ctx, event)
	}
}
