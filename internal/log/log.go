// Package log provides structured logging with level filtering, file output, and rotation.
package log

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

// Level represents log severity.
type Level int

const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
)

func (l Level) String() string {
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

// ParseLevel converts a string to a Level.
func ParseLevel(s string) Level {
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

// Format represents log output format.
type Format int

const (
	FormatText Format = iota
	FormatJSON
)

// ParseFormat converts a string to a Format.
func ParseFormat(s string) Format {
	switch strings.ToLower(s) {
	case "json":
		return FormatJSON
	default:
		return FormatText
	}
}

// Config holds logger configuration.
type Config struct {
	Level    string // "debug", "info", "warn", "error" (default "info")
	Format   string // "text", "json" (default "text")
	File     string // log file path (default baseDir/logs/tetora.log)
	MaxSizeMB int  // max file size before rotation in MB (default 50)
	MaxFiles int   // rotated files to keep (default 5)
}

func (c Config) levelOrDefault() string {
	if c.Level != "" {
		return c.Level
	}
	return "info"
}

func (c Config) formatOrDefault() string {
	if c.Format != "" {
		return c.Format
	}
	return "text"
}

func (c Config) maxSizeMBOrDefault() int {
	if c.MaxSizeMB > 0 {
		return c.MaxSizeMB
	}
	return 50
}

func (c Config) maxFilesOrDefault() int {
	if c.MaxFiles > 0 {
		return c.MaxFiles
	}
	return 5
}

// TraceExtractor extracts a trace ID from a context.
// If nil, Ctx methods will use an empty trace ID.
type TraceExtractor func(context.Context) string

// Logger is a structured logger with level filtering, file output, and rotation.
type Logger struct {
	mu             sync.Mutex
	level          Level
	format         Format
	out            io.Writer
	file           *os.File
	filePath       string
	maxSize        int64 // bytes
	maxFiles       int
	curSize        int64
	traceExtractor TraceExtractor
}

// New creates a Logger writing to the given writer.
func New(level Level, format Format, out io.Writer) *Logger {
	return &Logger{
		level:    level,
		format:   format,
		out:      out,
		maxSize:  50 * 1024 * 1024, // 50MB default
		maxFiles: 5,
	}
}

// Init creates a Logger from config, setting up file output and rotation.
func Init(cfg Config, baseDir string) *Logger {
	level := ParseLevel(cfg.levelOrDefault())
	format := ParseFormat(cfg.formatOrDefault())
	l := New(level, format, os.Stderr)

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

// SetTraceExtractor sets the function used to extract trace IDs from context.
func (l *Logger) SetTraceExtractor(fn TraceExtractor) {
	l.traceExtractor = fn
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
func (l *Logger) log(level Level, traceID, msg string, fields ...any) {
	if level < l.level {
		return
	}

	ts := time.Now().UTC().Format(time.RFC3339)
	fieldMap := BuildFieldMap(fields)

	var line string
	if l.format == FormatJSON {
		line = FormatJSONLine(ts, level.String(), traceID, msg, fieldMap)
	} else {
		line = FormatTextLine(ts, level.String(), traceID, msg, fieldMap)
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

// BuildFieldMap converts variadic key-value pairs to a map.
func BuildFieldMap(fields []any) map[string]any {
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

// FormatJSONLine renders a log entry as a single-line JSON string.
func FormatJSONLine(ts, level, traceID, msg string, fields map[string]any) string {
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

// FormatTextLine renders a log entry in human-readable text.
// Format: 2026-02-22T10:30:00Z INFO [http-a1b2c3] server started addr=:7777
func FormatTextLine(ts, level, traceID, msg string, fields map[string]any) string {
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

// rotate performs log file rotation: app.log -> app.log.1 -> app.log.2 ...
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

	// Rename current -> .1
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

func (l *Logger) extractTrace(ctx context.Context) string {
	if l.traceExtractor != nil {
		return l.traceExtractor(ctx)
	}
	return ""
}

func (l *Logger) DebugCtx(ctx context.Context, msg string, fields ...any) {
	l.log(LevelDebug, l.extractTrace(ctx), msg, fields...)
}
func (l *Logger) InfoCtx(ctx context.Context, msg string, fields ...any) {
	l.log(LevelInfo, l.extractTrace(ctx), msg, fields...)
}
func (l *Logger) WarnCtx(ctx context.Context, msg string, fields ...any) {
	l.log(LevelWarn, l.extractTrace(ctx), msg, fields...)
}
func (l *Logger) ErrorCtx(ctx context.Context, msg string, fields ...any) {
	l.log(LevelError, l.extractTrace(ctx), msg, fields...)
}

// --- Package-level shortcuts (use defaultLogger) ---

var defaultLogger *Logger

// SetDefault sets the package-level default logger.
func SetDefault(l *Logger) {
	defaultLogger = l
}

// Default returns the package-level default logger.
func Default() *Logger {
	return defaultLogger
}

func Debug(msg string, fields ...any) {
	if defaultLogger != nil {
		defaultLogger.Debug(msg, fields...)
	}
}
func Info(msg string, fields ...any) {
	if defaultLogger != nil {
		defaultLogger.Info(msg, fields...)
	} else {
		fmt.Fprintf(os.Stderr, "INFO: %s %v\n", msg, fields)
	}
}
func Warn(msg string, fields ...any) {
	if defaultLogger != nil {
		defaultLogger.Warn(msg, fields...)
	} else {
		fmt.Fprintf(os.Stderr, "WARN: %s %v\n", msg, fields)
	}
}
func Error(msg string, fields ...any) {
	if defaultLogger != nil {
		defaultLogger.Error(msg, fields...)
	} else {
		fmt.Fprintf(os.Stderr, "ERROR: %s %v\n", msg, fields)
	}
}

func DebugCtx(ctx context.Context, msg string, fields ...any) {
	if defaultLogger != nil {
		defaultLogger.DebugCtx(ctx, msg, fields...)
	}
}
func InfoCtx(ctx context.Context, msg string, fields ...any) {
	if defaultLogger != nil {
		defaultLogger.InfoCtx(ctx, msg, fields...)
	}
}
func WarnCtx(ctx context.Context, msg string, fields ...any) {
	if defaultLogger != nil {
		defaultLogger.WarnCtx(ctx, msg, fields...)
	}
}
func ErrorCtx(ctx context.Context, msg string, fields ...any) {
	if defaultLogger != nil {
		defaultLogger.ErrorCtx(ctx, msg, fields...)
	}
}
