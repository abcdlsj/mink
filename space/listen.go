package space

import (
	"strings"
)

func ListenMatches(content string, listening []string) []string {
	if strings.TrimSpace(content) == "" {
		return nil
	}
	hits := make([]string, 0, len(listening))
	for _, id := range listening {
		if id = strings.TrimSpace(id); id != "" {
			hits = append(hits, id)
		}
	}
	return hits
}

func ListeningAgents(sp *Space, parentMessageID string) []string {
	if sp == nil {
		return nil
	}
	modes := sp.AgentModes
	if parentMessageID = strings.TrimSpace(parentMessageID); parentMessageID != "" {
		if threadModes, ok := sp.ThreadAgentModes[parentMessageID]; ok {
			modes = threadModes
		}
	}
	out := make([]string, 0, len(modes))
	for id, mode := range modes {
		if mode == "listen" {
			out = append(out, id)
		}
	}
	return out
}
