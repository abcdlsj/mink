package telegram

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	tele "gopkg.in/telebot.v4"
)

func TestParseTelegramOutput(t *testing.T) {
	out := parseOutput("[[reply_to:42]] [[react:👍]] done")
	if out.Text != "done" {
		t.Fatalf("text = %q", out.Text)
	}
	if out.ReplyToID != 42 {
		t.Fatalf("reply_to = %d", out.ReplyToID)
	}
	if out.Reaction != "👍" {
		t.Fatalf("reaction = %q", out.Reaction)
	}
	if !out.HasAction {
		t.Fatal("expected action")
	}
}

func TestParseTelegramOutputSilent(t *testing.T) {
	out := parseOutput("NO_REPLY")
	if !out.Silent {
		t.Fatal("expected silent")
	}
	if out.Text != "" {
		t.Fatalf("text = %q", out.Text)
	}
}

func TestParseTelegramOutputReactionOnly(t *testing.T) {
	out := parseOutput("[[react:👍]]")
	if out.Reaction != "👍" {
		t.Fatalf("reaction = %q", out.Reaction)
	}
	if out.Text != "" {
		t.Fatalf("text = %q", out.Text)
	}
	if !out.HasAction {
		t.Fatal("expected action")
	}
}

func TestReactionOnlyTargetsCurrentMessage(t *testing.T) {
	msg := &tele.Message{ID: 11, Chat: &tele.Chat{ID: 42}}
	out := parseOutput("[[react:👍]]")
	got := reactionTarget(msg, replyTarget(msg, out), out)
	if got == nil || got.ID != 11 {
		t.Fatalf("target = %#v", got)
	}
}

func TestReactionWithReplyTargetUsesReply(t *testing.T) {
	msg := &tele.Message{ID: 11, Chat: &tele.Chat{ID: 42}}
	out := parseOutput("[[reply_to:99]] [[react:👍]]")
	got := reactionTarget(msg, replyTarget(msg, out), out)
	if got == nil || got.ID != 99 {
		t.Fatalf("target = %#v", got)
	}
}

func TestParseTelegramOutputReplyCurrent(t *testing.T) {
	out := parseOutput("[[reply_to_current]] hi")
	if !out.ReplyNow {
		t.Fatal("expected reply_now")
	}
	if out.Text != "hi" {
		t.Fatalf("text = %q", out.Text)
	}
}

func TestParseTelegramOutputIgnoresReactionInFencedCode(t *testing.T) {
	raw := "我的原始输出就是：\n\n```markdown\n[[react:👍]]\n```\n\n拿这个排查"
	out := parseOutput(raw)
	if out.Reaction != "" {
		t.Fatalf("reaction = %q", out.Reaction)
	}
	if out.Text != raw {
		t.Fatalf("text = %q", out.Text)
	}
}

func TestParseTelegramOutputIgnoresReactionInCodeSpan(t *testing.T) {
	raw := "directive 是 `[[react:👍]]`"
	out := parseOutput(raw)
	if out.Reaction != "" {
		t.Fatalf("reaction = %q", out.Reaction)
	}
	if out.Text != raw {
		t.Fatalf("text = %q", out.Text)
	}
}

func TestParseTelegramOutputImages(t *testing.T) {
	out := parseOutput("[[reply_to:42]] look [[photo:https://example.com/a.png]] and ![b](./b.jpg)")
	if out.Text != "look  and" {
		t.Fatalf("text = %q", out.Text)
	}
	if len(out.Images) != 2 {
		t.Fatalf("images = %#v", out.Images)
	}
	if out.Images[0].Ref != "https://example.com/a.png" {
		t.Fatalf("image 0 = %#v", out.Images[0])
	}
	if out.Images[1].Ref != "./b.jpg" {
		t.Fatalf("image 1 = %#v", out.Images[1])
	}
	if !out.HasAction {
		t.Fatal("expected action")
	}
}

