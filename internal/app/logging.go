package app

import (
	"log/slog"
	"os"
	"strings"
)

// ConfigureLogging sets the default slog logger level based on the provided
// configuration value. Recognised levels are "debug", "info", "warn", and
// "error" (case-insensitive). Unrecognised values default to info.
//
// Expected:
//   - level is a string log level from the application configuration.
//
// Side effects:
//   - Replaces the default slog logger with one using the parsed level.
func ConfigureLogging(level string) {
	slogLevel := parseLogLevel(level)
	levelVar := &slog.LevelVar{}
	levelVar.Set(slogLevel)
	handler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: levelVar})
	slog.SetDefault(slog.New(handler))
}

// parseLogLevel converts a string log level to a slog.Level.
// Unrecognised values default to slog.LevelInfo.
//
// Expected:
//   - level is a case-insensitive string ("debug", "info", "warn", "error").
//
// Returns:
//   - The corresponding slog.Level value.
//
// Side effects:
//   - None.
func parseLogLevel(level string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
