package space

import "strings"

func SourceUsesRouter(source string) bool {
	source = strings.TrimSpace(source)
	if source == "desktop" {
		return true
	}
	if strings.HasPrefix(source, "desktop:channel:") {
		return true
	}
	if strings.HasPrefix(source, "desktop:direct:") {
		return true
	}
	return false
}

func HasLeadingMention(text string) bool {
	s := strings.TrimSpace(text)
	if !strings.HasPrefix(s, "@") {
		return false
	}
	rest := s[1:]
	for i := 0; i < len(rest); i++ {
		c := rest[i]
		if c == ' ' || c == '\t' || c == '\n' {
			return i > 0
		}
	}
	return len(rest) > 0
}
