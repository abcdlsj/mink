package observability

import (
	"io"
	"os"
	"strings"
	"time"

	charmlog "github.com/charmbracelet/log"
)

type Logger = charmlog.Logger

type Component string

const (
	ComponentServer    Component = "server"
	ComponentComputer  Component = "computer"
	ComponentInstaller Component = "installer"
)

type Category string

const (
	CategoryLifecycle Category = "lifecycle"
	CategoryTransport Category = "transport"
	CategoryAuthority Category = "authority"
	CategoryArtifact  Category = "artifact"
	CategoryKnowledge Category = "knowledge"
	CategoryPlacement Category = "placement"
	CategoryRuntime   Category = "runtime"
	CategoryRun       Category = "run"
	CategoryEngine    Category = "engine"
	CategoryOutbox    Category = "outbox"
	CategoryInstall   Category = "install"
)

func New(component Component, writer io.Writer) *Logger {
	if writer == nil {
		writer = io.Discard
	}
	level, invalidLevel := parseLevel()
	formatter, invalidFormat := parseFormatter()
	if invalidLevel || invalidFormat {
		level = charmlog.InfoLevel
		formatter = charmlog.TextFormatter
	}
	logger := charmlog.NewWithOptions(writer, charmlog.Options{
		Level:           level,
		ReportTimestamp: true,
		TimeFunction:    charmlog.NowUTC,
		TimeFormat:      time.RFC3339Nano,
		Fields:          []any{"component", component},
		Formatter:       formatter,
	})
	if invalidLevel || invalidFormat {
		logger.Warn("invalid logging configuration; using safe defaults",
			"category", CategoryLifecycle, "event", "logging.configuration.invalid",
			"invalid_level", invalidLevel, "invalid_format", invalidFormat)
	}
	return logger
}

func parseLevel() (charmlog.Level, bool) {
	raw := strings.TrimSpace(os.Getenv("SUMI_LOG_LEVEL"))
	if raw == "" {
		return charmlog.InfoLevel, false
	}
	level, err := charmlog.ParseLevel(raw)
	if err != nil || level == charmlog.FatalLevel {
		return charmlog.InfoLevel, true
	}
	return level, false
}

func parseFormatter() (charmlog.Formatter, bool) {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("SUMI_LOG_FORMAT"))) {
	case "", "text":
		return charmlog.TextFormatter, false
	case "json":
		return charmlog.JSONFormatter, false
	case "logfmt":
		return charmlog.LogfmtFormatter, false
	default:
		return charmlog.TextFormatter, true
	}
}

func Discard(component Component) *Logger {
	return New(component, io.Discard)
}

func CategoryLogger(logger *Logger, component Component, category Category) *Logger {
	if logger == nil {
		logger = Discard(component)
	}
	return logger.With("category", category)
}
