package app

import (
	"strings"

	"github.com/abcdlsj/sumi/msg"
	"github.com/abcdlsj/sumi/space"
)

type DraftAssistantMessage struct {
	AgentID         string
	Content         string
	Reasoning       string
	Mentions        []string
	AutoReplyReason string
	ParentMessageID string
	Added           []msg.Message
	Attachments     []msg.Attachment
}

func (d DraftAssistantMessage) Message() space.Message {
	return space.Message{
		AuthorID:        strings.TrimSpace(d.AgentID),
		AuthorKind:      space.ParticipantAgent,
		Content:         d.Content,
		Reasoning:       d.Reasoning,
		Mentions:        d.Mentions,
		AutoReplyReason: strings.TrimSpace(d.AutoReplyReason),
		ParentMessageID: strings.TrimSpace(d.ParentMessageID),
		Usage:           msg.AssistantUsage(d.Added),
		RuntimeMeta:     msg.AssistantRuntimeMeta(d.Added),
		Attachments:     d.Attachments,
	}
}
