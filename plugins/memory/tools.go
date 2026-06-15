package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/abcdlsj/sumi/command"
	"github.com/abcdlsj/sumi/tool"
)

type readTool struct{ s *store }

func (t *readTool) Name() string { return "read_memory" }
func (t *readTool) Desc() string { return "Read recent memory docs from a scope" }
func (t *readTool) Schema() map[string]any {
	return tool.ObjectSchema(
		tool.Prop("scope_kind", "string", "Scope kind"),
		tool.Prop("scope_key", "string", "Scope key"),
		tool.Prop("limit", "integer", "Maximum results"),
	)
}

func (t *readTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var in readArgs
	if err := decode("read_memory", args, &in); err != nil {
		return "", err
	}
	limit := defaultLimit(in.Limit, 3)
	sc := t.s.resolveReadScope(ctx, command.SourceFrom(ctx), in.ScopeKind, in.ScopeKey)
	docs, err := t.s.recent(ctx, sc, limit)
	if err != nil {
		return "", err
	}
	return render("recent", []scope{sc}, docs), nil
}

type searchTool struct{ s *store }

func (t *searchTool) Name() string { return "search_memory" }
func (t *searchTool) Desc() string { return "Search memory docs across scopes" }
func (t *searchTool) Schema() map[string]any {
	return tool.ObjectSchema(
		tool.Prop("query", "string", "Search query"),
		tool.Prop("scope_kind", "string", "Scope kind"),
		tool.Prop("scope_key", "string", "Scope key"),
		tool.Prop("limit", "integer", "Maximum results"),
		tool.Required("query"),
	)
}

func (t *searchTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var in searchArgs
	if err := decode("search_memory", args, &in); err != nil {
		return "", err
	}
	if strings.TrimSpace(in.Query) == "" {
		return "", fmt.Errorf("query is required")
	}
	limit := defaultLimit(in.Limit, 5)
	scopes := t.s.resolveSearchScopes(ctx, command.SourceFrom(ctx), in.ScopeKind, in.ScopeKey)
	docs, err := t.s.search(ctx, scopes, in.Query, limit)
	if err != nil {
		return "", err
	}
	return render("search", scopes, docs), nil
}

type writeTool struct{ s *store }

func (t *writeTool) Name() string { return "write_memory" }
func (t *writeTool) Desc() string { return "Write a memory doc" }
func (t *writeTool) Schema() map[string]any {
	return tool.ObjectSchema(
		tool.Prop("scope_kind", "string", "Scope kind"),
		tool.Prop("scope_key", "string", "Scope key"),
		tool.Prop("title", "string", "Title"),
		tool.Prop("body", "string", "Body"),
		tool.Prop("summary", "string", "Summary"),
		tool.Prop("kind", "string", "Memory kind"),
		tool.Prop("source_space_id", "string", "Source Space id"),
		tool.Prop("source_message_id", "string", "Source message id"),
		tool.Prop("created_by", "string", "Actor that committed the memory"),
		tool.Prop("confidence", "string", "Confidence: low, medium, or high"),
		tool.Prop("expires_at", "string", "Optional expiry timestamp in RFC3339"),
		tool.StringArrayProp("tags", "Tags"),
		tool.Required("title", "body"),
	)
}

func (t *writeTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var in writeArgs
	if err := decode("write_memory", args, &in); err != nil {
		return "", err
	}
	if strings.TrimSpace(in.Title) == "" || strings.TrimSpace(in.Body) == "" {
		return "", fmt.Errorf("title and body are required")
	}
	sc := t.s.resolveWriteScope(ctx, command.SourceFrom(ctx), in.ScopeKind, in.ScopeKey)
	d, err := t.s.put(ctx, sc, memoryDocFromWrite(ctx, in))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("saved memory %s in %s:%s", d.ID, d.ScopeKind, d.ScopeKey), nil
}

type rememberTool struct{ s *store }

