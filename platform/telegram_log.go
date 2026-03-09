package platform

import "github.com/abcdlsj/mink/internal/logx"

func (t *Telegram) debugf(format string, args ...any) {
	if t != nil && t.logger != nil {
		t.logger.Debugf(format, args...)
	}
}

func (t *Telegram) infof(format string, args ...any) {
	if t != nil && t.logger != nil {
		t.logger.Infof(format, args...)
	}
}

func (t *Telegram) warnf(format string, args ...any) {
	if t != nil && t.logger != nil {
		t.logger.Warnf(format, args...)
	}
}

func (t *Telegram) errorf(format string, args ...any) {
	if t != nil && t.logger != nil {
		t.logger.Errorf(format, args...)
	}
}

func newTelegramLogger() *logx.Logger {
	return logx.New("telegram")
}
