package main

import (
	"context"

	"tetora/internal/log"
)

// --- Log Levels ---

// LogLevel is an alias for log.Level for backward compatibility.
type LogLevel = log.Level

const (
	LevelDebug = log.LevelDebug
	LevelInfo  = log.LevelInfo
	LevelWarn  = log.LevelWarn
	LevelError = log.LevelError
)

// --- Log Format ---

// LogFormat is an alias for log.Format for backward compatibility.
type LogFormat = log.Format

const (
	FormatText = log.FormatText
	FormatJSON = log.FormatJSON
)

// --- Logger ---

// Logger is an alias for log.Logger for backward compatibility.
type Logger = log.Logger

// Global logger instance.
var defaultLogger *Logger

// newLogger creates a Logger writing to the given writer.
func newLogger(level LogLevel, format LogFormat, out interface{ Write([]byte) (int, error) }) *Logger {
	return log.New(level, format, out)
}

// initLogger creates the global logger from config.
func initLogger(cfg LoggingConfig, baseDir string) *Logger {
	l := log.Init(log.Config{
		Level:     cfg.levelOrDefault(),
		Format:    cfg.formatOrDefault(),
		File:      cfg.File,
		MaxSizeMB: cfg.maxSizeMBOrDefault(),
		MaxFiles:  cfg.maxFilesOrDefault(),
	}, baseDir)
	l.SetTraceExtractor(traceIDFromContext)
	log.SetDefault(l)
	return l
}

// --- Package-level shortcuts (use defaultLogger) ---

func logDebug(msg string, fields ...any) {
	if defaultLogger != nil {
		defaultLogger.Debug(msg, fields...)
	}
}
func logInfo(msg string, fields ...any) {
	if defaultLogger != nil {
		defaultLogger.Info(msg, fields...)
	}
}
func logWarn(msg string, fields ...any) {
	if defaultLogger != nil {
		defaultLogger.Warn(msg, fields...)
	}
}
func logError(msg string, fields ...any) {
	if defaultLogger != nil {
		defaultLogger.Error(msg, fields...)
	}
}

func logDebugCtx(ctx context.Context, msg string, fields ...any) {
	if defaultLogger != nil {
		defaultLogger.DebugCtx(ctx, msg, fields...)
	}
}
func logInfoCtx(ctx context.Context, msg string, fields ...any) {
	if defaultLogger != nil {
		defaultLogger.InfoCtx(ctx, msg, fields...)
	}
}
func logWarnCtx(ctx context.Context, msg string, fields ...any) {
	if defaultLogger != nil {
		defaultLogger.WarnCtx(ctx, msg, fields...)
	}
}
func logErrorCtx(ctx context.Context, msg string, fields ...any) {
	if defaultLogger != nil {
		defaultLogger.ErrorCtx(ctx, msg, fields...)
	}
}
