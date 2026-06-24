package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/abcdlsj/sumi/agent"
	"github.com/abcdlsj/sumi/command"
	"github.com/abcdlsj/sumi/msg"
	"github.com/abcdlsj/sumi/session"
)

var memoryFenceRE = regexp.MustCompile("(?s)```([^`\n]*)\n(.*?)```")

type MemoryOverview struct {
	Scopes []MemoryScopeOverview `json:"scopes"`
}

type MemoryScopeOverview struct {
	Kind   string              `json:"kind"`
	Key    string              `json:"key,omitempty"`
	Label  string              `json:"label"`
	Recent []MemoryDocOverview `json:"recent"`
}

type MemoryDocOverview struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Summary   string    `json:"summary,omitempty"`
	Kind      string    `json:"kind,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

func externalRuntimeName(name string) bool {
	switch strings.TrimSpace(strings.ToLower(name)) {
	case "claude", "codex":
		return true
	default:
		return false
	}
}

func (a *App) prepareMemoryForTurn(ctx context.Context, turn *agent.Turn, precommitExplicit bool) {
	if a == nil || turn == nil || a.tools == nil {
		return
	}
	turn.MemoryBrief = a.memoryBrief(ctx)
}

type MemoryCommitCard struct {
	Status     string   `json:"status"`
	ScopeKind  string   `json:"scope_kind"`
	ScopeKey   string   `json:"scope_key,omitempty"`
	Title      string   `json:"title"`
	Body       string   `json:"body"`
	Kind       string   `json:"kind,omitempty"`
	Tags       []string `json:"tags,omitempty"`
	Reason     string   `json:"reason,omitempty"`
	Confidence string   `json:"confidence,omitempty"`
	MemoryID   string   `json:"memory_id,omitempty"`
	Notice     string   `json:"notice,omitempty"`
	Error      string   `json:"error,omitempty"`
	CreatedBy  string   `json:"created_by,omitempty"`
}

type memoryProposalFence struct {
	ScopeKind  string   `json:"scope_kind"`
	ScopeKey   string   `json:"scope_key"`
	Title      string   `json:"title"`
	Body       string   `json:"body"`
	Content    string   `json:"content"`
	Summary    string   `json:"summary"`
	Kind       string   `json:"kind"`
	Tags       []string `json:"tags"`
	Reason     string   `json:"reason"`
	Confidence string   `json:"confidence"`
}

type processedMemoryOutput struct {
	Content      string
	Reasoning    string
	Attachments  []msg.Attachment
	MemoryNotice string
}

func (a *App) processAssistantMemoryOutput(ctx context.Context, turn *agent.Turn, content, reasoning string) processedMemoryOutput {
	out := processedMemoryOutput{Content: content, Reasoning: reasoning}
	if a == nil || a.tools == nil || turn == nil {
		return out
	}
	cleaned, proposals := extractMemoryProposalFences(content)
	if len(proposals) == 0 {
		return out
	}
	out.Content = strings.TrimSpace(cleaned)
	for _, proposal := range proposals {
		card := a.commitAgentMemoryProposal(ctx, turn, proposal)
		if card.Notice != "" {
			out.MemoryNotice = strings.TrimSpace(strings.Join([]string{out.MemoryNotice, card.Notice}, "\n"))
		}
		raw, _ := json.Marshal(card)
		out.Attachments = append(out.Attachments, msg.Attachment{
			Kind:  "memory_commit",
			Label: card.Title,
			MIME:  "application/vnd.sumi.memory+json",
			Data:  string(raw),
		})
	}
	return out
}

func (a *App) processAssistantMemoryInSession(ctx context.Context, turn *agent.Turn, s *session.Session, baseline int) {
	if s == nil || baseline < 0 || baseline >= len(s.Messages) {
		return
	}
	for i := baseline; i < len(s.Messages); i++ {
		if s.Messages[i].Role != "assistant" {
			continue
		}
		processed := a.processAssistantMemoryOutput(ctx, turn, s.Messages[i].Content, s.Messages[i].Reasoning)
		s.Messages[i].Content = processed.Content
		s.Messages[i].Reasoning = processed.Reasoning
		if len(processed.Attachments) > 0 {
			s.Messages[i].Attachments = append(s.Messages[i].Attachments, processed.Attachments...)
		}
		if processed.MemoryNotice != "" {
			turn.MemoryNotice = strings.TrimSpace(strings.Join([]string{turn.MemoryNotice, processed.MemoryNotice}, "\n"))
		}
	}
	if notice := strings.TrimSpace(turn.MemoryNotice); notice != "" {
		turn.MemoryBrief = a.memoryBrief(ctx)
	}
}

func assistantAttachments(added []msg.Message, kind string) []msg.Attachment {
	var out []msg.Attachment
	for _, m := range added {
		if m.Role != "assistant" {
			continue
		}
		for _, a := range m.Attachments {
			if kind == "" || a.Kind == kind {
				out = append(out, a)
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (a *App) commitAgentMemoryProposal(ctx context.Context, turn *agent.Turn, proposal memoryProposalFence) MemoryCommitCard {
	scopeKind := strings.TrimSpace(proposal.ScopeKind)
	scopeKey := strings.TrimSpace(proposal.ScopeKey)
	if scopeKind == "" {
		scopeKind, scopeKey = firstMemoryScope(ctx)
	}
	body := strings.TrimSpace(firstNonEmpty(proposal.Body, proposal.Content))
	card := MemoryCommitCard{
		Status:     "failed",
		ScopeKind:  scopeKind,
		ScopeKey:   scopeKey,
		Title:      strings.TrimSpace(proposal.Title),
		Body:       body,
		Kind:       firstNonEmpty(proposal.Kind, "note"),
		Tags:       proposal.Tags,
		Reason:     strings.TrimSpace(proposal.Reason),
		Confidence: firstNonEmpty(proposal.Confidence, "high"),
		CreatedBy:  strings.TrimSpace(turn.AgentID),
	}
	if card.Title == "" {
		card.Title = rememberTitle(body)
	}
	if body == "" {
		card.Error = "memory proposal body is required"
		return card
	}
	if reason := sensitiveMemoryReason(card.Title + "\n" + body); reason != "" {
		card.Error = "refusing to remember sensitive memory: " + reason
		return card
	}
	args := map[string]any{
		"scope_kind":        scopeKind,
		"scope_key":         scopeKey,
		"title":             card.Title,
		"body":              body,
		"summary":           strings.TrimSpace(proposal.Summary),
		"kind":              card.Kind,
		"tags":              card.Tags,
		"source_space_id":   strings.TrimSpace(turn.SpaceID),
		"source_message_id": strings.TrimSpace(turn.ParentMessageID),
		"created_by":        card.CreatedBy,
		"confidence":        card.Confidence,
	}
	raw, _ := json.Marshal(args)
	notice, err := a.tools.Run(ctx, "write_memory", raw)
	if err != nil {
		card.Error = err.Error()
		return card
	}
	card.Status = "remembered"
	card.Notice = strings.TrimSpace(notice)
	card.MemoryID = memoryIDFromNotice(card.Notice)
	return card
}

func extractMemoryProposalFences(content string) (string, []memoryProposalFence) {
	var proposals []memoryProposalFence
	cleaned := memoryFenceRE.ReplaceAllStringFunc(content, func(block string) string {
		lang, body, ok := splitFence(block)
		if !ok || strings.TrimSpace(lang) != "sumi-memory" {
			return block
		}
		var proposal memoryProposalFence
		if err := json.Unmarshal([]byte(body), &proposal); err != nil {
			return block
		}
		proposals = append(proposals, proposal)
		return ""
	})
	return strings.TrimSpace(cleaned), proposals
}

func splitFence(block string) (string, string, bool) {
	matches := memoryFenceRE.FindStringSubmatch(block)
	if len(matches) != 3 {
		return "", "", false
	}
	return strings.TrimSpace(matches[1]), strings.TrimSpace(matches[2]), true
}

func memoryIDFromNotice(notice string) string {
	fields := strings.Fields(notice)
	for i, field := range fields {
		if field == "memory" && i+1 < len(fields) {
			return strings.Trim(fields[i+1], ".:,;")
		}
	}
	return ""
}

func sensitiveMemoryReason(s string) string {
	lower := strings.ToLower(s)
	for _, marker := range []string{
		"sk_agent_",
		"sk_machine_",
		"bearer ",
		"password=",
		"passwd=",
		"cookie:",
		"set-cookie:",
		"webhook",
	} {
		if strings.Contains(lower, marker) {
			return marker
		}
	}
	for _, marker := range []string{"token", "secret", "api_key", "apikey", "access_key"} {
		if strings.Contains(lower, marker+"=") || strings.Contains(lower, marker+":") {
			return marker
		}
	}
	return ""
}

func (a *App) commitExplicitRemember(ctx context.Context, turn *agent.Turn) string {
	input := strings.TrimSpace(command.InputFrom(ctx))
	if input == "" || !explicitRememberRequest(input) {
		return ""
	}
	scopeKind, scopeKey := firstMemoryScope(ctx)
	args := map[string]any{
		"scope_kind":         scopeKind,
		"scope_key":          scopeKey,
		"title":              rememberTitle(input),
		"body":               rememberBody(input),
		"kind":               "preference",
		"authorization_text": input,
		"source_space_id":    strings.TrimSpace(turn.SpaceID),
		"source_message_id":  strings.TrimSpace(turn.ParentMessageID),
		"confidence":         "high",
	}
	raw, _ := json.Marshal(args)
	out, err := a.tools.Run(ctx, "remember_memory", raw)
	if err != nil {
		return "Sumi did not change long-term memory: " + err.Error()
	}
	return strings.TrimSpace(out)
}

func (a *App) memoryBrief(ctx context.Context) string {
	var chunks []string
	seen := map[string]bool{}
	for i, sc := range command.MemoryScopesFrom(ctx) {
		kind := strings.TrimSpace(sc.Kind)
		key := strings.TrimSpace(sc.Key)
		if kind == "" {
			continue
		}
		scopeID := kind + "\x00" + key
		if seen[scopeID] {
			continue
		}
		seen[scopeID] = true
		limit := 3
		if i == 0 {
			limit = 5
		}
		raw, _ := json.Marshal(map[string]any{
			"scope_kind": kind,
			"scope_key":  key,
			"limit":      limit,
		})
		out, err := a.tools.Run(ctx, "read_memory", raw)
		if err != nil {
			continue
		}
		out = strings.TrimSpace(out)
		if out == "" || strings.Contains(out, "no memory docs") {
			continue
		}
		chunks = append(chunks, out)
		if len(chunks) >= 4 {
			break
		}
	}
	return strings.Join(chunks, "\n\n")
}

func (a *App) MemoryOverview(scopes []command.MemoryScope, limit int) MemoryOverview {
	if a == nil {
		return MemoryOverview{Scopes: []MemoryScopeOverview{}}
	}
	if limit <= 0 {
		limit = 3
	}
	out := MemoryOverview{Scopes: make([]MemoryScopeOverview, 0, len(scopes))}
	seen := map[string]bool{}
	for _, sc := range scopes {
		kind := strings.TrimSpace(sc.Kind)
		key := strings.TrimSpace(sc.Key)
		if kind == "" {
			continue
		}
		scopeID := kind + "\x00" + key
		if seen[scopeID] {
			continue
		}
		seen[scopeID] = true
		out.Scopes = append(out.Scopes, MemoryScopeOverview{
			Kind:   kind,
			Key:    memoryScopePublicKey(kind, key),
			Label:  memoryScopeLabel(kind, key),
			Recent: a.recentMemoryDocs(kind, key, limit),
		})
	}
	return out
}

func (a *App) recentMemoryDocs(kind, key string, limit int) []MemoryDocOverview {
	dir := filepath.Join(a.cfg.MemoryDir(), sanitizeMemoryPath(kind))
	if strings.TrimSpace(key) != "" {
		dir = filepath.Join(dir, sanitizeMemoryPath(key))
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return []MemoryDocOverview{}
	}
	docs := make([]MemoryDocOverview, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".md" {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		doc := memoryDocOverviewFromFile(entry.Name(), string(data), info.ModTime().UTC())
		docs = append(docs, doc)
	}
	sort.Slice(docs, func(i, j int) bool {
		return docs[i].UpdatedAt.After(docs[j].UpdatedAt)
	})
	if len(docs) > limit {
		docs = docs[:limit]
	}
	return docs
}

func memoryDocOverviewFromFile(name, raw string, mod time.Time) MemoryDocOverview {
	body := raw
	head := ""
	if strings.HasPrefix(body, "---\n") {
		rest := body[4:]
		if i := strings.Index(rest, "\n---\n"); i >= 0 {
			head = rest[:i]
			body = rest[i+5:]
		}
	}
	meta := parseMemoryFrontmatter(head)
	title := strings.TrimSpace(meta["title"])
	if title == "" {
		title = firstMemoryHeading(body)
	}
	if title == "" {
		title = strings.TrimSuffix(name, filepath.Ext(name))
	}
	summary := strings.TrimSpace(meta["summary"])
	if summary == "" {
		summary = summarizeMemoryText(body, 160)
	}
	updatedAt := mod
	if parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(meta["updated_at"])); err == nil {
		updatedAt = parsed
	}
	return MemoryDocOverview{
		ID:        strings.TrimSuffix(name, filepath.Ext(name)),
		Title:     title,
		Summary:   summary,
		Kind:      strings.TrimSpace(meta["kind"]),
		UpdatedAt: updatedAt,
	}
}

func parseMemoryFrontmatter(head string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(head, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"`)
		if key != "" {
			out[key] = value
		}
	}
	return out
}

