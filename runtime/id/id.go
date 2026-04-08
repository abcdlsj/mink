package id

import (
	"strings"

	"github.com/google/uuid"
)

func New(prefix string) string {
	s := strings.ReplaceAll(uuid.NewString(), "-", "")
	if prefix == "" {
		return s
	}
	return prefix + "_" + s[:16]
}

func Task() string {
	return New("task")
}

func Run() string {
	return New("run")
}

func Event() string {
	return New("event")
}

func Artifact() string {
	return New("artifact")
}

func Memory() string {
	return New("mem")
}

func Team() string {
	return New("team")
}

func Thread() string {
	return New("thread")
}
