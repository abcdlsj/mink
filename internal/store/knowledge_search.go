package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

func (s *Store) SearchKnowledge(ctx context.Context, params KnowledgeSearchParams) (KnowledgeSearchOutput, error) {
	query, err := normalizeKnowledgeSearchQuery(params.Query)
	if err != nil || params.Now.IsZero() || params.Limit > knowledgeSearchMaxLimit {
		return KnowledgeSearchOutput{}, ErrKnowledgeSearchInvalid
	}
	limit := params.Limit
	if limit == 0 {
		limit = knowledgeSearchDefaultLimit
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return KnowledgeSearchOutput{}, fmt.Errorf("begin knowledge search: %w", err)
	}
	defer tx.Rollback()
	actor, runtime, err := knowledgeSearchAuthentication(ctx, tx, params, params.Now)
	if err != nil {
		return KnowledgeSearchOutput{}, err
	}
	state, err := readKnowledgeIndexState(ctx, tx)
	if err != nil {
		return KnowledgeSearchOutput{}, err
	}
	output := KnowledgeSearchOutput{Status: state.Status}
	if state.Status != KnowledgeIndexReady {
		return output, nil
	}
	available, err := knowledgeSearchFTSAvailable(ctx, tx)
	if err != nil {
		if errors.Is(classifyKnowledgeSearchError(err), errKnowledgeSearchCorrupt) {
			return KnowledgeSearchOutput{Status: KnowledgeIndexDegraded}, nil
		}
		return KnowledgeSearchOutput{}, fmt.Errorf("read knowledge search fts: %w", err)
	}
	if !available {
		return KnowledgeSearchOutput{Status: KnowledgeIndexDegraded}, nil
	}
	candidates, err := listKnowledgeSearchCandidates(ctx, tx, query)
	if err != nil {
		if errors.Is(err, errKnowledgeSearchCorrupt) {
			return KnowledgeSearchOutput{Status: KnowledgeIndexDegraded}, nil
		}
		return KnowledgeSearchOutput{}, err
	}
	var aggregate int
	for _, candidate := range candidates {
		document, visible, err := currentKnowledgeSearchDocument(ctx, tx, actor, runtime, candidate, params.Now)
		if err != nil {
			if errors.Is(err, errKnowledgeSearchCorrupt) {
				return KnowledgeSearchOutput{Status: KnowledgeIndexDegraded}, nil
			}
			return KnowledgeSearchOutput{}, err
		}
		if !visible {
			continue
		}
		snippet, ok := knowledgeSearchSnippet(document.Body)
		if !ok {
			continue
		}
		if len(output.Results) >= int(limit) || aggregate+len(snippet) > knowledgeSearchAggregateMaxBytes {
			break
		}
		output.Results = append(output.Results, KnowledgeSearchResult{Source: document.Source, Snippet: snippet})
		aggregate += len(snippet)
	}
	return output, nil
}

func normalizeKnowledgeSearchQuery(raw string) (string, error) {
	if len(raw) == 0 || len(raw) > knowledgeSearchQueryMaxBytes || !utf8.ValidString(raw) {
		return "", ErrKnowledgeSearchInvalid
	}
	terms := strings.Fields(raw)
	if len(terms) == 0 || len(terms) > knowledgeSearchTermsMax {
		return "", ErrKnowledgeSearchInvalid
	}
	quoted := make([]string, len(terms))
	for index, term := range terms {
		if len(term) > knowledgeSearchTermMaxBytes {
			return "", ErrKnowledgeSearchInvalid
		}
		quoted[index] = `"` + strings.ReplaceAll(term, `"`, `""`) + `"`
	}
	return strings.Join(quoted, " AND "), nil
}

