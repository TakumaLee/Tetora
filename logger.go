package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// --- Log Levels ---

type LogLevel int

const (
	LevelDebug LogLevel = iota
	LevelInfo
	LevelWarn
	LevelError
)

func (l LogLevel) String() string {
	switch l {
	case LevelDebug:
		return "DEBUG"
	case LevelInfo:
		return "INFO"
	case LevelWarn:
		return "WARN"
	case LevelError:
		return "ERROR"
	default:
		return "INFO"
	}
}

func parseLevel(s string) LogLevel {
	switch strings.ToLower(s) {
	case "debug":
		return LevelDebug
	case "info":
		return LevelInfo
	case "warn", "warning":
		return LevelWarn
	case "error":
		return LevelError
	default:
		return LevelInfo
	}
}

// --- Log Format ---

type LogFormat int

const (
	FormatText LogFormat = iota
	FormatJSON
)

func parseFormat(s string) LogFormat {
	switch strings.ToLower(s) {
	case "json":
		return FormatJSON
	default:
		return FormatText
	}
}

// --- Logger ---

// Logger is a structured logger with level filtering, file output, and rotation.
type Logger struct {
	mu       sync.Mutex
	level    LogLevel
	format   LogFormat
	out      io.Writer
	file     *os.File
	filePath string
	maxSize  int64 // bytes
	maxFiles int
	curSize  int64
}

// Global logger instance.
var defaultLogger *Logger

// newLogger creates a Logger writing to the given writer.
func newLogger(level LogLevel, format LogFormat, out io.Writer) *Logger {
	return &Logger{
		level:    level,
		format:   format,
		out:      out,
		maxSize:  50 * 1024 * 1024, // 50MB default
		maxFiles: 5,
	}
}

// initLogger creates the global logger from config.
func initLogger(cfg LoggingConfig, baseDir string) *Logger {
	level := parseLevel(cfg.levelOrDefault())
	format := parseFormat(cfg.formatOrDefault())
	l := newLogger(level, format, os.Stderr)

	// Set up file output if configured.
	logFile := cfg.File
	if logFile == "" {
		logFile = filepath.Join(baseDir, "logs", "tetora.log")
	}
	maxSize := int64(cfg.maxSizeMBOrDefault()) * 1024 * 1024
	maxFiles := cfg.maxFilesOrDefault()
	l.maxSize = maxSize
	l.maxFiles = maxFiles
	l.setupFile(logFile)

	return l
}

// setupFile opens the log file for writing, creating directories as needed.
func (l *Logger) setupFile(filePath string) {
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "logger: cannot create log dir %s: %v\n", dir, err)
		return
	}
	f, err := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "logger: cannot open log file %s: %v\n", filePath, err)
		return
	}
	info, err := f.Stat()
	if err == nil {
		l.curSize = info.Size()
	}
	l.file = f
	l.filePath = filePath
	l.out = f
}