func firstMemoryHeading(body string) string {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "# "))
		}
	}
	return ""
}

func summarizeMemoryText(s string, n int) string {
	text := strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
	if len([]rune(text)) <= n {
		return text
	}
	runes := []rune(text)
	return strings.TrimSpace(string(runes[:n-1])) + "…"
}

func sanitizeMemoryPath(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "_"
	}
	r := strings.NewReplacer("/", "_", "\\", "_", ":", "_", "\n", "_", "\t", "_")
	return r.Replace(s)
}

func memoryScopeLabel(kind, key string) string {
	if strings.TrimSpace(kind) == "workspace" {
		return "workspace"
	}
	if strings.TrimSpace(key) == "" {
		return strings.TrimSpace(kind)
	}
	return strings.TrimSpace(kind) + ":" + strings.TrimSpace(key)
}

func memoryScopePublicKey(kind, key string) string {
	if strings.TrimSpace(kind) == "workspace" {
		return ""
	}
	return strings.TrimSpace(key)
}

func firstMemoryScope(ctx context.Context) (string, string) {
	if scopes := command.MemoryScopesFrom(ctx); len(scopes) > 0 {
		return strings.TrimSpace(scopes[0].Kind), strings.TrimSpace(scopes[0].Key)
	}
	if p := strings.TrimSpace(command.PersonaFrom(ctx)); p != "" {
		return "persona", p
	}
	return "global", ""
}

