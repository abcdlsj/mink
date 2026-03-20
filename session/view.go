package session

import "github.com/abcdlsj/mink/msg"

func buildView(s *Session, limit int) ([]msg.Message, *Anchor) {
	if s == nil {
		return nil, nil
	}

	snap := s.snapshot()
	entries := snap.Entries
	if limit >= 0 && limit < len(entries) {
		entries = entries[:limit]
	}

	if anchor := latestAnchor(snap.Anchors, len(entries)); anchor != nil {
		start := anchor.EntryCount
		if start > len(entries) {
			start = len(entries)
		}
		msgs := make([]msg.Message, 0, 1+len(entries)-start)
		msgs = append(msgs, anchorMessage(*anchor))
		msgs = append(msgs, entriesToMessages(entries[start:])...)
		return msgs, anchor
	}

	if snap.Provenance != nil {
		parent, err := s.parent(snap.Provenance.ParentSessionID)
		if err == nil && parent != nil {
			msgs, anchor := buildView(parent, snap.Provenance.ForkEntryCount)
			msgs = append(msgs, entriesToMessages(entries)...)
			return msgs, anchor
		}
	}

	return entriesToMessages(entries), nil
}

func entriesToMessages(entries []Entry) []msg.Message {
	msgs := make([]msg.Message, 0, len(entries))
	for _, e := range entries {
		msgs = append(msgs, e.Message)
	}
	return msgs
}

func latestAnchor(anchors []Anchor, entryCount int) *Anchor {
	for i := len(anchors) - 1; i >= 0; i-- {
		if anchors[i].EntryCount <= entryCount {
			a := anchors[i]
			return &a
		}
	}
	return nil
}

func anchorMessage(a Anchor) msg.Message {
	return msg.Message{
		Role:    "system",
		Content: "[Context Anchor]\n" + a.Summary,
	}
}
