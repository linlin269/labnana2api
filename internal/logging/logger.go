package logging

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type Logger struct {
	mu      sync.Mutex
	level   int
	backend *log.Logger
}

const (
	levelDebug = iota
	levelInfo
	levelWarn
	levelError
)

func NewFromConfig(level, filePath string) (*Logger, error) {
	writer := io.Writer(os.Stdout)
	if strings.TrimSpace(filePath) != "" {
		if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
			return nil, err
		}
		file, err := os.OpenFile(filePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			return nil, err
		}
		writer = io.MultiWriter(os.Stdout, file)
	}

	return &Logger{
		level:   parseLevel(level),
		backend: log.New(writer, "", log.LstdFlags|log.Lmicroseconds),
	}, nil
}

func (l *Logger) Debug(format string, args ...interface{}) {
	l.logf(levelDebug, "DEBUG", format, args...)
}

func (l *Logger) Info(format string, args ...interface{}) {
	l.logf(levelInfo, "INFO", format, args...)
}

func (l *Logger) Warn(format string, args ...interface{}) {
	l.logf(levelWarn, "WARN", format, args...)
}

func (l *Logger) Error(format string, args ...interface{}) {
	l.logf(levelError, "ERROR", format, args...)
}

func (l *Logger) logf(level int, prefix, format string, args ...interface{}) {
	if l == nil || level < l.level {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.backend.Printf("[%s] %s", prefix, fmt.Sprintf(format, args...))
}

func parseLevel(level string) int {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return levelDebug
	case "warn", "warning":
		return levelWarn
	case "error":
		return levelError
	default:
		return levelInfo
	}
}
