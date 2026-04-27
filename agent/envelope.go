package agent

import "strings"

type promptEnvelope struct {
	parts []string
}

func (p *promptEnvelope) Add(tag, body string) {
	if b := (promptBlock{Tag: tag, Body: body}).String(); b != "" {
		p.parts = append(p.parts, b)
	}
}

func (p *promptEnvelope) AddRaw(body string) {
	body = strings.TrimSpace(body)
	if body != "" {
		p.parts = append(p.parts, body)
	}
}

func (p promptEnvelope) String() string {
	return strings.TrimSpace(strings.Join(p.parts, "\n\n"))
}

type promptSections struct {
	parts []string
}

func (p *promptSections) Add(s string) {
	s = strings.TrimSpace(s)
	if s != "" {
		p.parts = append(p.parts, s)
	}
}

func (p promptSections) String() string {
	return strings.TrimSpace(strings.Join(p.parts, "\n\n"))
}

type promptBlock struct {
	Tag  string
	Body string
}

func (b promptBlock) String() string {
	tag := strings.TrimSpace(b.Tag)
	body := strings.TrimSpace(b.Body)
	if tag == "" || body == "" {
		return ""
	}
	return "<" + tag + ">\n" + cdata(body) + "\n</" + tag + ">"
}

func cdata(s string) string {
	s = strings.ReplaceAll(s, "]]>", "]]]]><![CDATA[>")
	return "<![CDATA[\n" + escapeBlockTags(s) + "\n]]>"
}

func escapeBlockTags(s string) string {
	for _, tag := range []string{"system_prompt", "conversation_history", "user_message"} {
		s = strings.ReplaceAll(s, "<"+tag+">", "["+tag+"]")
		s = strings.ReplaceAll(s, "</"+tag+">", "]]]]><![CDATA[>/"+tag+">")
	}
	return strings.ReplaceAll(s, "</", "]]]]><![CDATA[>/")
}
