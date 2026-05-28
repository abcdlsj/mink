//go:build darwin

package cli

import (
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/abcdlsj/sumi/msg"
)

const clipboardImageLimit = 20 << 20

func pasteClipboardImage() tea.Cmd {
	return func() tea.Msg {
		a, err := readClipboardImage()
		return shellClipboardImageMsg{Attachment: a, Err: err}
	}
}

func readClipboardImage() (msg.Attachment, error) {
	for _, f := range []clipboardFormat{
		{Class: "PNGf", MIME: "image/png", Ext: ".png"},
		{Class: "JPEG", MIME: "image/jpeg", Ext: ".jpg"},
	} {
		a, err := readClipboardFormat(f)
		if err == nil {
			return a, nil
		}
	}
	return msg.Attachment{}, fmt.Errorf("clipboard has no supported image")
}

type clipboardFormat struct {
	Class string
	MIME  string
	Ext   string
}

func readClipboardFormat(f clipboardFormat) (msg.Attachment, error) {
	path := filepath.Join(os.TempDir(), fmt.Sprintf("sumi-clipboard-%d%s", time.Now().UnixNano(), f.Ext))
	defer os.Remove(path)
	script := fmt.Sprintf(`set img to the clipboard as «class %s»
set outFile to POSIX file %q
set fh to open for access outFile with write permission
set eof fh to 0
write img to fh
close access fh`, f.Class, path)
	if out, err := exec.Command("osascript", "-e", script).CombinedOutput(); err != nil {
		return msg.Attachment{}, fmt.Errorf("%s: %s", err, strings.TrimSpace(string(out)))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return msg.Attachment{}, err
	}
	if len(data) == 0 {
		return msg.Attachment{}, fmt.Errorf("clipboard image is empty")
	}
	if len(data) > clipboardImageLimit {
		return msg.Attachment{}, fmt.Errorf("clipboard image is larger than %d bytes", clipboardImageLimit)
	}
	return msg.Attachment{
		Kind: "image",
		Name: "clipboard" + f.Ext,
		MIME: f.MIME,
		Data: base64.StdEncoding.EncodeToString(data),
	}, nil
}
