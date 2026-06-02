package space

import (
	"strings"
	"unicode/utf8"
)

const minListenSeedLen = 6

var listenKeywords = map[string][]string{
	"coder":    {"code", "build", "retry", "error", "fail", "test", "fix", "bug", "compile", "panic", "exception", "构建", "错误", "失败", "编译"},
	"reviewer": {"review", "regression", "risk", "lgtm", "audit", "diff", "pr", "commit", "审查", "风险"},
	"tshoot":   {"debug", "trace", "panic", "leak", "deadlock", "oom", "latency", "排查", "死锁"},
}

var listenSkip = map[string]bool{
	"hi": true, "hey": true, "hello": true, "yo": true,
	"ok": true, "okay": true, "thanks": true, "thank you": true,
	"lgtm": true,
	"在吗":   true, "你好": true, "您好": true, "嗨": true, "收到": true, "好的": true,
	"看看": true, "看一下": true, "看下": true, "帮我看看": true,
}

func ListenMatches(content string, listening []string) []string {
	t := strings.TrimSpace(content)
	if utf8.RuneCountInString(t) < minListenSeedLen {
		return nil
	}
	if listenSkip[strings.ToLower(t)] {
		return nil
	}
	lower := strings.ToLower(content)
	hits := make([]string, 0, len(listening))
	for _, id := range listening {
		kws := listenKeywords[strings.ToLower(id)]
		if len(kws) == 0 {
			continue
		}
		for _, kw := range kws {
			if strings.Contains(lower, kw) {
				hits = append(hits, id)
				break
			}
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
