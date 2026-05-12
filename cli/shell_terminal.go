package cli

import (
	"regexp"

	"github.com/abcdlsj/sumi/textutil"
)

var terminalInputNoise = []*regexp.Regexp{
	regexp.MustCompile(`(?:\x1b\]|\x9d|\])(?:10|11|12);(?:rgb:)?[0-9A-Fa-f]{1,4}/[0-9A-Fa-f]{1,4}/[0-9A-Fa-f]{1,4}(?:\x07|\x1b\\|\x9c|\\)?`),
	regexp.MustCompile(`(?:\x1b\[|\x9b|\[)\d{1,3};\d{1,3}R`),
	regexp.MustCompile(`(?:\x1b\[|\x9b|\[)<\d{1,4};\d{1,4};\d{1,4}[mM]`),
}

func cleanTerminalInput(s string) string {
	s = textutil.Valid(s)
	for _, re := range terminalInputNoise {
		s = re.ReplaceAllString(s, "")
	}
	return s
}

func (m *shellModel) cleanInput() {
	if m == nil {
		return
	}
	v := m.input.Value()
	if s := cleanTerminalInput(v); s != v {
		m.input.SetValue(s)
	}
}
