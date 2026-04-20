package cron

import (
	"sync"

	"github.com/abcdlsj/mink/app"
	robcron "github.com/robfig/cron/v3"
)

type Task struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Schedule string `json:"schedule"`
	Prompt   string `json:"prompt"`
	Enabled  bool   `json:"enabled"`
	Source   string `json:"source"`
}

type params struct {
	Action   string `json:"action"`
	ID       string `json:"id"`
	Name     string `json:"name"`
	Schedule string `json:"schedule"`
	Prompt   string `json:"prompt"`
	Source   string `json:"source"`
}

type scheduler struct {
	app  *app.App
	path string
	mu   sync.Mutex
	c    *robcron.Cron
}
