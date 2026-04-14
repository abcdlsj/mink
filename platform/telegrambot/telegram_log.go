package telegrambot

import (
	"fmt"
	"log/slog"
)

func (t *Telegram) debugf(format string, args ...any) {
	slog.Debug(fmt.Sprintf(format, args...), "component", "telegram")
}

func (t *Telegram) infof(format string, args ...any) {
	slog.Info(fmt.Sprintf(format, args...), "component", "telegram")
}

func (t *Telegram) warnf(format string, args ...any) {
	slog.Warn(fmt.Sprintf(format, args...), "component", "telegram")
}

func (t *Telegram) errorf(format string, args ...any) {
	slog.Error(fmt.Sprintf(format, args...), "component", "telegram")
}