func knowledgeSearchAuthentication(ctx context.Context, tx *sql.Tx, params KnowledgeSearchParams, now time.Time) (Principal, AgentRuntimeAuthentication, error) {
	humanProvided := params.Human.Kind != "" || params.Human.ID != "" || params.Human.OrganizationID != ""
	agentProvided := params.Agent.Principal.Kind != "" || params.Agent.Principal.ID != "" || params.Agent.Principal.OrganizationID != "" || params.Agent.Proof.Valid()
	if humanProvided == agentProvided {
		return Principal{}, AgentRuntimeAuthentication{}, ErrKnowledgeSearchUnauthenticated
	}
	if humanProvided {
		if params.Human.Kind != "human" || !params.Human.Valid() {
			return Principal{}, AgentRuntimeAuthentication{}, ErrKnowledgeSearchUnauthenticated
		}
		if err := validatePrincipalInOrganization(ctx, tx, params.Human, params.Human.OrganizationID); err != nil {
			if errors.Is(err, ErrPrincipalNotFound) || errors.Is(err, ErrPermissionDenied) {
				return Principal{}, AgentRuntimeAuthentication{}, ErrKnowledgeSearchUnauthenticated
			}
			return Principal{}, AgentRuntimeAuthentication{}, err
		}
		return params.Human, AgentRuntimeAuthentication{}, nil
	}
	if !params.Agent.Valid() {
		return Principal{}, AgentRuntimeAuthentication{}, ErrKnowledgeSearchUnauthenticated
	}
	current, err := requireAgentRuntimeSession(ctx, tx, params.Agent.Proof, now)
	if err != nil {
		if errors.Is(err, ErrAgentRuntimeUnauthenticated) {
			return Principal{}, AgentRuntimeAuthentication{}, ErrKnowledgeSearchUnauthenticated
		}
		return Principal{}, AgentRuntimeAuthentication{}, err
	}
	if current.Principal != params.Agent.Principal {
		return Principal{}, AgentRuntimeAuthentication{}, ErrKnowledgeSearchUnauthenticated
	}
	return current.Principal, current, nil
}

func knowledgeSearchFTSAvailable(ctx context.Context, queryer knowledgeQueryer) (bool, error) {
	var schema string
	err := queryer.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'knowledge_fts'`).Scan(&schema)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if normalizeKnowledgeSchema(schema) != normalizeKnowledgeSchema(knowledgeFTSSchema) {
		return false, nil
	}
	var count int
	if err := queryer.QueryRowContext(ctx, `SELECT count(*) FROM knowledge_fts WHERE knowledge_fts MATCH ?`, "knowledgecapabilityprobe").Scan(&count); err != nil {
		return false, err
	}
	return true, nil
}

type knowledgeSearchCandidate struct {
	rowID    int64
	rank     float64
	source   KnowledgeSource
	revision [sha256.Size]byte
}

func classifyKnowledgeSearchError(err error) error {
	if errors.Is(err, errKnowledgeSearchCorrupt) {
		return errKnowledgeSearchCorrupt
	}
	var sqliteErr *sqlite.Error
	if errors.As(err, &sqliteErr) {
		switch sqliteErr.Code() & 0xff {
		case sqlite3.SQLITE_CORRUPT, sqlite3.SQLITE_NOTADB:
			return errKnowledgeSearchCorrupt
		}
	}
	return err
}

func listKnowledgeSearchCandidates(ctx context.Context, tx *sql.Tx, match string) ([]knowledgeSearchCandidate, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT rowid, source_kind, source_id, source_version, revision, bm25(knowledge_fts)
		FROM knowledge_fts
		WHERE knowledge_fts MATCH ?
		ORDER BY bm25(knowledge_fts), source_kind, source_id, source_version, rowid
		LIMIT ?
	`, match, knowledgeSearchCandidateLimit)
	if err != nil {
		return nil, fmt.Errorf("list knowledge search candidates: %w", classifyKnowledgeSearchError(err))
	}
	defer rows.Close()
	candidates := make([]knowledgeSearchCandidate, 0, knowledgeSearchCandidateLimit)
	for rows.Next() {
		var candidate knowledgeSearchCandidate
		var revision []byte
		if err := rows.Scan(&candidate.rowID, &candidate.source.Kind, &candidate.source.ID, &candidate.source.Version, &revision, &candidate.rank); err != nil {
			return nil, fmt.Errorf("scan knowledge search candidate: %w", classifyKnowledgeSearchError(err))
		}
		if len(revision) != sha256.Size || candidate.rowID <= 0 || math.IsNaN(candidate.rank) || math.IsInf(candidate.rank, 0) || validateKnowledgeSource(candidate.source) != nil {
			return nil, errKnowledgeSearchCorrupt
		}
		copy(candidate.revision[:], revision)
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate knowledge search candidates: %w", classifyKnowledgeSearchError(err))
	}
	return candidates, nil
}

