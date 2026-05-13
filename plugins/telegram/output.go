package telegram

import (
	"regexp"
	"strconv"
	"strings"

	tele "gopkg.in/telebot.v4"
)

const silentToken = "NO_REPLY"

var (
	reactTagRe        = regexp.MustCompile(`(?is)\[\[\s*react\s*:\s*([^\]]+?)\s*\]\]`)
	replyCurrentTagRe = regexp.MustCompile(`(?is)\[\[\s*reply_to_current\s*\]\]`)
	replyIDTagRe      = regexp.MustCompile(`(?is)\[\[\s*reply_to\s*:\s*([0-9]+)\s*\]\]`)
)

type output struct {
	Text      string
	Reaction  string
	ReplyToID int
	ReplyNow  bool
	Silent    bool
	HasAction bool
}

func parseOutput(raw string) output {
	idMatches := replyIDTagRe.FindAllStringSubmatch(raw, -1)
	replyNow := replyCurrentTagRe.MatchString(raw)

	matches := reactTagRe.FindAllStringSubmatch(raw, -1)
	clean := reactTagRe.ReplaceAllString(raw, "")
	clean = replyCurrentTagRe.ReplaceAllString(clean, "")
	clean = replyIDTagRe.ReplaceAllString(clean, "")
	clean = strings.TrimSpace(clean)

	out := output{Text: clean, ReplyNow: replyNow}
	for _, m := range idMatches {
		if len(m) < 2 {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(m[1]))
		if err == nil && n > 0 {
			out.ReplyToID = n
		}
	}
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		if emoji := normalizeReaction(m[1]); emoji != "" {
			out.Reaction = emoji
			break
		}
	}
	tok := strings.Trim(clean, " \t\n\r'\"`")
	if strings.EqualFold(tok, silentToken) {
		out.Silent = true
		out.Text = ""
	}
	out.HasAction = out.Silent || out.Reaction != "" || out.Text != ""
	return out
}

func sendOutput(bot *tele.Bot, c tele.Context, raw string) error {
	out := parseOutput(raw)
	if !out.HasAction {
		return nil
	}
	msg := c.Message()
	if msg == nil || msg.Chat == nil {
		return nil
	}
	target := replyTarget(msg, out)
	if out.Reaction != "" && bot != nil && target != nil {
		_ = bot.React(msg.Chat, target, tele.Reactions{
			Reactions: []tele.Reaction{{
				Type:  tele.ReactionTypeEmoji,
				Emoji: out.Reaction,
			}},
		})
	}
	if out.Silent || strings.TrimSpace(out.Text) == "" {
		return nil
	}
	return sendLong(c, out.Text, sendOptions(target)...)
}

func sendNotice(bot *tele.Bot, source, raw string) error {
	if bot == nil {
		return nil
	}
	chatID, threadID, ok := parseTelegramSource(source)
	if !ok {
		return nil
	}
	out := parseOutput(raw)
	if !out.HasAction {
		return nil
	}
	chat := &tele.Chat{ID: chatID}
	target := noticeReplyTarget(chat, out)
	if out.Reaction != "" && target != nil {
		_ = bot.React(chat, target, tele.Reactions{
			Reactions: []tele.Reaction{{
				Type:  tele.ReactionTypeEmoji,
				Emoji: out.Reaction,
			}},
		})
	}
	if out.Silent || strings.TrimSpace(out.Text) == "" {
		return nil
	}
	opts := sendOption(target)
	if threadID > 0 {
		opts.ThreadID = threadID
	}
	return botSendLong(bot, chat, out.Text, opts)
}

func replyTarget(msg *tele.Message, out output) *tele.Message {
	switch {
	case msg == nil || msg.Chat == nil:
		return nil
	case out.ReplyToID > 0:
		return &tele.Message{ID: out.ReplyToID, Chat: msg.Chat}
	case out.ReplyNow:
		return msg
	default:
		return nil
	}
}

func noticeReplyTarget(chat *tele.Chat, out output) *tele.Message {
	if chat == nil || out.ReplyToID <= 0 {
		return nil
	}
	return &tele.Message{ID: out.ReplyToID, Chat: chat}
}

func sendOptions(reply *tele.Message) []interface{} {
	return []interface{}{sendOption(reply)}
}

func sendOption(reply *tele.Message) *tele.SendOptions {
	return &tele.SendOptions{
		ReplyTo:   reply,
		ParseMode: tele.ModeMarkdown,
	}
}

func botSendLong(bot *tele.Bot, chat *tele.Chat, text string, opts *tele.SendOptions) error {
	for _, part := range split(text, 3500) {
		if _, err := bot.Send(chat, part, opts); err != nil {
			if shouldRetryPlain(err, opts) {
				plain := plainSendOption(opts)
				if _, retryErr := bot.Send(chat, part, plain); retryErr == nil {
					continue
				}
			}
			return err
		}
	}
	return nil
}

func plainSendOption(opts *tele.SendOptions) *tele.SendOptions {
	if opts == nil {
		return nil
	}
	plain := *opts
	plain.ParseMode = tele.ModeDefault
	return &plain
}

func plainSendOptions(opts []interface{}) []interface{} {
	if len(opts) == 0 {
		return nil
	}
	out := make([]interface{}, 0, len(opts))
	for _, opt := range opts {
		if so, ok := opt.(*tele.SendOptions); ok {
			out = append(out, plainSendOption(so))
			continue
		}
		out = append(out, opt)
	}
	return out
}

func hasMarkdown(opts []interface{}) bool {
	for _, opt := range opts {
		if so, ok := opt.(*tele.SendOptions); ok && so.ParseMode == tele.ModeMarkdown {
			return true
		}
	}
	return false
}

func shouldRetryPlain(err error, opts *tele.SendOptions) bool {
	if err == nil || opts == nil || opts.ParseMode != tele.ModeMarkdown {
		return false
	}
	return markdownParseError(err)
}

func markdownParseError(err error) bool {
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "parse") || strings.Contains(s, "entity") || strings.Contains(s, "markdown")
}

func normalizeReaction(raw string) string {
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
