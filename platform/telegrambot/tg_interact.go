package telegrambot

import (
	"regexp"
	"strconv"
	"strings"
)

const telegramSilentReplyToken = "NO_REPLY"

var tgReactTagRe = regexp.MustCompile(`(?is)\[\[\s*react\s*:\s*([^\]]+?)\s*\]\]`)
var tgReplyCurrentTagRe = regexp.MustCompile(`(?is)\[\[\s*reply_to_current\s*\]\]`)
var tgReplyIDTagRe = regexp.MustCompile(`(?is)\[\[\s*reply_to\s*:\s*([0-9]+)\s*\]\]`)

type tgAssistantOut struct {
	Text      string
	Reaction  string
	ReplyToID int
	ReplyNow  bool
	Silent    bool
	HasAction bool
}

func parseTelegramAssistantOutput(raw string) tgAssistantOut {
	idMatches := tgReplyIDTagRe.FindAllStringSubmatch(raw, -1)
	replyNow := tgReplyCurrentTagRe.MatchString(raw)

	matches := tgReactTagRe.FindAllStringSubmatch(raw, -1)
	clean := tgReactTagRe.ReplaceAllString(raw, "")
	clean = tgReplyCurrentTagRe.ReplaceAllString(clean, "")
	clean = tgReplyIDTagRe.ReplaceAllString(clean, "")
	clean = strings.TrimSpace(clean)

	out := tgAssistantOut{Text: clean, ReplyNow: replyNow}
	for _, m := range idMatches {
		if len(m) < 2 {
			continue
		}
		id := strings.TrimSpace(m[1])
		if id == "" {
			continue
		}
		if n, err := strconv.Atoi(id); err == nil && n > 0 {
			out.ReplyToID = n
		}
	}
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		e := normalizeReactionEmoji(m[1])
		if e == "" {
			continue
		}
		out.Reaction = e
		break
	}

	tok := strings.Trim(clean, " \t\n\r'\"`")
	if strings.EqualFold(tok, telegramSilentReplyToken) {
		out.Silent = true
		out.Text = ""
	}

	out.HasAction = out.Silent || out.Reaction != "" || out.Text != ""
	return out
}

func normalizeReactionEmoji(raw string) string {
	v := strings.TrimSpace(raw)
	if v == "" {
		return ""
	}
	if i := strings.IndexAny(v, ",|"); i > 0 {
		v = strings.TrimSpace(v[:i])
	}
	if parts := strings.Fields(v); len(parts) > 0 {
		return parts[0]
	}
	return ""
}
