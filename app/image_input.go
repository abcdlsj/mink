package app

import (
	"encoding/base64"
	"fmt"
	"mime"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/abcdlsj/sumi/msg"
)

const imageInputLimit = 20 << 20

var (
	imageDirectiveRe = regexp.MustCompile(`(?is)\[\[\s*(?:image|photo)\s*:\s*([^\]]+?)\s*\]\]`)
	imageMarkdownRe  = regexp.MustCompile(`!\[[^\]]*\]\(([^)\s]+)(?:\s+"[^"]*")?\)`)
)

func prepareImageInput(input string, attachments []msg.Attachment) (string, []msg.Attachment, error) {
	clean, refs := extractImageRefs(input)
	for _, ref := range refs {
		a, err := imageAttachment(ref)
		if err != nil {
			return "", nil, err
		}
		attachments = append(attachments, a)
	}
	if len(attachments) == 0 {
		return input, nil, nil
	}
	clean = strings.TrimSpace(clean)
	if clean == "" {
		clean = "请看这张图片。"
	}
	attachments = labelImageAttachments(attachments)
	clean = appendImageReferences(clean, attachments)
	return clean, attachments, nil
}

func labelImageAttachments(attachments []msg.Attachment) []msg.Attachment {
	n := 0
	for i := range attachments {
		if attachments[i].Kind != "image" {
			continue
		}
		n++
		if strings.TrimSpace(attachments[i].Label) == "" {
			attachments[i].Label = fmt.Sprintf("image_%d", n)
		}
	}
	return attachments
}

func appendImageReferences(input string, attachments []msg.Attachment) string {
	var refs []string
	for _, a := range attachments {
		if a.Kind != "image" {
			continue
		}
		label := strings.TrimSpace(a.Label)
		if label == "" {
			continue
		}
		name := strings.TrimSpace(a.Name)
		if name == "" {
			name = "image"
		}
		refs = append(refs, fmt.Sprintf("[%s: %s]", label, name))
	}
	if len(refs) == 0 {
		return input
	}
	return strings.TrimSpace(input) + "\n\nAttached images:\n" + strings.Join(refs, "\n")
}

func extractImageRefs(input string) (string, []string) {
	var refs []string
	clean := imageDirectiveRe.ReplaceAllStringFunc(input, func(s string) string {
		m := imageDirectiveRe.FindStringSubmatch(s)
		if len(m) > 1 {
			refs = append(refs, m[1])
		}
		return ""
	})
	clean = imageMarkdownRe.ReplaceAllStringFunc(clean, func(s string) string {
		m := imageMarkdownRe.FindStringSubmatch(s)
		if len(m) > 1 {
			refs = append(refs, m[1])
		}
		return ""
	})

	var lines []string
	for _, line := range strings.Split(clean, "\n") {
		if ref := pastedImageRef(line); ref != "" {
			refs = append(refs, ref)
			continue
		}
		lines = append(lines, line)
	}
	return strings.TrimSpace(strings.Join(lines, "\n")), refs
}

func pastedImageRef(line string) string {
	ref := cleanImageRef(line)
	if ref == "" {
		return ""
	}
	if imageURL(ref) {
		return ref
	}
	path := localImagePath(ref)
	st, err := os.Stat(path)
	if err != nil || st.IsDir() {
		return ""
	}
	if imageMIME(path, nil) == "" {
		return ""
	}
	return ref
}

func imageAttachment(ref string) (msg.Attachment, error) {
	ref = cleanImageRef(ref)
	if ref == "" {
		return msg.Attachment{}, fmt.Errorf("empty image ref")
	}
	if imageURL(ref) {
		return msg.Attachment{
			Kind: "image",
			Name: filepath.Base(ref),
			MIME: mimeByExt(ref),
			URL:  ref,
		}, nil
	}

	path := localImagePath(ref)
	data, err := os.ReadFile(path)
	if err != nil {
		return msg.Attachment{}, fmt.Errorf("read image %s: %w", ref, err)
	}
	if len(data) > imageInputLimit {
		return msg.Attachment{}, fmt.Errorf("image %s is larger than %d bytes", ref, imageInputLimit)
	}
	mime := imageMIME(path, data)
	if mime == "" {
		return msg.Attachment{}, fmt.Errorf("unsupported image type: %s", ref)
	}
	return msg.Attachment{
		Kind: "image",
		Name: filepath.Base(path),
		MIME: mime,
		Data: base64.StdEncoding.EncodeToString(data),
	}, nil
}

func cleanImageRef(ref string) string {
	ref = strings.TrimSpace(ref)
	ref = strings.Trim(ref, "`'\"")
	return strings.ReplaceAll(ref, `\ `, " ")
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

func localImagePath(ref string) string {
	if strings.HasPrefix(ref, "file://") {
		u, err := url.Parse(ref)
		if err == nil {
			path, err := url.PathUnescape(u.Path)
			if err == nil {
				return path
			}
			return u.Path
		}
		return strings.TrimPrefix(ref, "file://")
	}
	return expandHome(ref)
}

func imageURL(ref string) bool {
	u, err := url.Parse(ref)
	if err != nil {
		return false
	}
	return (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

func imageMIME(path string, data []byte) string {
	if mt := mimeByExt(path); mt != "" {
		return mt
	}
	if len(data) > 0 {
		switch {
		case len(data) >= 3 && string(data[:3]) == "\xff\xd8\xff":
			return "image/jpeg"
		case len(data) >= 8 && string(data[:8]) == "\x89PNG\r\n\x1a\n":
			return "image/png"
		case len(data) >= 6 && (string(data[:6]) == "GIF87a" || string(data[:6]) == "GIF89a"):
			return "image/gif"
		case len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
			return "image/webp"
		}
	}
	return ""
}

func mimeByExt(path string) string {
	switch strings.ToLower(mime.TypeByExtension(filepath.Ext(path))) {
	case "image/jpeg":
		return "image/jpeg"
	case "image/png":
		return "image/png"
	case "image/gif":
		return "image/gif"
	case "image/webp":
		return "image/webp"
	default:
		return ""
	}
}
