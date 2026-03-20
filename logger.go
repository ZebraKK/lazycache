package lazycache

import (
	"context"
	"fmt"
	"log"
	"strings"
)

// Logger is the interface callers implement to receive log events from the cache.
type Logger interface {
	Debug(msg string, args ...any)
	Warn(msg string, args ...any)
	Error(msg string, args ...any)
}

type contextKey struct{}

// NewContext returns a child context carrying the given logger.
func NewContext(ctx context.Context, l Logger) context.Context {
	return context.WithValue(ctx, contextKey{}, l)
}

// fromContext extracts the logger from ctx; falls back to noopLogger.
func fromContext(ctx context.Context) Logger {
	if l, ok := ctx.Value(contextKey{}).(Logger); ok && l != nil {
		return l
	}
	return noopLogger{}
}

// noopLogger discards all log messages (zero overhead default).
type noopLogger struct{}

func (noopLogger) Debug(msg string, args ...any) {}
func (noopLogger) Warn(msg string, args ...any)  {}
func (noopLogger) Error(msg string, args ...any) {}

// stdLogger wraps the standard library log package.
type stdLogger struct {
	prefix string
}

// StdLogger returns a Logger backed by the standard library log package.
// The prefix is prepended to every log line (e.g. "[cache] ").
func StdLogger(prefix string) Logger {
	return &stdLogger{prefix: prefix}
}

func (l *stdLogger) Debug(msg string, args ...any) {
	log.Print(l.prefix + "DEBUG " + msg + formatArgs(args))
}

func (l *stdLogger) Warn(msg string, args ...any) {
	log.Print(l.prefix + "WARN " + msg + formatArgs(args))
}

func (l *stdLogger) Error(msg string, args ...any) {
	log.Print(l.prefix + "ERROR " + msg + formatArgs(args))
}

// formatArgs formats key-value pairs as " k1=v1 k2=v2 ...".
func formatArgs(args []any) string {
	if len(args) == 0 {
		return ""
	}
	parts := make([]string, 0, len(args)/2)
	for i := 0; i+1 < len(args); i += 2 {
		parts = append(parts, fmt.Sprintf("%v=%v", args[i], args[i+1]))
	}
	return " " + strings.Join(parts, " ")
}