func (t *rememberTool) Name() string { return "remember_memory" }
func (t *rememberTool) Desc() string {
	return "Write a memory doc only when the current user explicitly asked to remember it"
}
func (t *rememberTool) Schema() map[string]any {
	return tool.ObjectSchema(
		tool.Prop("scope_kind", "string", "Scope kind"),
		tool.Prop("scope_key", "string", "Scope key"),
		tool.Prop("title", "string", "Title"),
		tool.Prop("body", "string", "Memory body"),
		tool.Prop("summary", "string", "Summary"),
		tool.Prop("kind", "string", "Memory kind such as preference, fact, convention, decision, or note"),
		tool.Prop("authorization_text", "string", "Exact current user text that explicitly authorizes remembering this"),
		tool.Prop("source_space_id", "string", "Source Space id"),
		tool.Prop("source_message_id", "string", "Source message id"),
		tool.Prop("confidence", "string", "Confidence: low, medium, or high"),
		tool.StringArrayProp("tags", "Tags"),
		tool.Required("title", "body", "authorization_text"),
	)
}

func (t *rememberTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var in rememberArgs
	if err := decode("remember_memory", args, &in); err != nil {
		return "", err
	}
	if strings.TrimSpace(in.Title) == "" || strings.TrimSpace(in.Body) == "" {
		return "", fmt.Errorf("title and body are required")
	}
	if err := validateHumanAuthorizedMemory(ctx, in); err != nil {
		return "", err
	}
	sc := t.s.resolveWriteScope(ctx, command.SourceFrom(ctx), in.ScopeKind, in.ScopeKey)
	d, err := t.s.put(ctx, sc, memoryDocFromRemember(ctx, in))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Remembered memory %s in %s:%s. To undo: !memory delete %s:%s %s", d.ID, d.ScopeKind, d.ScopeKey, d.ScopeKind, d.ScopeKey, d.ID), nil
}

type proposeTool struct{ s *store }

func (t *proposeTool) Name() string { return "propose_memory" }
func (t *proposeTool) Desc() string {
	return "Create a pending memory proposal for human confirmation; does not write long-term memory"
}
func (t *proposeTool) Schema() map[string]any {
	return tool.ObjectSchema(
		tool.Prop("scope_kind", "string", "Scope kind"),
		tool.Prop("scope_key", "string", "Scope key"),
		tool.Prop("title", "string", "Short candidate title"),
		tool.Prop("content", "string", "Candidate memory content"),
		tool.Prop("kind", "string", "Memory kind such as preference, fact, convention, decision, or note"),
		tool.Prop("reason", "string", "Why this should become long-term memory"),
		tool.Prop("confidence", "string", "Confidence: low, medium, or high"),
		tool.Prop("source_space_id", "string", "Source Space id"),
		tool.Prop("source_message_id", "string", "Source message id"),
		tool.StringArrayProp("tags", "Tags"),
		tool.Required("content", "reason"),
	)
}

func (t *proposeTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var in proposeArgs
	if err := decode("propose_memory", args, &in); err != nil {
		return "", err
	}
	sc := t.s.resolveWriteScope(ctx, command.SourceFrom(ctx), in.ScopeKind, in.ScopeKey)
	p, err := t.s.propose(ctx, sc, in)
	if err != nil {
		return "", err
	}
	return renderProposalCreated(p), nil
}

type deleteTool struct{ s *store }

func (t *deleteTool) Name() string { return "delete_memory" }
func (t *deleteTool) Desc() string { return "Delete a committed memory doc by id" }
func (t *deleteTool) Schema() map[string]any {
	return tool.ObjectSchema(
		tool.Prop("scope_kind", "string", "Scope kind"),
		tool.Prop("scope_key", "string", "Scope key"),
		tool.Prop("id", "string", "Memory id"),
		tool.Required("id"),
	)
}

func (t *deleteTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var in deleteArgs
	if err := decode("delete_memory", args, &in); err != nil {
		return "", err
	}
	sc := t.s.resolveWriteScope(ctx, command.SourceFrom(ctx), in.ScopeKind, in.ScopeKey)
	d, err := t.s.delete(ctx, sc, in.ID)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("deleted memory %s from %s:%s", d.ID, d.ScopeKind, d.ScopeKey), nil
}

