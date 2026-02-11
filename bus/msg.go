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
	Context MsgContext
	Time    time.Time
	ReplyTo string
}

type MsgContext struct {
	SessionID string
	AgentID   string
	BranchID  string
	ParentID  string
	Data      map[string]any
}

type Handler func(ctx context.Context, m Msg) (Msg, error)
