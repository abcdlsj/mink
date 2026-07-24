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
	CategoryDelivery  Category = "delivery"
	CategoryDriver    Category = "driver"
	CategoryOutbox    Category = "outbox"
	CategoryInstall   Category = "install"
)

func New(component Component, writer io.Writer) *Logger {
	if writer == nil {
		writer = io.Discard
	}
	level := charmlog.InfoLevel
	invalidLevel := false
	if configured := strings.TrimSpace(os.Getenv("SUMI_LOG_LEVEL")); configured != "" {
		parsed, err := charmlog.ParseLevel(configured)
		if err != nil || parsed == charmlog.FatalLevel {
			invalidLevel = true
		} else {
			level = parsed
		}
	}
	formatter := charmlog.TextFormatter
	invalidFormat := false
	switch strings.ToLower(strings.TrimSpace(os.Getenv("SUMI_LOG_FORMAT"))) {
	case "", "text":
	case "json":
		formatter = charmlog.JSONFormatter
	case "logfmt":
		formatter = charmlog.LogfmtFormatter
	default:
		invalidFormat = true
	}
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
		logger.Warn("invalid logging configuration; using safe defaults", "category", CategoryLifecycle, "event", "logging.configuration.invalid", "invalid_level", invalidLevel, "invalid_format", invalidFormat)
	}
	return logger
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