func memoryDocFromWrite(ctx context.Context, in writeArgs) doc {
	return doc{
		Title:           strings.TrimSpace(in.Title),
		Body:            strings.TrimSpace(in.Body),
		Summary:         blank(in.Summary, summarize(in.Body, 140)),
		Kind:            blank(in.Kind, "note"),
		Tags:            in.Tags,
		Source:          command.SourceFrom(ctx),
		SourceSpaceID:   strings.TrimSpace(in.SourceSpaceID),
		SourceMessageID: blank(in.SourceMessageID, command.ParentMessageFrom(ctx)),
		CreatedBy:       memoryCreatedBy(ctx, in.CreatedBy),
		Confidence:      normalizeConfidence(in.Confidence),
		ExpiresAt:       parseExpiresAt(in.ExpiresAt),
	}
}

func memoryDocFromRemember(ctx context.Context, in rememberArgs) doc {
	return memoryDocFromWrite(ctx, writeArgs{
		ScopeKind:       in.ScopeKind,
		ScopeKey:        in.ScopeKey,
		Title:           in.Title,
		Body:            in.Body,
		Summary:         in.Summary,
		Kind:            in.Kind,
		Tags:            in.Tags,
		SourceSpaceID:   in.SourceSpaceID,
		SourceMessageID: in.SourceMessageID,
		CreatedBy:       "user",
		Confidence:      blank(in.Confidence, "high"),
	})
}

func validateHumanAuthorizedMemory(ctx context.Context, in rememberArgs) error {
	auth := strings.TrimSpace(in.AuthorizationText)
	if auth == "" {
		return fmt.Errorf("authorization_text is required")
	}
	current := strings.TrimSpace(command.InputFrom(ctx))
	if current == "" {
		return fmt.Errorf("remember_memory requires current user input for authorization")
	}
	if !containsNormalized(current, auth) {
		return fmt.Errorf("authorization_text must be copied from the current user message")
	}
	if !explicitRememberAuthorization(auth) {
		return fmt.Errorf("authorization_text does not explicitly ask to remember a long-term preference or fact")
	}
	if reason := sensitiveMemoryReason(in.Title + "\n" + in.Body + "\n" + auth); reason != "" {
		return fmt.Errorf("refusing to remember sensitive memory: %s", reason)
	}
	return nil
}

func containsNormalized(haystack, needle string) bool {
	h := strings.ToLower(strings.Join(strings.Fields(haystack), " "))
	n := strings.ToLower(strings.Join(strings.Fields(needle), " "))
	return n != "" && strings.Contains(h, n)
}

func explicitRememberAuthorization(s string) bool {
	lower := strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(s)), " "))
	phrases := []string{
		"记住",
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
	}
	for _, phrase := range phrases {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}

func sensitiveMemoryReason(s string) string {
	lower := strings.ToLower(s)
	credentialMarkers := []string{
		"sk_agent_",
		"sk_machine_",
		"bearer ",
		"password=",
		"passwd=",
		"cookie:",
		"set-cookie:",
		"webhook",
	}
	for _, marker := range credentialMarkers {
		if strings.Contains(lower, marker) {
			return marker
		}
	}
	tokenLike := []string{"token", "secret", "api_key", "apikey", "access_key"}
	for _, marker := range tokenLike {
		if strings.Contains(lower, marker+"=") || strings.Contains(lower, marker+":") {
			return marker
		}
	}
	return ""
}

func memoryCreatedBy(ctx context.Context, explicit string) string {
	if v := strings.TrimSpace(explicit); v != "" {
		return v
	}
	if v := command.PersonaFrom(ctx); v != "" {
		return v
	}
	return "user"
}

func normalizeConfidence(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "low", "medium", "high":
		return strings.ToLower(strings.TrimSpace(s))
	default:
		return "medium"
	}
}

func parseExpiresAt(s string) time.Time {
	if strings.TrimSpace(s) == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(s))
	if err != nil {
		return time.Time{}
	}
	return t
}
