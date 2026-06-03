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
	merged := map[string]string{}
	for id, mode := range sp.AgentModes {
		merged[id] = mode
	}
	if parentMessageID = strings.TrimSpace(parentMessageID); parentMessageID != "" {
		for id, mode := range sp.ThreadAgentModes[parentMessageID] {
			merged[id] = mode
		}
	}
	out := make([]string, 0, len(merged))
	for id, mode := range merged {
		if mode == "listen" {
			out = append(out, id)
		}
	}
	return out
}
