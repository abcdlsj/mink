package llm

import (
	"strings"

	"github.com/abcdlsj/sumi/msg"
)

func imageAttachments(m msg.Message) []msg.Attachment {
	var out []msg.Attachment
	for _, a := range m.Attachments {
		if a.Kind == "image" && (a.URL != "" || (a.Data != "" && a.MIME != "")) {
			out = append(out, a)
		}
	}
	return out
}

func imageURL(a msg.Attachment) string {
	if a.URL != "" {
		return a.URL
	}
	if a.Data == "" || a.MIME == "" {
		return ""
	}
	return "data:" + a.MIME + ";base64," + strings.TrimSpace(a.Data)
}
