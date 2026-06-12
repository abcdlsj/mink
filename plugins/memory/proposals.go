package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/abcdlsj/sumi/command"
)

func (s *store) propose(ctx context.Context, sc scope, in proposeArgs) (proposal, error) {
	content := strings.TrimSpace(firstNonEmpty(in.Content, in.Body))
	if content == "" {
		return proposal{}, fmt.Errorf("content is required")
	}
	reason := strings.TrimSpace(in.Reason)
	if reason == "" {
		return proposal{}, fmt.Errorf("reason is required")
	}
	p := proposal{
		ID:              fmt.Sprintf("memprop-%d", time.Now().UnixNano()),
		ScopeKind:       normalizeScope(sc).Kind,
		ScopeKey:        normalizeScope(sc).Key,
		Title:           blank(in.Title, summarize(content, 60)),
		Content:         content,
		Kind:            blank(in.Kind, "note"),
		Tags:            in.Tags,
		Source:          command.SourceFrom(ctx),
		SourceSpaceID:   strings.TrimSpace(in.SourceSpaceID),
		SourceMessageID: blank(in.SourceMessageID, command.ParentMessageFrom(ctx)),
		Reason:          reason,
		Confidence:      normalizeConfidence(in.Confidence),
		CreatedBy:       memoryCreatedBy(ctx, ""),
		CreatedAt:       time.Now().UTC(),
	}
	if err := s.saveProposal(p); err != nil {
		return proposal{}, err
	}
	return p, nil
}

func (s *store) saveProposal(p proposal) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := proposalDir(s.root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeFile(filepath.Join(dir, sanitize(p.ID)+".json"), data)
}

func (s *store) listProposals() ([]proposal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := proposalDir(s.root)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []proposal
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		p, err := loadProposal(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (s *store) popProposal(id string) (proposal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := filepath.Join(proposalDir(s.root), sanitize(id)+".json")
	p, err := loadProposal(path)
	if err != nil {
		return proposal{}, err
	}
	if err := os.Remove(path); err != nil {
		return proposal{}, err
	}
	return p, nil
}

func (s *store) rejectProposal(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := filepath.Join(proposalDir(s.root), sanitize(id)+".json")
	if err := os.Remove(path); err != nil {
		return err
	}
	return nil
}

func loadProposal(path string) (proposal, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return proposal{}, err
	}
	var p proposal
	if err := json.Unmarshal(data, &p); err != nil {
		return proposal{}, err
	}
	if p.ID == "" {
		p.ID = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	return p, nil
}

func (s *store) confirmProposal(ctx context.Context, id string) (doc, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return doc{}, fmt.Errorf("proposal id is required")
	}
	p, err := s.popProposal(id)
	if err != nil {
		return doc{}, err
	}
	d := doc{
		Title:           p.Title,
		Body:            p.Content,
		Summary:         summarize(p.Content, 140),
		Kind:            p.Kind,
		Tags:            p.Tags,
		Source:          p.Source,
		SourceSpaceID:   p.SourceSpaceID,
		SourceMessageID: p.SourceMessageID,
		CreatedBy:       firstNonEmpty(command.PersonaFrom(ctx), "user"),
		Confidence:      normalizeConfidence(p.Confidence),
		CreatedAt:       p.CreatedAt,
	}
	d.Tags = appendUnique(d.Tags, "confirmed")
	return s.put(ctx, scope{Kind: p.ScopeKind, Key: p.ScopeKey}, d)
}

func proposalDir(root string) string {
	return filepath.Join(root, "_proposals")
}

func renderProposals(items []proposal) string {
	if len(items) == 0 {
		return "no pending memory proposals"
	}
	var b strings.Builder
	b.WriteString("Memory proposals:\n")
	for _, p := range items {
		fmt.Fprintf(&b, "- %s [%s:%s] %s (%s confidence)\n", p.ID, p.ScopeKind, p.ScopeKey, p.Title, p.Confidence)
		if strings.TrimSpace(p.Reason) != "" {
			fmt.Fprintf(&b, "  reason: %s\n", strings.TrimSpace(p.Reason))
		}
		if strings.TrimSpace(p.Content) != "" {
			fmt.Fprintf(&b, "  content: %s\n", summarize(p.Content, 180))
		}
	}
	return strings.TrimSpace(b.String())
}

func renderProposalCreated(p proposal) string {
	return fmt.Sprintf("memory proposal %s created for %s:%s; confirm with !memory confirm %s or reject with !memory reject %s", p.ID, p.ScopeKind, p.ScopeKey, p.ID, p.ID)
}

func appendUnique(in []string, values ...string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range append(in, values...) {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
