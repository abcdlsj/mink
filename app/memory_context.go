package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
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

type MemoryDocDetail struct {
	ID              string    `json:"id"`
	ScopeKind       string    `json:"scope_kind"`
	ScopeKey        string    `json:"scope_key,omitempty"`
	ScopeLabel      string    `json:"scope_label"`
	Title           string    `json:"title"`
	Body            string    `json:"body"`
	Summary         string    `json:"summary,omitempty"`
	Kind            string    `json:"kind,omitempty"`
	Tags            []string  `json:"tags,omitempty"`
	Source          string    `json:"source,omitempty"`
	SourceSpaceID   string    `json:"source_space_id,omitempty"`
	SourceMessageID string    `json:"source_message_id,omitempty"`
	CreatedBy       string    `json:"created_by,omitempty"`
	Confidence      string    `json:"confidence,omitempty"`
	CreatedAt       time.Time `json:"created_at,omitempty"`
	UpdatedAt       time.Time `json:"updated_at"`
	ExpiresAt       time.Time `json:"expires_at,omitempty"`
}

type MemoryUpdateInput struct {
	ScopeKind  string `json:"scope_kind"`
	ScopeKey   string `json:"scope_key,omitempty"`
	ID         string `json:"id"`
	Title      string `json:"title"`
	Body       string `json:"body"`
	Summary    string `json:"summary,omitempty"`
	Kind       string `json:"kind,omitempty"`
	Confidence string `json:"confidence,omitempty"`
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
	Action     string   `json:"action,omitempty"`
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
	Action     string   `json:"action"`
	ID         string   `json:"id"`
	MemoryID   string   `json:"memory_id"`
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

func (p *memoryProposalFence) UnmarshalJSON(data []byte) error {
	type rawProposal struct {
		Action     string          `json:"action"`
		ID         string          `json:"id"`
		MemoryID   string          `json:"memory_id"`
		ScopeKind  string          `json:"scope_kind"`
		ScopeKey   string          `json:"scope_key"`
		Title      string          `json:"title"`
		Body       string          `json:"body"`
		Content    string          `json:"content"`
		Summary    string          `json:"summary"`
		Kind       string          `json:"kind"`
		Tags       []string        `json:"tags"`
		Reason     string          `json:"reason"`
		Confidence json.RawMessage `json:"confidence"`
	}
	var raw rawProposal
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*p = memoryProposalFence{
		Action:     raw.Action,
		ID:         raw.ID,
		MemoryID:   raw.MemoryID,
		ScopeKind:  raw.ScopeKind,
		ScopeKey:   raw.ScopeKey,
		Title:      raw.Title,
		Body:       raw.Body,
		Content:    raw.Content,
		Summary:    raw.Summary,
		Kind:       raw.Kind,
		Tags:       raw.Tags,
		Reason:     raw.Reason,
		Confidence: decodeMemoryConfidence(raw.Confidence),
	}
	return nil
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
		Action:     normalizedMemoryProposalAction(proposal),
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
	if memoryProposalDeletes(proposal) {
		card.Error = "memory delete proposals are not supported; use !memory delete with a memory id"
		return card
	}
	if reason := sensitiveMemoryReason(card.Title + "\n" + body); reason != "" {
		card.Error = "refusing to remember sensitive memory: " + reason
		return card
	}
	if card.Action == "update" {
		memoryID := strings.TrimSpace(firstNonEmpty(proposal.ID, proposal.MemoryID))
		if memoryID == "" {
			card.Error = "memory update proposals require id"
			return card
		}
		args := map[string]any{
			"scope_kind": scopeKind,
			"scope_key":  scopeKey,
			"id":         memoryID,
			"title":      card.Title,
			"body":       body,
			"summary":    strings.TrimSpace(proposal.Summary),
			"kind":       card.Kind,
			"confidence": card.Confidence,
		}
		raw, _ := json.Marshal(args)
		notice, err := a.tools.Run(ctx, "update_memory", raw)
		if err != nil {
			card.Error = err.Error()
			return card
		}
		card.Status = "updated"
		card.Notice = strings.TrimSpace(notice)
		card.MemoryID = memoryIDFromNotice(card.Notice)
		if card.MemoryID == "" {
			card.MemoryID = memoryID
		}
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

func normalizedMemoryProposalAction(p memoryProposalFence) string {
	action := strings.ToLower(strings.TrimSpace(p.Action))
	switch action {
	case "update", "write", "remember", "create":
		if action == "write" || action == "remember" || action == "create" {
			return "remember"
		}
		return action
	default:
		if strings.TrimSpace(firstNonEmpty(p.ID, p.MemoryID)) != "" {
			return "update"
		}
		return "remember"
	}
}

func decodeMemoryConfidence(raw json.RawMessage) string {
	raw = bytesTrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return strings.TrimSpace(s)
	}
	var n float64
	if err := json.Unmarshal(raw, &n); err == nil {
		switch {
		case n >= 0.8:
			return "high"
		case n >= 0.45:
			return "medium"
		default:
			return "low"
		}
	}
	return ""
}

func bytesTrimSpace(in []byte) []byte {
	return []byte(strings.TrimSpace(string(in)))
}

func memoryProposalDeletes(p memoryProposalFence) bool {
	kind := strings.ToLower(strings.TrimSpace(p.Kind))
	return kind == "delete" || kind == "deletion" || kind == "remove"
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

func (a *App) MemoryDoc(kind, key, id string) (MemoryDocDetail, error) {
	if a == nil {
		return MemoryDocDetail{}, fmt.Errorf("app not initialized")
	}
	kind = strings.TrimSpace(kind)
	key = strings.TrimSpace(key)
	id = strings.TrimSpace(id)
	if kind == "" || id == "" {
		return MemoryDocDetail{}, fmt.Errorf("memory scope and id required")
	}
	doc, err := a.loadMemoryDoc(kind, key, id)
	if err != nil {
		return MemoryDocDetail{}, err
	}
	return memoryDocDetailFromFile(kind, key, doc.id, doc.raw, doc.mod), nil
}

func (a *App) UpdateMemoryDoc(in MemoryUpdateInput) (MemoryDocDetail, error) {
	if a == nil {
		return MemoryDocDetail{}, fmt.Errorf("app not initialized")
	}
	kind := strings.TrimSpace(in.ScopeKind)
	key := strings.TrimSpace(in.ScopeKey)
	id := strings.TrimSpace(in.ID)
	if kind == "" || id == "" {
		return MemoryDocDetail{}, fmt.Errorf("memory scope and id required")
	}
	title := strings.TrimSpace(in.Title)
	body := strings.TrimSpace(in.Body)
	if title == "" || body == "" {
		return MemoryDocDetail{}, fmt.Errorf("memory title and body required")
	}
	loaded, err := a.loadMemoryDoc(kind, key, id)
	if err != nil {
		return MemoryDocDetail{}, err
	}
	detail := memoryDocDetailFromFile(kind, key, loaded.id, loaded.raw, loaded.mod)
	detail.Title = title
	detail.Body = body
	detail.Summary = strings.TrimSpace(in.Summary)
	if detail.Summary == "" {
		detail.Summary = summarizeMemoryText(body, 160)
	}
	detail.Kind = strings.TrimSpace(in.Kind)
	if detail.Kind == "" {
		detail.Kind = "note"
	}
	detail.Confidence = normalizeMemoryConfidence(strings.TrimSpace(in.Confidence))
	if detail.Confidence == "" {
		detail.Confidence = "medium"
	}
	detail.UpdatedAt = time.Now().UTC()
	if err := writeMemoryDocFile(loaded.path, detail); err != nil {
		return MemoryDocDetail{}, err
	}
	return detail, nil
}

type loadedMemoryDoc struct {
	id   string
	path string
	raw  string
	mod  time.Time
}

func (a *App) loadMemoryDoc(kind, key, id string) (loadedMemoryDoc, error) {
	dir := filepath.Join(a.cfg.MemoryDir(), sanitizeMemoryPath(kind))
	if strings.TrimSpace(key) != "" {
		dir = filepath.Join(dir, sanitizeMemoryPath(key))
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return loadedMemoryDoc{}, fmt.Errorf("memory %s not found in %s", id, memoryScopeLabel(kind, key))
		}
		return loadedMemoryDoc{}, err
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".md" {
			continue
		}
		fileID := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		if fileID != id {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return loadedMemoryDoc{}, err
		}
		info, err := entry.Info()
		if err != nil {
			return loadedMemoryDoc{}, err
		}
		return loadedMemoryDoc{id: fileID, path: path, raw: string(data), mod: info.ModTime().UTC()}, nil
	}
	return loadedMemoryDoc{}, fmt.Errorf("memory %s not found in %s", id, memoryScopeLabel(kind, key))
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

func memoryDocDetailFromFile(kind, key, id, raw string, mod time.Time) MemoryDocDetail {
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
	body = strings.TrimSpace(stripLeadingMemoryTitle(body, meta["title"]))
	detail := MemoryDocDetail{
		ID:              id,
		ScopeKind:       strings.TrimSpace(kind),
		ScopeKey:        memoryScopePublicKey(kind, key),
		ScopeLabel:      memoryScopeLabel(kind, key),
		Title:           strings.TrimSpace(meta["title"]),
		Body:            body,
		Summary:         strings.TrimSpace(meta["summary"]),
		Kind:            strings.TrimSpace(meta["kind"]),
		Tags:            parseMemoryTags(head),
		Source:          strings.TrimSpace(meta["source"]),
		SourceSpaceID:   strings.TrimSpace(meta["source_space_id"]),
		SourceMessageID: strings.TrimSpace(meta["source_message_id"]),
		CreatedBy:       strings.TrimSpace(meta["created_by"]),
		Confidence:      strings.TrimSpace(meta["confidence"]),
		UpdatedAt:       mod,
	}
	if detail.Title == "" {
		detail.Title = firstMemoryHeading(raw)
	}
	if detail.Title == "" {
		detail.Title = id
	}
	if detail.Body == "" {
		detail.Body = strings.TrimSpace(raw)
	}
	if detail.Summary == "" {
		detail.Summary = summarizeMemoryText(detail.Body, 160)
	}
	if detail.Kind == "" {
		detail.Kind = "note"
	}
	if detail.Confidence == "" {
		detail.Confidence = "medium"
	}
	if parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(meta["updated_at"])); err == nil {
		detail.UpdatedAt = parsed
	}
	if parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(meta["created_at"])); err == nil {
		detail.CreatedAt = parsed
	}
	if parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(meta["expires_at"])); err == nil {
		detail.ExpiresAt = parsed
	}
	return detail
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

func writeMemoryDocFile(path string, detail MemoryDocDetail) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "scope_kind: %s\n", quoteMemoryMeta(detail.ScopeKind))
	fmt.Fprintf(&b, "scope_key: %s\n", quoteMemoryMeta(detail.ScopeKey))
	fmt.Fprintf(&b, "title: %s\n", quoteMemoryMeta(detail.Title))
	fmt.Fprintf(&b, "kind: %s\n", quoteMemoryMeta(firstNonEmpty(detail.Kind, "note")))
	if detail.Source != "" {
		fmt.Fprintf(&b, "source: %s\n", quoteMemoryMeta(detail.Source))
	}
	if detail.SourceSpaceID != "" {
		fmt.Fprintf(&b, "source_space_id: %s\n", quoteMemoryMeta(detail.SourceSpaceID))
	}
	if detail.SourceMessageID != "" {
		fmt.Fprintf(&b, "source_message_id: %s\n", quoteMemoryMeta(detail.SourceMessageID))
	}
	if detail.CreatedBy != "" {
		fmt.Fprintf(&b, "created_by: %s\n", quoteMemoryMeta(detail.CreatedBy))
	}
	if detail.Confidence != "" {
		fmt.Fprintf(&b, "confidence: %s\n", quoteMemoryMeta(detail.Confidence))
	}
	if detail.Summary != "" {
		fmt.Fprintf(&b, "summary: %s\n", quoteMemoryMeta(detail.Summary))
	}
	if len(detail.Tags) > 0 {
		b.WriteString("tags:\n")
		for _, tag := range detail.Tags {
			fmt.Fprintf(&b, "  - %s\n", quoteMemoryMeta(tag))
		}
	}
	if !detail.CreatedAt.IsZero() {
		fmt.Fprintf(&b, "created_at: %s\n", detail.CreatedAt.Format(time.RFC3339Nano))
	}
	fmt.Fprintf(&b, "updated_at: %s\n", detail.UpdatedAt.Format(time.RFC3339Nano))
	if !detail.ExpiresAt.IsZero() {
		fmt.Fprintf(&b, "expires_at: %s\n", detail.ExpiresAt.Format(time.RFC3339Nano))
	}
	b.WriteString("---\n\n")
	b.WriteString("# ")
	b.WriteString(strings.TrimSpace(detail.Title))
	b.WriteString("\n\n")
	b.WriteString(strings.TrimSpace(detail.Body))
	if !strings.HasSuffix(detail.Body, "\n") {
		b.WriteByte('\n')
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.WriteString(b.String()); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return os.Rename(tmpPath, path)
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

func parseMemoryTags(head string) []string {
	var tags []string
	lines := strings.Split(head, "\n")
	for i := 0; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != "tags:" {
			continue
		}
		for i+1 < len(lines) {
			next := strings.TrimSpace(lines[i+1])
			if !strings.HasPrefix(next, "- ") {
				return tags
			}
			tag := strings.Trim(strings.TrimSpace(strings.TrimPrefix(next, "- ")), `"`)
			if unquoted, err := strconv.Unquote(strings.TrimSpace(strings.TrimPrefix(next, "- "))); err == nil {
				tag = unquoted
			}
			if tag != "" {
				tags = append(tags, tag)
			}
			i++
		}
	}
	return tags
}

func stripLeadingMemoryTitle(body, title string) string {
	body = strings.TrimSpace(body)
	title = strings.TrimSpace(title)
	if body == "" || title == "" {
		return body
	}
	lines := strings.Split(body, "\n")
	if len(lines) == 0 {
		return body
	}
	first := strings.TrimSpace(lines[0])
	if strings.TrimSpace(strings.TrimLeft(first, "#")) != title {
		return body
	}
	return strings.TrimSpace(strings.Join(lines[1:], "\n"))
}

func quoteMemoryMeta(s string) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\n", " ")
	return strconv.Quote(s)
}

func normalizeMemoryConfidence(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "low", "medium", "high":
		return strings.ToLower(strings.TrimSpace(s))
	default:
		return ""
	}
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
