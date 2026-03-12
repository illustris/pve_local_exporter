package logging

import (
	"context"
	"log/slog"
)

// LevelTrace is a custom log level below Debug for very detailed diagnostic output.
const LevelTrace = slog.Level(-8)

// Trace logs a message at TRACE level using the default logger.
func Trace(msg string, args ...any) {
	slog.Default().Log(context.Background(), LevelTrace, msg, args...)
}
