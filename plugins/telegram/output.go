package telegram

import (
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	tele "gopkg.in/telebot.v4"
)

const silentToken = "NO_REPLY"
const captionLimit = 1024

var (
	reactTagRe        = regexp.MustCompile(`(?is)\[\[\s*react\s*:\s*([^\]]+?)\s*\]\]`)
	replyCurrentTagRe = regexp.MustCompile(`(?is)\[\[\s*reply_to_current\s*\]\]`)
	replyIDTagRe      = regexp.MustCompile(`(?is)\[\[\s*reply_to\s*:\s*([0-9]+)\s*\]\]`)
	imageTagRe        = regexp.MustCompile(`(?is)\[\[\s*(?:photo|image)\s*:\s*([^\]]+?)\s*\]\]`)
	markdownImageRe   = regexp.MustCompile(`!\[[^\]]*\]\(([^)\s]+)(?:\s+"[^"]*")?\)`)
)

type output struct {
	Text      string
	Reaction  string
	Images    []image
	ReplyToID int
	ReplyNow  bool
	Silent    bool
	HasAction bool
}

type image struct {
	Ref string
}

type sender func(any, ...interface{}) error

func parseOutput(raw string) output {
	idMatches := replyIDTagRe.FindAllStringSubmatch(raw, -1)
	replyNow := replyCurrentTagRe.MatchString(raw)

	matches := reactTagRe.FindAllStringSubmatch(raw, -1)
	images := parseImages(raw)
	clean := reactTagRe.ReplaceAllString(raw, "")
	clean = replyCurrentTagRe.ReplaceAllString(clean, "")
	clean = replyIDTagRe.ReplaceAllString(clean, "")
	clean = imageTagRe.ReplaceAllString(clean, "")
	clean = markdownImageRe.ReplaceAllString(clean, "")
	clean = strings.TrimSpace(clean)

	out := output{Text: clean, Images: images, ReplyNow: replyNow}
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
	if strings.EqualFold(tok, silentToken) && len(images) == 0 {
		out.Silent = true
		out.Text = ""
	}
	out.HasAction = out.Silent || out.Reaction != "" || out.Text != "" || len(out.Images) > 0
	return out
}

func parseImages(raw string) []image {
	var out []image
	for _, re := range []*regexp.Regexp{imageTagRe, markdownImageRe} {
		for _, m := range re.FindAllStringSubmatch(raw, -1) {
			if len(m) < 2 {
				continue
			}
			ref := cleanImageRef(m[1])
			if ref != "" {
				out = append(out, image{Ref: ref})
			}
		}
	}
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
	return sendParsed(c.Send, out, sendOptions(target)...)
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
	opts := sendOption(target)
	if threadID > 0 {
		opts.ThreadID = threadID
	}
	send := func(what any, opts ...interface{}) error {
		_, err := bot.Send(chat, what, opts...)
		return err
	}
	return sendParsed(send, out, opts)
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

func sendParsed(send sender, out output, opts ...interface{}) error {
	if out.Silent {
		return nil
	}
	if strings.TrimSpace(out.Text) == "" {
		return sendImages(send, out.Images, opts...)
	}
	return sendTextImages(send, out.Text, out.Images, opts...)
}

func sendText(send sender, text string, opts ...interface{}) error {
	for _, part := range split(text, 3500) {
		if err := send(part, opts...); err != nil {
			if hasMarkdown(opts) && markdownParseError(err) {
				if retryErr := send(part, plainSendOptions(opts)...); retryErr == nil {
					continue
				}
			}
			return err
		}
	}
	return nil
}

func sendTextImages(send sender, text string, images []image, opts ...interface{}) error {
	text = strings.TrimSpace(text)
	if len(images) == 0 {
		if text == "" {
			return nil
		}
		return sendText(send, text, opts...)
	}
	if text != "" && len(images) == 1 && captionOK(text) {
		return sendPhoto(send, images[0], text, opts...)
	}
	if text != "" {
		if err := sendText(send, text, opts...); err != nil {
			return err
		}
	}
	return sendImages(send, images, opts...)
}

func sendImages(send sender, images []image, opts ...interface{}) error {
	for _, img := range images {
		if err := sendPhoto(send, img, "", opts...); err != nil {
			return err
		}
	}
	return nil
}

func sendPhoto(send sender, img image, caption string, opts ...interface{}) error {
	err := sendPreparedPhoto(send, telegramPhoto(img, caption), opts...)
	if err == nil {
		return nil
	}
	if shouldUploadHTTPImage(img, err) {
		err = sendDownloadedPhoto(send, img, caption, opts...)
		if err == nil {
			return nil
		}
	}
	return sendPhotoError(send, img, caption, err, opts...)
}

func sendPreparedPhoto(send sender, p *tele.Photo, opts ...interface{}) error {
	if err := send(p, opts...); err != nil {
		if hasMarkdown(opts) && markdownParseError(err) {
			if retryErr := send(p, plainSendOptions(opts)...); retryErr == nil {
				return nil
			}
		}
		return err
	}
	return nil
}

func telegramPhoto(img image, caption string) *tele.Photo {
	ref := cleanImageRef(img.Ref)
	p := &tele.Photo{Caption: caption}
	switch {
	case ref == "":
	case isHTTPURL(ref):
		p.File = tele.FromURL(ref)
	case strings.HasPrefix(ref, "file://"):
		p.File = tele.FromDisk(fileURLPath(ref))
	case isLocalImageRef(ref):
		p.File = tele.FromDisk(expandHome(ref))
	default:
		p.File = tele.File{FileID: ref}
	}
	return p
}

func cleanImageRef(ref string) string {
	return strings.Trim(strings.TrimSpace(ref), "`'\"")
}

func captionOK(s string) bool {
	return len([]rune(s)) <= captionLimit
}

func isHTTPURL(ref string) bool {
	u, err := url.Parse(ref)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

func isLocalImageRef(ref string) bool {
	ref = expandHome(ref)
	if filepath.IsAbs(ref) || strings.HasPrefix(ref, ".") || strings.HasPrefix(ref, "~") {
		return true
	}
	if _, err := os.Stat(ref); err == nil {
		return true
	}
	switch strings.ToLower(filepath.Ext(ref)) {
	case ".jpg", ".jpeg", ".png", ".webp", ".gif", ".bmp", ".heic", ".heif":
		return true
	default:
		return false
	}
}

func expandHome(path string) string {
	if path == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
	}
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}

func fileURLPath(ref string) string {
	u, err := url.Parse(ref)
	if err != nil {
		return strings.TrimPrefix(ref, "file://")
	}
	path, err := url.PathUnescape(u.Path)
	if err != nil {
		return u.Path
	}
	return path
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
