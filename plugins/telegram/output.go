package telegram

import (
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
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

type matchRange struct {
	start int
	end   int
	idx   []int
}

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
	mask := markdownCodeMask(raw)
	idMatches := directiveMatches(raw, replyIDTagRe, mask)
	replyNowMatches := directiveMatches(raw, replyCurrentTagRe, mask)
	reactMatches := directiveMatches(raw, reactTagRe, mask)
	imageMatches := directiveMatches(raw, imageTagRe, mask)
	mdImageMatches := directiveMatches(raw, markdownImageRe, mask)

	images := parseImages(raw, imageMatches, mdImageMatches)
	clean := removeMatches(raw, reactMatches, replyNowMatches, idMatches, imageMatches, mdImageMatches)
	clean = strings.TrimSpace(clean)

	out := output{Text: clean, Images: images, ReplyNow: len(replyNowMatches) > 0}
	for _, m := range idMatches {
		if len(m.idx) < 4 {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(raw[m.idx[2]:m.idx[3]]))
		if err == nil && n > 0 {
			out.ReplyToID = n
		}
	}
	for _, m := range reactMatches {
		if len(m.idx) < 4 {
			continue
		}
		if emoji := normalizeReaction(raw[m.idx[2]:m.idx[3]]); emoji != "" {
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

func parseImages(raw string, matches ...[]matchRange) []image {
	var out []image
	for _, group := range matches {
		for _, m := range group {
			if len(m.idx) < 4 {
				continue
			}
			ref := cleanImageRef(raw[m.idx[2]:m.idx[3]])
			if ref != "" {
				out = append(out, image{Ref: ref})
			}
		}
	}
	return out
}

func directiveMatches(raw string, re *regexp.Regexp, mask []bool) []matchRange {
	idxs := re.FindAllStringSubmatchIndex(raw, -1)
	out := make([]matchRange, 0, len(idxs))
	for _, idx := range idxs {
		if len(idx) < 2 || protected(mask, idx[0], idx[1]) {
			continue
		}
		out = append(out, matchRange{start: idx[0], end: idx[1], idx: idx})
	}
	return out
}

func protected(mask []bool, start, end int) bool {
	for i := start; i < end && i < len(mask); i++ {
		if mask[i] {
			return true
		}
	}
	return false
}

func removeMatches(raw string, groups ...[]matchRange) string {
	var ranges []matchRange
	for _, group := range groups {
		ranges = append(ranges, group...)
	}
	if len(ranges) == 0 {
		return raw
	}
	sort.Slice(ranges, func(i, j int) bool {
		if ranges[i].start == ranges[j].start {
			return ranges[i].end < ranges[j].end
		}
		return ranges[i].start < ranges[j].start
	})
	var b strings.Builder
	pos := 0
	for _, r := range ranges {
		if r.start < pos {
			continue
		}
		b.WriteString(raw[pos:r.start])
		pos = r.end
	}
	b.WriteString(raw[pos:])
	return b.String()
}

func markdownCodeMask(raw string) []bool {
	mask := make([]bool, len(raw))
	markFencedCode(mask, raw)
	markCodeSpans(mask, raw)
	return mask
}

func markFencedCode(mask []bool, raw string) {
	inFence := false
	var fence byte
	fenceLen := 0
	for start := 0; start < len(raw); {
		end := strings.IndexByte(raw[start:], '\n')
		if end < 0 {
			end = len(raw)
		} else {
			end += start + 1
		}
		line := raw[start:end]
		if inFence {
			mark(mask, start, end)
			if closesFence(line, fence, fenceLen) {
				inFence = false
			}
			start = end
			continue
		}
		if ch, n, ok := opensFence(line); ok {
			mark(mask, start, end)
			inFence = true
			fence = ch
			fenceLen = n
		}
		start = end
	}
}

func opensFence(line string) (byte, int, bool) {
	i := indent(line)
	if i > 3 || i >= len(line) {
		return 0, 0, false
	}
	ch := line[i]
	if ch != '`' && ch != '~' {
		return 0, 0, false
	}
	n := fenceRun(line[i:], ch)
	if n < 3 {
		return 0, 0, false
	}
	return ch, n, true
}

func closesFence(line string, fence byte, min int) bool {
	i := indent(line)
	if i > 3 || i >= len(line) || line[i] != fence {
		return false
	}
	n := fenceRun(line[i:], fence)
	if n < min {
		return false
	}
	return strings.TrimSpace(strings.TrimRight(line[i+n:], "\n\r")) == ""
}

func indent(line string) int {
	i := 0
	for i < len(line) && i < 4 && line[i] == ' ' {
		i++
	}
	return i
}

func fenceRun(s string, ch byte) int {
	i := 0
	for i < len(s) && s[i] == ch {
		i++
	}
	return i
}

func markCodeSpans(mask []bool, raw string) {
	for i := 0; i < len(raw); i++ {
		if mask[i] || raw[i] != '`' {
			continue
		}
		n := fenceRun(raw[i:], '`')
		for j := i + n; j < len(raw); j++ {
			if mask[j] || raw[j] != '`' {
				continue
			}
			if fenceRun(raw[j:], '`') == n {
				mark(mask, i, j+n)
				i = j + n - 1
				break
			}
		}
	}
}

func mark(mask []bool, start, end int) {
	for i := start; i < end && i < len(mask); i++ {
		mask[i] = true
	}
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
	reply := replyTarget(msg, out)
	target := reactionTarget(msg, reply, out)
	if out.Reaction != "" && bot != nil && target != nil {
		_ = bot.React(msg.Chat, target, tele.Reactions{
			Reactions: []tele.Reaction{{
				Type:  tele.ReactionTypeEmoji,
				Emoji: out.Reaction,
			}},
		})
	}
	return sendParsed(c.Send, out, sendOptions(reply)...)
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

func reactionTarget(msg, reply *tele.Message, out output) *tele.Message {
	if out.Reaction == "" {
		return nil
	}
	if reply != nil {
		return reply
	}
	if msg == nil || msg.Chat == nil {
		return nil
	}
	return msg
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
		ParseMode: tele.ModeHTML,
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
		msg := part
		if hasParseMode(opts) {
			msg = renderTelegramHTML(part)
		}
		if err := send(msg, opts...); err != nil {
			if hasParseMode(opts) && parseModeError(err) {
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
	err := sendPreparedPhoto(send, telegramPhoto(img, caption), caption, opts...)
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

func sendPreparedPhoto(send sender, p *tele.Photo, caption string, opts ...interface{}) error {
	if p.Caption != "" {
		p.Caption = renderTelegramHTML(p.Caption)
	}
	if err := send(p, opts...); err != nil {
		if hasParseMode(opts) && parseModeError(err) {
			p.Caption = caption
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

func hasParseMode(opts []interface{}) bool {
	for _, opt := range opts {
		if so, ok := opt.(*tele.SendOptions); ok && so.ParseMode != tele.ModeDefault {
			return true
		}
	}
	return false
}

func parseModeError(err error) bool {
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
