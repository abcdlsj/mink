package bus

import (
	"context"
	"time"
)

type Msg struct {
	ID      string
	From    string
	To      string
	Type    string
	Payload any
	Time    time.Time
	ReplyTo string
}

type Handler func(ctx context.Context, m Msg) (Msg, error)