func TestParseTelegramOutputImageKeepsNoReplyText(t *testing.T) {
	out := parseOutput("NO_REPLY [[photo:https://example.com/a.png]]")
	if out.Silent {
		t.Fatal("unexpected silent")
	}
	if out.Text != "NO_REPLY" {
		t.Fatalf("text = %q", out.Text)
	}
}

func TestTelegramPhotoSource(t *testing.T) {
	got := telegramPhoto(image{Ref: "https://example.com/a.png"}, "cap")
	if got.FileURL != "https://example.com/a.png" {
		t.Fatalf("url = %q", got.FileURL)
	}
	if got.Caption != "cap" {
		t.Fatalf("caption = %q", got.Caption)
	}

	got = telegramPhoto(image{Ref: "./a.png"}, "")
	if got.FileLocal != "./a.png" {
		t.Fatalf("local = %q", got.FileLocal)
	}

	got = telegramPhoto(image{Ref: "AgACAgUAAxkBAAIB"}, "")
	if got.FileID != "AgACAgUAAxkBAAIB" {
		t.Fatalf("file id = %q", got.FileID)
	}

	got = telegramPhoto(image{Ref: "AgAC/with/slash"}, "")
	if got.FileID != "AgAC/with/slash" {
		t.Fatalf("file id with slash = %q", got.FileID)
	}
}

func TestTelegramPhotoDownloadsHTTP(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("png"))
	}))
	defer s.Close()

	got, cleanup, err := downloadedTelegramPhoto(image{Ref: s.URL + "/a.png"}, "cap")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if got.FileURL != "" {
		t.Fatalf("url = %q", got.FileURL)
	}
	if got.FileLocal == "" {
		t.Fatal("expected local file")
	}
	b, err := os.ReadFile(got.FileLocal)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "png" {
		t.Fatalf("file = %q", b)
	}
	if got.Caption != "cap" {
		t.Fatalf("caption = %q", got.Caption)
	}
}

func TestSendPhotoFallsBackToText(t *testing.T) {
	var sent []any
	send := func(what any, opts ...interface{}) error {
		sent = append(sent, what)
		if _, ok := what.(*tele.Photo); ok {
			return errors.New("telegram: failed to get HTTP URL content (400)")
		}
		return nil
	}

	if err := sendPhoto(send, image{Ref: "AgACAgUAAxkBAAIB"}, "cap", sendOptions(nil)...); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 2 {
		t.Fatalf("sent = %#v", sent)
	}
	text, ok := sent[1].(string)
	if !ok {
		t.Fatalf("fallback = %T", sent[1])
	}
	if !strings.Contains(text, "cap") || !strings.Contains(text, "image failed to send") || !strings.Contains(text, "400") {
		t.Fatalf("fallback = %q", text)
	}
}

func TestSendPlainTextDoesNotRenderHTML(t *testing.T) {
	var sent []any
	send := func(what any, opts ...interface{}) error {
		sent = append(sent, what)
		return nil
	}

	if err := sendText(send, "https://x.test/a?height=600&tag=abc", plainSendOptions(sendOptions(nil))...); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 1 {
		t.Fatalf("sent = %#v", sent)
	}
	if sent[0] != "https://x.test/a?height=600&tag=abc" {
		t.Fatalf("text = %q", sent[0])
	}
}

func TestSendHTTPPhotoFallsBackToUpload(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("png"))
	}))
	defer s.Close()

	var sent []any
	send := func(what any, opts ...interface{}) error {
		sent = append(sent, what)
		if p, ok := what.(*tele.Photo); ok && p.FileURL != "" {
			return errors.New("telegram: failed to get HTTP URL content (400)")
		}
		return nil
	}

	if err := sendPhoto(send, image{Ref: s.URL + "/a.png"}, "cap", sendOptions(nil)...); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 2 {
		t.Fatalf("sent = %#v", sent)
	}
	first := sent[0].(*tele.Photo)
	if first.FileURL == "" {
		t.Fatalf("first photo = %#v", first)
	}
	second := sent[1].(*tele.Photo)
	if second.FileLocal == "" {
		t.Fatalf("second photo = %#v", second)
	}
}

