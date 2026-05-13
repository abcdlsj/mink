package telegram

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	tele "gopkg.in/telebot.v4"
)

const imageDownloadLimit = 20 << 20
const imageDownloadTimeout = 20 * time.Second

func sendDownloadedPhoto(send sender, img image, caption string, opts ...interface{}) error {
	p, cleanup, err := downloadedTelegramPhoto(img, caption)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		return err
	}
	return sendPreparedPhoto(send, p, caption, opts...)
}

func downloadedTelegramPhoto(img image, caption string) (*tele.Photo, func(), error) {
	path, cleanup, err := downloadImage(cleanImageRef(img.Ref))
	if err != nil {
		return nil, nil, err
	}
	return &tele.Photo{Caption: caption, File: tele.FromDisk(path)}, cleanup, nil
}

func shouldUploadHTTPImage(img image, err error) bool {
	if err == nil || !isHTTPURL(cleanImageRef(img.Ref)) {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "http url") ||
		strings.Contains(s, "url content") ||
		strings.Contains(s, "web page content") ||
		strings.Contains(s, "wrong file identifier")
}

func sendPhotoError(send sender, img image, caption string, err error, opts ...interface{}) error {
	return sendText(send, photoErrorText(img, caption, err), plainSendOptions(opts)...)
}

func photoErrorText(img image, caption string, err error) string {
	msg := fmt.Sprintf("[image failed to send: %s - %v]", cleanImageRef(img.Ref), err)
	caption = strings.TrimSpace(caption)
	if caption == "" {
		return msg
	}
	return caption + "\n\n" + msg
}

func downloadImage(ref string) (string, func(), error) {
	req, err := http.NewRequest(http.MethodGet, ref, nil)
	if err != nil {
		return "", nil, err
	}
	req.Header.Set("User-Agent", "sumi")

	client := http.Client{Timeout: imageDownloadTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", nil, fmt.Errorf("download image: HTTP %d", resp.StatusCode)
	}

	f, err := os.CreateTemp("", "sumi-telegram-image-*"+imageExt(ref))
	if err != nil {
		return "", nil, err
	}
	path := f.Name()
	cleanup := func() { _ = os.Remove(path) }

	n, err := io.Copy(f, io.LimitReader(resp.Body, imageDownloadLimit+1))
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		cleanup()
		return "", nil, err
	}
	if n > imageDownloadLimit {
		cleanup()
		return "", nil, fmt.Errorf("download image: larger than %d bytes", imageDownloadLimit)
	}
	return path, cleanup, nil
}

func imageExt(ref string) string {
	u, err := url.Parse(ref)
	if err == nil {
		ref = u.Path
	}
	ext := filepath.Ext(ref)
	switch strings.ToLower(ext) {
	case ".jpg", ".jpeg", ".png", ".webp", ".gif", ".bmp", ".heic", ".heif":
		return ext
	default:
		return ".img"
	}
}