// log is the core logging method.
func (l *Logger) log(level LogLevel, traceID, msg string, fields ...any) {
	if level < l.level {
		return
	}

	ts := time.Now().UTC().Format(time.RFC3339)
	fieldMap := buildFieldMap(fields)

	var line string
	if l.format == FormatJSON {
		line = formatJSON(ts, level.String(), traceID, msg, fieldMap)
	} else {
		line = formatText(ts, level.String(), traceID, msg, fieldMap)
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	n, _ := io.WriteString(l.out, line)
	l.curSize += int64(n)

	// Check rotation.
	if l.file != nil && l.maxSize > 0 && l.curSize >= l.maxSize {
		l.rotate()
	}
}

// buildFieldMap converts variadic key-value pairs to a map.
func buildFieldMap(fields []any) map[string]any {
	if len(fields) == 0 {
		return nil
	}
	m := make(map[string]any, len(fields)/2)
	for i := 0; i+1 < len(fields); i += 2 {
		key, ok := fields[i].(string)
		if !ok {
			key = fmt.Sprintf("%v", fields[i])
		}
		m[key] = fields[i+1]
	}
	// Handle odd number of fields (last value without key).
	if len(fields)%2 != 0 {
		m["_extra"] = fields[len(fields)-1]
	}
	return m
}

// formatJSON renders a log entry as a single-line JSON string.
func formatJSON(ts, level, traceID, msg string, fields map[string]any) string {
	entry := make(map[string]any, 5)
	entry["ts"] = ts
	entry["level"] = level
	if traceID != "" {
		entry["traceId"] = traceID
	}
	entry["msg"] = msg
	if len(fields) > 0 {
		entry["fields"] = fields
	}
	b, err := json.Marshal(entry)
	if err != nil {
		return fmt.Sprintf(`{"ts":%q,"level":%q,"msg":%q,"error":"marshal failed"}`, ts, level, msg) + "\n"
	}
	return string(b) + "\n"
}

// formatText renders a log entry in human-readable text.
// Format: 2026-02-22T10:30:00Z INFO [http-a1b2c3] server started addr=:7777
func formatText(ts, level, traceID, msg string, fields map[string]any) string {
	var sb strings.Builder
	sb.WriteString(ts)
	sb.WriteByte(' ')
	// Pad level to 5 chars for alignment.
	sb.WriteString(level)
	for i := len(level); i < 5; i++ {
		sb.WriteByte(' ')
	}
	sb.WriteByte(' ')
	if traceID != "" {
		sb.WriteByte('[')
		sb.WriteString(traceID)
		sb.WriteString("] ")
	}
	sb.WriteString(msg)
	for k, v := range fields {
		sb.WriteByte(' ')
		sb.WriteString(k)
		sb.WriteByte('=')
		sb.WriteString(fmt.Sprintf("%v", v))
	}
	sb.WriteByte('\n')
	return sb.String()
}

// rotate performs log file rotation: app.log → app.log.1 → app.log.2 ...
func (l *Logger) rotate() {
	if l.file == nil || l.filePath == "" {
		return
	}
	l.file.Close()

	// Shift existing rotated files.
	for i := l.maxFiles - 1; i >= 1; i-- {
		src := l.filePath + fmt.Sprintf(".%d", i)
		dst := l.filePath + fmt.Sprintf(".%d", i+1)
		os.Rename(src, dst)
	}
	// Remove the oldest if it exceeds maxFiles.
	os.Remove(l.filePath + fmt.Sprintf(".%d", l.maxFiles))

	// Rename current → .1
	os.Rename(l.filePath, l.filePath+".1")

	// Open fresh file.
	f, err := os.OpenFile(l.filePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		l.file = nil
		l.out = os.Stderr
		return
	}
	l.file = f
	l.out = f
	l.curSize = 0
}

// Close closes the log file.
func (l *Logger) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		l.file.Close()
		l.file = nil
	}
}

// --- Level convenience methods ---

func (l *Logger) Debug(msg string, fields ...any) { l.log(LevelDebug, "", msg, fields...) }
func (l *Logger) Info(msg string, fields ...any)  { l.log(LevelInfo, "", msg, fields...) }
func (l *Logger) Warn(msg string, fields ...any)  { l.log(LevelWarn, "", msg, fields...) }
func (l *Logger) Error(msg string, fields ...any) { l.log(LevelError, "", msg, fields...) }

// --- Context-aware methods (extract trace ID from context) ---

func (l *Logger) DebugCtx(ctx context.Context, msg string, fields ...any) {
	l.log(LevelDebug, traceIDFromContext(ctx), msg, fields...)
}
func (l *Logger) InfoCtx(ctx context.Context, msg string, fields ...any) {
	l.log(LevelInfo, traceIDFromContext(ctx), msg, fields...)
}
func (l *Logger) WarnCtx(ctx context.Context, msg string, fields ...any) {
	l.log(LevelWarn, traceIDFromContext(ctx), msg, fields...)
}
func (l *Logger) ErrorCtx(ctx context.Context, msg string, fields ...any) {
	l.log(LevelError, traceIDFromContext(ctx), msg, fields...)
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