func TestSendHTTPPhotoWrongWebPageContentFallsBackToUpload(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("png"))
	}))
	defer s.Close()

	var sent []any
	send := func(what any, opts ...interface{}) error {
		sent = append(sent, what)
		if p, ok := what.(*tele.Photo); ok && p.FileURL != "" {
			return errors.New("telegram: wrong type of the web page content (400)")
		}
		return nil
	}

	if err := sendPhoto(send, image{Ref: s.URL + "/a.png"}, "cap", sendOptions(nil)...); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 2 {
		t.Fatalf("sent = %#v", sent)
	}
	if sent[1].(*tele.Photo).FileLocal == "" {
		t.Fatalf("upload = %#v", sent[1])
	}
}

func TestSendHTTPPhotoDownloadFailureFallsBackToText(t *testing.T) {
	s := httptest.NewServer(http.NotFoundHandler())
	defer s.Close()

	var sent []any
	send := func(what any, opts ...interface{}) error {
		sent = append(sent, what)
		if p, ok := what.(*tele.Photo); ok && p.FileURL != "" {
			return errors.New("telegram: failed to get HTTP URL content (400)")
		}
		return nil
	}

	if err := sendPhoto(send, image{Ref: s.URL + "/missing.png"}, "", sendOptions(nil)...); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 2 {
		t.Fatalf("sent = %#v", sent)
	}
	text, ok := sent[1].(string)
	if !ok {
		t.Fatalf("fallback = %T", sent[0])
	}
	if !strings.Contains(text, "image failed to send") || !strings.Contains(text, "HTTP 404") {
		t.Fatalf("fallback = %q", text)
	}
}

func TestSendOptionsEnableHTMLWithoutReply(t *testing.T) {
	opts := sendOptions(nil)
	if len(opts) != 1 {
		t.Fatalf("len = %d", len(opts))
	}
	opt, ok := opts[0].(*tele.SendOptions)
	if !ok {
		t.Fatalf("option type = %T", opts[0])
	}
	if opt.ReplyTo != nil {
		t.Fatalf("reply = %v", opt.ReplyTo)
	}
	if opt.ParseMode != tele.ModeHTML {
		t.Fatalf("parse mode = %q", opt.ParseMode)
	}
}

func TestSendOptionsRepliesToMessage(t *testing.T) {
	reply := &tele.Message{ID: 42}
	opts := sendOptions(reply)
	if len(opts) != 1 {
		t.Fatalf("len = %d", len(opts))
	}
	opt, ok := opts[0].(*tele.SendOptions)
	if !ok {
		t.Fatalf("option type = %T", opts[0])
	}
	if opt.ReplyTo != reply {
		t.Fatalf("reply = %v", opt.ReplyTo)
	}
	if opt.ParseMode != tele.ModeHTML {
		t.Fatalf("parse mode = %q", opt.ParseMode)
	}
}

func TestNoticeReplyTargetRequiresExplicitReplyID(t *testing.T) {
	chat := &tele.Chat{ID: 7}
	if got := noticeReplyTarget(chat, output{ReplyNow: true}); got != nil {
		t.Fatalf("target = %#v", got)
	}
	got := noticeReplyTarget(chat, output{ReplyToID: 42})
	if got == nil || got.ID != 42 || got.Chat != chat {
		t.Fatalf("target = %#v", got)
	}
}

func TestPlainSendOptionsDropParseMode(t *testing.T) {
	reply := &tele.Message{ID: 42}
	opts := plainSendOptions(sendOptions(reply))
	opt := opts[0].(*tele.SendOptions)
	if opt.ReplyTo != reply {
		t.Fatalf("reply = %v", opt.ReplyTo)
	}
	if opt.ParseMode != tele.ModeDefault {
		t.Fatalf("parse mode = %q", opt.ParseMode)
	}
}

func TestParseModeError(t *testing.T) {
	if !parseModeError(errors.New("Bad Request: can't parse entities")) {
		t.Fatal("expected parse error")
	}
	if parseModeError(errors.New("network timeout")) {
		t.Fatal("unexpected parse error")
	}
}