func explicitRememberRequest(s string) bool {
	lower := strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(s)), " "))
	for _, phrase := range []string{
		"记住",
		"记忆",
		"记得",
		"以后都",
		"以后请",
		"以后要",
		"长期偏好",
		"作为长期",
		"you should remember",
		"please remember",
		"remember that",
		"remember i",
		"from now on",
	} {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}

func rememberTitle(input string) string {
	body := rememberBody(input)
	runes := []rune(body)
	if len(runes) > 42 {
		body = string(runes[:42]) + "..."
	}
	if strings.TrimSpace(body) == "" {
		return "User memory"
	}
	return body
}

func rememberBody(input string) string {
	body := strings.TrimSpace(input)
	if explicitRememberRequest(body) {
		if left, right, ok := strings.Cut(body, "："); ok && explicitRememberRequest(left) && strings.TrimSpace(right) != "" {
			body = strings.TrimSpace(right)
		} else if left, right, ok := strings.Cut(body, ":"); ok && explicitRememberRequest(left) && strings.TrimSpace(right) != "" {
			body = strings.TrimSpace(right)
		}
	}
	prefixes := []string{
		"请记忆一下", "帮我记忆一下", "你记忆一下", "记忆一下", "请记忆", "帮我记忆", "你记忆", "记忆",
		"请记住", "帮我记住", "你记住", "记住", "记得",
		"please remember that", "please remember", "remember that",
	}
	lower := strings.ToLower(body)
	for _, p := range prefixes {
		lp := strings.ToLower(p)
		if strings.HasPrefix(lower, lp) {
			body = strings.TrimSpace(body[len(p):])
			break
		}
	}
	body = strings.TrimLeft(body, "：:，,。. ")
	if body == "" {
		body = strings.TrimSpace(input)
	}
	return fmt.Sprintf("User explicitly asked Sumi to remember: %s", body)
}
