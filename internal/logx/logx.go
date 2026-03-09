package logx

import (
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
)

type Level int

const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
)

var (
	levelOnce    sync.Once
	currentLevel = LevelInfo
)

func parseLevel(raw string) Level {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "debug":
		return LevelDebug
	case "warn", "warning":
		return LevelWarn
	case "error":
		return LevelError
	default:
		return LevelInfo
	}
}

func configuredLevel() Level {
	levelOnce.Do(func() {
		currentLevel = parseLevel(os.Getenv("MINK_LOG_LEVEL"))
	})
	return currentLevel
}

type Logger struct {
	component string
}

func New(component string) *Logger {
	return &Logger{component: strings.TrimSpace(component)}
}

func (l *Logger) enabled(level Level) bool {
	return level >= configuredLevel()
}

func (l *Logger) prefix(level Level) string {
	name := strings.ToUpper(strings.TrimSpace(l.component))
	if name == "" {
		name = "MINK"
	}
	return fmt.Sprintf("[%s][%s] ", name, level.String())
}

func (l *Logger) logf(level Level, format string, args ...any) {
	if !l.enabled(level) {
		return
	}
	log.Printf("%s%s", l.prefix(level), fmt.Sprintf(format, args...))
}

func (l *Logger) Debugf(format string, args ...any) { l.logf(LevelDebug, format, args...) }
func (l *Logger) Infof(format string, args ...any)  { l.logf(LevelInfo, format, args...) }
func (l *Logger) Warnf(format string, args ...any)  { l.logf(LevelWarn, format, args...) }
func (l *Logger) Errorf(format string, args ...any) { l.logf(LevelError, format, args...) }

func (l Level) String() string {
	switch l {
	case LevelDebug:
		return "debug"
	case LevelWarn:
		return "warn"
	case LevelError:
		return "error"
	default:
		return "info"
	}
}
