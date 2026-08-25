// Package logger provides structured logging for WUT
package logger

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/charmbracelet/log"
)

var (
	// globalLogger is the global logger instance
	globalLogger *Logger
	// once ensures the logger is initialized only once
	once sync.Once
)

// Level represents logging level
type Level int

const (
	DebugLevel Level = iota
	InfoLevel
	WarnLevel
	ErrorLevel
	FatalLevel
)

// Logger wraps charmbracelet/log with additional functionality
type Logger struct {
	logger *log.Logger
	level  Level
	writer io.Writer
}

// Config holds logger configuration
type Config struct {
	Level      string
	File       string
	MaxSize    int  // MB
	MaxBackups int  // number of backups
	MaxAge     int  // days
	Console    bool // output to console
}

// DefaultConfig returns default logger configuration
func DefaultConfig() Config {
	return Config{
		Level:      "info",
		File:       "",
		MaxSize:    10,
		MaxBackups: 5,
		MaxAge:     30,
		Console:    true,
	}
}

// Initialize initializes the global logger
func Initialize(cfg Config) error {
	var initErr error
	once.Do(func() {
		initErr = initLogger(cfg)
	})
	return initErr
}

// initLogger creates and configures the logger
func initLogger(cfg Config) error {
	level := parseLevel(cfg.Level)

	var writers []io.Writer

	// Console output
	if cfg.Console {
		writers = append(writers, os.Stdout)
	}

	// File output
	if cfg.File != "" {
		// Ensure log directory exists
		dir := filepath.Dir(cfg.File)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create log directory: %w", err)
		}

		fileWriter, err := newRotatingWriter(cfg)
		if err != nil {
			return fmt.Errorf("failed to create log file: %w", err)
		}
		writers = append(writers, fileWriter)
	}

	var writer io.Writer
	if len(writers) == 1 {
		writer = writers[0]
	} else {
		writer = io.MultiWriter(writers...)
	}

	l := log.New(writer)
	l.SetLevel(log.Level(level))
	l.SetTimeFormat(time.RFC3339)
	l.SetReportTimestamp(true)

	globalLogger = &Logger{
		logger: l,
		level:  level,
		writer: writer,
	}

	return nil
}

// Get returns the global logger instance
func Get() *Logger {
	if globalLogger == nil {
		// Initialize with defaults if not initialized
		_ = Initialize(DefaultConfig())
	}
	return globalLogger
}

// Debug logs debug message
func (l *Logger) Debug(msg string, keyvals ...any) {
	l.logger.Debug(msg, keyvals...)
}

// Info logs info message
func (l *Logger) Info(msg string, keyvals ...any) {
	l.logger.Info(msg, keyvals...)
}

// Warn logs warning message
func (l *Logger) Warn(msg string, keyvals ...any) {
	l.logger.Warn(msg, keyvals...)
}

// Error logs error message
func (l *Logger) Error(msg string, keyvals ...any) {
	l.logger.Error(msg, keyvals...)
}

// Fatal logs fatal message and exits
func (l *Logger) Fatal(msg string, keyvals ...any) {
	l.logger.Fatal(msg, keyvals...)
}

// With returns logger with prefix
func (l *Logger) With(prefix string) *Logger {
	return &Logger{
		logger: l.logger.WithPrefix(prefix),
		level:  l.level,
		writer: l.writer,
	}
}

// SetLevel sets logging level
func (l *Logger) SetLevel(level Level) {
	l.level = level
	l.logger.SetLevel(log.Level(level))
}

// Sync flushes the log buffer
func (l *Logger) Sync() error {
	// No-op for charmbracelet/log
	return nil
}

// Convenience functions for global logger

// Debug logs debug message using global logger
func Debug(msg string, keyvals ...any) {
	Get().Debug(msg, keyvals...)
}

// Info logs info message using global logger
func Info(msg string, keyvals ...any) {
	Get().Info(msg, keyvals...)
}

// Warn logs warning message using global logger
func Warn(msg string, keyvals ...any) {
	Get().Warn(msg, keyvals...)
}

// Error logs error message using global logger
func Error(msg string, keyvals ...any) {
	Get().Error(msg, keyvals...)
}

// Fatal logs fatal message using global logger
func Fatal(msg string, keyvals ...any) {
	Get().Fatal(msg, keyvals...)
}

// With returns global logger with prefix
func With(prefix string) *Logger {
	return Get().With(prefix)
}

// parseLevel parses level string to Level
func parseLevel(level string) Level {
	switch level {
	case "debug":
		return DebugLevel
	case "info":
		return InfoLevel
	case "warn", "warning":
		return WarnLevel
	case "error":
		return ErrorLevel
	case "fatal":
		return FatalLevel
	default:
		return InfoLevel
	}
}

// rotatingWriter handles log rotation.
//
// The global logger is written from several goroutines at once (the signal
// handler, the sync worker pool, and the suggestion engine's parallel sources),
// so every access to file/size is guarded. Without the mutex a Write racing a
// rotate could write to an already-closed handle.
type rotatingWriter struct {
	mu         sync.Mutex
	filename   string
	maxSize    int
	maxBackups int
	maxAge     int
	file       *os.File
	size       int64
}

// newRotatingWriter creates a new rotating file writer
func newRotatingWriter(cfg Config) (*rotatingWriter, error) {
	rw := &rotatingWriter{
		filename:   cfg.File,
		maxSize:    cfg.MaxSize,
		maxBackups: cfg.MaxBackups,
		maxAge:     cfg.MaxAge,
	}

	if err := rw.open(); err != nil {
		return nil, err
	}

	return rw, nil
}

// open opens or creates the log file
func (rw *rotatingWriter) open() error {
	info, err := os.Stat(rw.filename)
	if err == nil {
		rw.size = info.Size()
	}

	file, err := os.OpenFile(rw.filename, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}

	rw.file = file
	return nil
}

// Write implements io.Writer
func (rw *rotatingWriter) Write(p []byte) (n int, err error) {
	rw.mu.Lock()
	defer rw.mu.Unlock()

	// Check if rotation is needed
	if rw.size+int64(len(p)) > int64(rw.maxSize*1024*1024) {
		if err := rw.rotate(); err != nil {
			return 0, err
		}
	}

	n, err = rw.file.Write(p)
	rw.size += int64(n)
	return n, err
}

// rotate rotates the log file. The caller must hold rw.mu.
func (rw *rotatingWriter) rotate() error {
	if rw.file != nil {
		rw.file.Close()
	}

	// Remove oldest backup if exists
	oldest := rw.filename + fmt.Sprintf(".%d", rw.maxBackups)
	os.Remove(oldest)

	// Shift backups
	for i := rw.maxBackups - 1; i > 0; i-- {
		old := rw.filename + fmt.Sprintf(".%d", i)
		new := rw.filename + fmt.Sprintf(".%d", i+1)
		_ = os.Rename(old, new)
	}

	// Rename current file
	_ = os.Rename(rw.filename, rw.filename+".1")

	// The rotated-away file is gone, so the byte counter restarts. Without this
	// the counter keeps growing and every subsequent Write triggers a rotate.
	rw.size = 0

	return rw.open()
}

// Close closes the file
func (rw *rotatingWriter) Close() error {
	rw.mu.Lock()
	defer rw.mu.Unlock()

	if rw.file != nil {
		return rw.file.Close()
	}
	return nil
}