func currentKnowledgeSearchDocument(ctx context.Context, tx *sql.Tx, actor Principal, runtime AgentRuntimeAuthentication, candidate knowledgeSearchCandidate, now time.Time) (KnowledgeSourceDocument, bool, error) {
	var projectionRevision []byte
	var rowID int64
	err := tx.QueryRowContext(ctx, `
		SELECT revision, fts_rowid FROM knowledge_projection_rows
		WHERE source_kind = ? AND source_id = ? AND source_version = ?
	`, candidate.source.Kind, candidate.source.ID, candidate.source.Version).Scan(&projectionRevision, &rowID)
	if errors.Is(err, sql.ErrNoRows) {
		return KnowledgeSourceDocument{}, false, nil
	}
	if err != nil {
		return KnowledgeSourceDocument{}, false, fmt.Errorf("read knowledge search projection: %w", err)
	}
	if rowID != candidate.rowID || len(projectionRevision) != sha256.Size || string(projectionRevision) != string(candidate.revision[:]) {
		return KnowledgeSourceDocument{}, false, errKnowledgeSearchCorrupt
	}
	document, found, err := readKnowledgeSourceDocument(ctx, tx, candidate.source)
	if err != nil || !found || document.Revision != candidate.revision {
		return KnowledgeSourceDocument{}, false, err
	}
	visible, err := knowledgeSearchSourceReadable(ctx, tx, actor, runtime, document, now)
	if err != nil || !visible {
		return KnowledgeSourceDocument{}, visible, err
	}
	return document, true, nil
}

func knowledgeSearchSourceReadable(ctx context.Context, tx *sql.Tx, actor Principal, runtime AgentRuntimeAuthentication, document KnowledgeSourceDocument, now time.Time) (bool, error) {
	switch document.Source.Kind {
	case KnowledgeSourceMessage:
		return messageSourceReadable(ctx, tx, actor, document.Source.ID, now)
	case KnowledgeSourceWork:
		work, err := scanWork(tx.QueryRowContext(ctx, workSelect()+` WHERE id = ?`, document.Source.ID))
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		if err != nil || work.OrganizationID != actor.OrganizationID {
			return false, err
		}
		reason, err := requireGrant(ctx, tx, actor, CapabilityWorkRead, Scope{Kind: "work", ID: work.ID}, now, "")
		return err == nil && reason == "", err
	case KnowledgeSourceArtifactVersion:
		artifact, err := artifactByID(ctx, tx, document.Source.ID, actor.OrganizationID)
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		allowed, err := artifactReadAllowed(ctx, tx, actor, artifact, now)
		if err != nil || !allowed {
			return allowed, err
		}
		version, err := artifactVersionByID(ctx, tx, artifact.ID, document.Source.Version)
		if errors.Is(err, ErrArtifactVersionNotFound) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		_, err = projectArtifactView(ctx, tx, actor, runtime, artifact, version, now)
		return err == nil, err
	default:
		return false, nil
	}
}

func knowledgeSearchSnippet(body string) (string, bool) {
	if !utf8.ValidString(body) {
		return "", false
	}
	if len(body) <= knowledgeSearchSnippetMaxBytes {
		return body, true
	}
	limit := knowledgeSearchSnippetMaxBytes
	for limit > 0 && !utf8.RuneStart(body[limit]) {
		limit--
	}
	return body[:limit], true
}
