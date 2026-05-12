package telegram

import (
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

var httpCodeRe = regexp.MustCompile(`(?i)(?:HTTP|status code:)\s*(\d{3})`)

func userError(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.TrimSpace(err.Error())
	if code := errorHTTPCode(msg); code != 0 {
		switch {
		case code == http.StatusTooManyRequests:
			return "Model service is rate limited (HTTP 429). Please try again later."
		case code >= http.StatusInternalServerError:
			return fmt.Sprintf("Model service is temporarily unavailable (HTTP %d). Please try again later.", code)
		}
	}
	return "error: " + msg
}

func errorHTTPCode(msg string) int {
	m := httpCodeRe.FindStringSubmatch(msg)
	if len(m) != 2 {
		return 0
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0
	}
	return n
}
