package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	sqlite3 "modernc.org/sqlite/lib"
)

const (
	KnowledgeSourceMessage         = "message"
	KnowledgeSourceWork            = "work"
	KnowledgeSourceArtifactVersion = "artifact_version"

	KnowledgeIndexReady      = "ready"
	KnowledgeIndexRebuilding = "rebuilding"
	KnowledgeIndexDegraded   = "degraded"

	knowledgeFTSSchema = `CREATE VIRTUAL TABLE knowledge_fts USING fts5(
		source_kind UNINDEXED,
		source_id UNINDEXED,
		source_version UNINDEXED,
		generation UNINDEXED,
		revision UNINDEXED,
		body
	)`

	knowledgeSearchQueryMaxBytes     = 256
	knowledgeSearchTermsMax          = 8
	knowledgeSearchTermMaxBytes      = 64
	knowledgeSearchDefaultPageSize   = 20
	knowledgeSearchMaxPageSize       = 50
	knowledgeSearchCandidateLimit    = 400
	knowledgeSearchSnippetMaxBytes   = 512
	knowledgeSearchAggregateMaxBytes = 16 << 10
)

type KnowledgeSource struct {
	Kind    string
	ID      string
	Version uint64
}

type KnowledgeDirtySource struct {
	Sequence uint64
	Source   KnowledgeSource
	Revision [sha256.Size]byte
	Enqueued time.Time
}

type KnowledgeIndexMetadata struct {
	ActiveGeneration uint64
	NextGeneration   uint64
	Status           string
}

type KnowledgeIndexHealth uint8

const (
	KnowledgeIndexHealthy KnowledgeIndexHealth = iota
	KnowledgeIndexLagging
	KnowledgeIndexCorrupt
)

var errKnowledgeProjectionInvariant = errors.New("knowledge projection invariant violated")

type KnowledgeGenerationProgress struct {
	Generation        uint64
	SnapshotHighWater uint64
	AppliedSequence   uint64
}

type KnowledgeSourceDocument struct {
	Source   KnowledgeSource
	Revision [sha256.Size]byte
	Body     string
}

type KnowledgeSearchParams struct {
	Human  Principal
	Agent  AgentRuntimeAuthentication
	Query  string
	Cursor string
	Limit  uint32
	Now    time.Time
}

type KnowledgeSearchResult struct {
	Source  KnowledgeSource
	Snippet string
}

type KnowledgeSearchPage struct {
	Results    []KnowledgeSearchResult
	NextCursor string
	Status     string
}

var (
	ErrKnowledgeSearchInvalid           = errors.New("knowledge search input is invalid")
	ErrKnowledgeSearchUnauthenticated   = errors.New("knowledge search authentication is invalid")
	ErrKnowledgeSearchCursorUnavailable = errors.New("knowledge search cursor is unavailable")
	errKnowledgeSearchCorrupt           = errors.New("knowledge search index is corrupt")
)

type KnowledgeWorkFields struct {
	Goal               string
	AcceptanceCriteria []string
	Constraints        []string
	BlockingReason     string
	Result             string
}

func KnowledgeWorkRevision(fields KnowledgeWorkFields) [sha256.Size]byte {
	hash := sha256.New()
	writeKnowledgeField(hash, "goal", fields.Goal)
	writeKnowledgeList(hash, "acceptance_criteria", fields.AcceptanceCriteria)
	writeKnowledgeList(hash, "constraints", fields.Constraints)
	writeKnowledgeField(hash, "blocking_reason", fields.BlockingReason)
	writeKnowledgeField(hash, "result", fields.Result)
	var revision [sha256.Size]byte
	copy(revision[:], hash.Sum(nil))
	return revision
}

func knowledgeWorkRevision(work Work) [sha256.Size]byte {
	criteria := make([]string, len(work.AcceptanceCriteria))
	for index, criterion := range work.AcceptanceCriteria {
		criteria[index] = criterion.Body
	}
	constraints := make([]string, len(work.Constraints))
	for index, constraint := range work.Constraints {
		constraints[index] = constraint.Body
	}
	return KnowledgeWorkRevision(KnowledgeWorkFields{Goal: work.Goal, AcceptanceCriteria: criteria, Constraints: constraints, BlockingReason: work.BlockingReason, Result: work.Result})
}

func KnowledgeMessageRevision(messageID string, targetSequence uint64) [sha256.Size]byte {
	hash := sha256.New()
	writeKnowledgeField(hash, "message_id", messageID)
	writeKnowledgeUint64(hash, targetSequence)
	return knowledgeHash(hash)
}

func KnowledgeArtifactVersionRevision(artifactID string, version uint64, digest [sha256.Size]byte) [sha256.Size]byte {
	hash := sha256.New()
	writeKnowledgeField(hash, "artifact_id", artifactID)
	writeKnowledgeUint64(hash, version)
	writeKnowledgeBytes(hash, digest[:])
	return knowledgeHash(hash)
}

func (s *Store) SearchKnowledge(ctx context.Context, params KnowledgeSearchParams) (KnowledgeSearchPage, error) {
	query, err := normalizeKnowledgeSearchQuery(params.Query)
	if err != nil || params.Now.IsZero() || (params.Limit > knowledgeSearchMaxPageSize) {
		return KnowledgeSearchPage{}, ErrKnowledgeSearchInvalid
	}
	limit := params.Limit
	if limit == 0 {
		limit = knowledgeSearchDefaultPageSize
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return KnowledgeSearchPage{}, fmt.Errorf("begin knowledge search: %w", err)
	}
	defer tx.Rollback()
	actor, runtime, err := knowledgeSearchAuthentication(ctx, tx, params, params.Now)
	if err != nil {
		return KnowledgeSearchPage{}, err
	}
	metadata, err := readKnowledgeIndexMetadata(ctx, tx)
	if err != nil {
		return KnowledgeSearchPage{}, fmt.Errorf("read knowledge search metadata: %w", err)
	}
	status, generation, searchable, err := searchableKnowledgeGeneration(ctx, tx, metadata)
	if err != nil {
		return KnowledgeSearchPage{}, fmt.Errorf("read knowledge search generation: %w", err)
	}
	page := KnowledgeSearchPage{Status: status}
	if !searchable {
		return page, nil
	}
	available, err := knowledgeSearchFTSAvailable(ctx, tx)
	if err != nil {
		if errors.Is(classifyKnowledgeSearchCandidateError(err), errKnowledgeSearchCorrupt) {
			return KnowledgeSearchPage{Status: KnowledgeIndexDegraded}, nil
		}
		return KnowledgeSearchPage{}, fmt.Errorf("read knowledge search fts: %w", err)
	}
	if !available {
		return KnowledgeSearchPage{Status: KnowledgeIndexDegraded}, nil
	}
	binding := KnowledgeCursorBinding{
		PrincipalFingerprint: knowledgeSearchPrincipalFingerprint(actor),
		QueryHash:            sha256.Sum256([]byte(query.normalized)),
		Generation:           generation,
	}
	var after *KnowledgeCursorSeekKey
	if params.Cursor != "" {
		seek, err := s.OpenKnowledgeCursor(params.Cursor, binding)
		if err != nil {
			return KnowledgeSearchPage{}, ErrKnowledgeSearchCursorUnavailable
		}
		after = &seek
	}
	candidates, err := listKnowledgeSearchCandidates(ctx, tx, generation, query.match, after)
	if err != nil {
		if errors.Is(err, errKnowledgeSearchCorrupt) {
			return KnowledgeSearchPage{Status: KnowledgeIndexDegraded}, nil
		}
		return KnowledgeSearchPage{}, err
	}
	var aggregate int
	hasNext := false
	var last KnowledgeCursorSeekKey
	for _, candidate := range candidates {
		document, visible, err := currentKnowledgeSearchDocument(ctx, tx, actor, runtime, generation, candidate, params.Now)
		if err != nil {
			if errors.Is(err, errKnowledgeSearchCorrupt) {
				return KnowledgeSearchPage{Status: KnowledgeIndexDegraded}, nil
			}
			return KnowledgeSearchPage{}, err
		}
		if !visible {
			continue
		}
		snippet, ok := knowledgeSearchSnippet(document.Body)
		if !ok {
			continue
		}
		if len(page.Results) >= int(limit) || aggregate+len(snippet) > knowledgeSearchAggregateMaxBytes {
			hasNext = len(page.Results) > 0
			break
		}
		page.Results = append(page.Results, KnowledgeSearchResult{Source: document.Source, Snippet: snippet})
		aggregate += len(snippet)
		last = candidate.seek
	}
	if hasNext && len(page.Results) > 0 {
		cursor, err := s.SealKnowledgeCursor(binding, last)
		if err != nil {
			return KnowledgeSearchPage{}, ErrKnowledgeSearchCursorUnavailable
		}
		page.NextCursor = cursor
	}
	return page, nil
}

type normalizedKnowledgeSearchQuery struct {
	normalized string
	match      string
}

func normalizeKnowledgeSearchQuery(raw string) (normalizedKnowledgeSearchQuery, error) {
	if len(raw) == 0 || len(raw) > knowledgeSearchQueryMaxBytes || !utf8.ValidString(raw) {
		return normalizedKnowledgeSearchQuery{}, ErrKnowledgeSearchInvalid
	}
	terms := strings.Fields(raw)
	if len(terms) == 0 || len(terms) > knowledgeSearchTermsMax {
		return normalizedKnowledgeSearchQuery{}, ErrKnowledgeSearchInvalid
	}
	quoted := make([]string, len(terms))
	for index, term := range terms {
		if len(term) == 0 || len(term) > knowledgeSearchTermMaxBytes {
			return normalizedKnowledgeSearchQuery{}, ErrKnowledgeSearchInvalid
		}
		quoted[index] = `"` + strings.ReplaceAll(term, `"`, `""`) + `"`
	}
	return normalizedKnowledgeSearchQuery{
		normalized: strings.Join(terms, " "),
		match:      strings.Join(quoted, " AND "),
	}, nil
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

func searchableKnowledgeGeneration(ctx context.Context, tx *sql.Tx, metadata KnowledgeIndexMetadata) (string, uint64, bool, error) {
	if metadata.Status != KnowledgeIndexReady && metadata.Status != KnowledgeIndexRebuilding {
		return KnowledgeIndexDegraded, 0, false, nil
	}
	if metadata.ActiveGeneration == 0 {
		if metadata.Status == KnowledgeIndexRebuilding {
			return KnowledgeIndexRebuilding, 0, false, nil
		}
		return KnowledgeIndexDegraded, 0, false, nil
	}
	var state string
	if err := tx.QueryRowContext(ctx, `SELECT state FROM knowledge_index_generations WHERE generation = ?`, metadata.ActiveGeneration).Scan(&state); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return KnowledgeIndexDegraded, 0, false, nil
		}
		return "", 0, false, err
	}
	if state != "complete" {
		return KnowledgeIndexDegraded, 0, false, nil
	}
	return metadata.Status, metadata.ActiveGeneration, true, nil
}

func knowledgeSearchFTSAvailable(ctx context.Context, tx *sql.Tx) (bool, error) {
	var schema string
	err := tx.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'knowledge_fts'`).Scan(&schema)
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
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM knowledge_fts WHERE knowledge_fts MATCH ?`, "knowledgecapabilityprobe").Scan(&count); err != nil {
		return false, err
	}
	return true, nil
}

func knowledgeSearchPrincipalFingerprint(principal Principal) [sha256.Size]byte {
	hash := sha256.New()
	writeKnowledgeField(hash, "kind", string(principal.Kind))
	writeKnowledgeField(hash, "id", principal.ID)
	writeKnowledgeField(hash, "organization", principal.OrganizationID)
	return knowledgeHash(hash)
}

type knowledgeSearchCandidate struct {
	seek     KnowledgeCursorSeekKey
	revision [sha256.Size]byte
}

type knowledgeSearchCandidateFaultContextKey struct{}

type knowledgeSearchCandidateFaultFunc func(string, *knowledgeSearchCandidate, error) error

type knowledgeSearchCandidateOverrideContextKey struct{}

type knowledgeSearchCandidateOverrideFunc func(uint64) []knowledgeSearchCandidate

func knowledgeSearchCandidateFault(ctx context.Context, stage string, candidate *knowledgeSearchCandidate, err error) error {
	if fault, ok := ctx.Value(knowledgeSearchCandidateFaultContextKey{}).(knowledgeSearchCandidateFaultFunc); ok {
		return fault(stage, candidate, err)
	}
	return err
}

func classifyKnowledgeSearchCandidateError(err error) error {
	if errors.Is(err, errKnowledgeSearchCorrupt) {
		return errKnowledgeSearchCorrupt
	}
	var sqliteErr interface {
		error
		Code() int
	}
	if errors.As(err, &sqliteErr) {
		if isKnowledgeSearchSQLiteCorruptionCode(sqliteErr.Code()) {
			return errKnowledgeSearchCorrupt
		}
	}
	return err
}

func isKnowledgeSearchSQLiteCorruptionCode(code int) bool {
	switch code & 0xff {
	case sqlite3.SQLITE_CORRUPT, sqlite3.SQLITE_NOTADB:
		return true
	default:
		return false
	}
}

func listKnowledgeSearchCandidates(ctx context.Context, tx *sql.Tx, generation uint64, match string, after *KnowledgeCursorSeekKey) ([]knowledgeSearchCandidate, error) {
	if override, ok := ctx.Value(knowledgeSearchCandidateOverrideContextKey{}).(knowledgeSearchCandidateOverrideFunc); ok {
		candidates := override(generation)
		filtered := make([]knowledgeSearchCandidate, 0, len(candidates))
		for _, candidate := range candidates {
			if after != nil && !knowledgeSearchCandidateAfter(candidate.seek, *after) {
				continue
			}
			filtered = append(filtered, candidate)
			if len(filtered) == knowledgeSearchCandidateLimit {
				break
			}
		}
		return filtered, nil
	}
	query := `
		WITH candidates AS (
			SELECT rowid, source_kind, source_id, source_version, revision, bm25(knowledge_fts) AS rank
			FROM knowledge_fts
			WHERE generation = ? AND knowledge_fts MATCH ?
		)
		SELECT rowid, source_kind, source_id, source_version, revision, rank FROM candidates`
	args := []any{generation, match}
	if after != nil {
		query += ` WHERE rank > ? OR (rank = ? AND (
			source_kind > ? OR (source_kind = ? AND (
				source_id > ? OR (source_id = ? AND (
					source_version > ? OR (source_version = ? AND rowid > ?)
				))
			))
		))`
		args = append(args, after.Rank, after.Rank, after.SourceKind, after.SourceKind, after.SourceID, after.SourceID, after.SourceVersion, after.SourceVersion, after.RowID)
	}
	query += ` ORDER BY rank, source_kind, source_id, source_version, rowid LIMIT ?`
	args = append(args, knowledgeSearchCandidateLimit)
	rows, err := tx.QueryContext(ctx, query, args...)
	if err == nil {
		defer rows.Close()
	}
	err = knowledgeSearchCandidateFault(ctx, "query", nil, err)
	if err != nil {
		available, availabilityErr := knowledgeSearchFTSAvailable(ctx, tx)
		if availabilityErr == nil && !available {
			return nil, errKnowledgeSearchCorrupt
		}
		if availabilityErr != nil {
			return nil, fmt.Errorf("read knowledge search fts capability: %w", classifyKnowledgeSearchCandidateError(availabilityErr))
		}
		return nil, fmt.Errorf("list knowledge search candidates: %w", classifyKnowledgeSearchCandidateError(err))
	}
	candidates := make([]knowledgeSearchCandidate, 0, knowledgeSearchCandidateLimit)
	for rows.Next() {
		var candidate knowledgeSearchCandidate
		var revision []byte
		err := rows.Scan(&candidate.seek.RowID, &candidate.seek.SourceKind, &candidate.seek.SourceID, &candidate.seek.SourceVersion, &revision, &candidate.seek.Rank)
		err = knowledgeSearchCandidateFault(ctx, "scan", &candidate, err)
		if err != nil {
			return nil, fmt.Errorf("scan knowledge search candidate: %w", classifyKnowledgeSearchCandidateError(err))
		}
		if len(revision) != sha256.Size || math.IsNaN(candidate.seek.Rank) || math.IsInf(candidate.seek.Rank, 0) || !validKnowledgeCursorSeekKey(candidate.seek) {
			return nil, errKnowledgeSearchCorrupt
		}
		copy(candidate.revision[:], revision)
		candidates = append(candidates, candidate)
	}
	err = knowledgeSearchCandidateFault(ctx, "iterate", nil, rows.Err())
	if err != nil {
		return nil, fmt.Errorf("iterate knowledge search candidates: %w", classifyKnowledgeSearchCandidateError(err))
	}
	return candidates, nil
}

func knowledgeSearchCandidateAfter(candidate, after KnowledgeCursorSeekKey) bool {
	if candidate.Rank != after.Rank {
		return candidate.Rank > after.Rank
	}
	if candidate.SourceKind != after.SourceKind {
		return candidate.SourceKind > after.SourceKind
	}
	if candidate.SourceID != after.SourceID {
		return candidate.SourceID > after.SourceID
	}
	if candidate.SourceVersion != after.SourceVersion {
		return candidate.SourceVersion > after.SourceVersion
	}
	return candidate.RowID > after.RowID
}

func currentKnowledgeSearchDocument(ctx context.Context, tx *sql.Tx, actor Principal, runtime AgentRuntimeAuthentication, generation uint64, candidate knowledgeSearchCandidate, now time.Time) (KnowledgeSourceDocument, bool, error) {
	var projectionRevision []byte
	var rowID int64
	err := tx.QueryRowContext(ctx, `
		SELECT revision, fts_rowid FROM knowledge_projection_rows
		WHERE generation = ? AND source_kind = ? AND source_id = ? AND source_version = ?
	`, generation, candidate.seek.SourceKind, candidate.seek.SourceID, candidate.seek.SourceVersion).Scan(&projectionRevision, &rowID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return KnowledgeSourceDocument{}, false, nil
		}
		return KnowledgeSourceDocument{}, false, fmt.Errorf("read knowledge search projection: %w", err)
	}
	if rowID != candidate.seek.RowID || len(projectionRevision) != sha256.Size || string(projectionRevision) != string(candidate.revision[:]) {
		return KnowledgeSourceDocument{}, false, errKnowledgeSearchCorrupt
	}
	source := KnowledgeSource{Kind: candidate.seek.SourceKind, ID: candidate.seek.SourceID, Version: candidate.seek.SourceVersion}
	document, found, err := readKnowledgeSourceDocument(ctx, tx, source)
	if err != nil {
		return KnowledgeSourceDocument{}, false, err
	}
	if !found || document.Revision != candidate.revision {
		return KnowledgeSourceDocument{}, false, nil
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
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return false, nil
			}
			return false, err
		}
		if work.OrganizationID != actor.OrganizationID {
			return false, nil
		}
		if err := validatePrincipalInOrganization(ctx, tx, actor, actor.OrganizationID); err != nil {
			if errors.Is(err, ErrPrincipalNotFound) || errors.Is(err, ErrPermissionDenied) {
				return false, nil
			}
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

func (s *Store) EnqueueKnowledgeDirtySource(ctx context.Context, source KnowledgeSource, revision [sha256.Size]byte, now time.Time) (KnowledgeDirtySource, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return KnowledgeDirtySource{}, fmt.Errorf("begin knowledge dirty source: %w", err)
	}
	defer tx.Rollback()
	entry, err := enqueueKnowledgeDirtySource(ctx, tx, source, revision, now)
	if err != nil {
		return KnowledgeDirtySource{}, err
	}
	if err := tx.Commit(); err != nil {
		return KnowledgeDirtySource{}, fmt.Errorf("commit knowledge dirty source: %w", err)
	}
	return entry, nil
}

func (s *Store) KnowledgeIndexMetadata(ctx context.Context) (KnowledgeIndexMetadata, error) {
	return readKnowledgeIndexMetadata(ctx, s.db)
}

func (s *Store) StartKnowledgeRebuild(ctx context.Context, now time.Time) (KnowledgeIndexMetadata, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return KnowledgeIndexMetadata{}, fmt.Errorf("begin knowledge rebuild: %w", err)
	}
	defer tx.Rollback()
	metadata, err := readKnowledgeIndexMetadata(ctx, tx)
	if err != nil {
		return KnowledgeIndexMetadata{}, err
	}
	if metadata.NextGeneration != 0 {
		return KnowledgeIndexMetadata{}, fmt.Errorf("knowledge rebuild already targets generation %d", metadata.NextGeneration)
	}
	var maximum uint64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(generation), 0) FROM knowledge_index_generations`).Scan(&maximum); err != nil {
		return KnowledgeIndexMetadata{}, fmt.Errorf("read latest knowledge generation: %w", err)
	}
	next := maximum + 1
	if _, err := tx.ExecContext(ctx, `INSERT INTO knowledge_index_generations(generation, state, created_at) VALUES(?, 'building', ?)`, next, unixNano(now)); err != nil {
		return KnowledgeIndexMetadata{}, fmt.Errorf("create knowledge generation: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE knowledge_index_metadata SET next_generation = ?, status = 'rebuilding' WHERE singleton = 1`, next); err != nil {
		return KnowledgeIndexMetadata{}, fmt.Errorf("start knowledge rebuild: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO knowledge_generation_progress(generation, snapshot_high_water, applied_sequence) VALUES(?, 0, 0)`, next); err != nil {
		return KnowledgeIndexMetadata{}, fmt.Errorf("create knowledge generation progress: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return KnowledgeIndexMetadata{}, fmt.Errorf("commit knowledge rebuild: %w", err)
	}
	return KnowledgeIndexMetadata{ActiveGeneration: metadata.ActiveGeneration, NextGeneration: next, Status: KnowledgeIndexRebuilding}, nil
}

func (s *Store) BuildKnowledgeGenerationSnapshot(ctx context.Context, generation uint64) error {
	if generation == 0 {
		return errors.New("knowledge generation must be positive")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin knowledge snapshot: %w", err)
	}
	defer tx.Rollback()
	if err := requireBuildingKnowledgeGeneration(ctx, tx, generation); err != nil {
		return err
	}
	progress, err := readKnowledgeGenerationProgress(ctx, tx, generation)
	if err != nil {
		return err
	}
	if progress.SnapshotHighWater != 0 || progress.AppliedSequence != 0 {
		return fmt.Errorf("knowledge generation %d snapshot is already built", generation)
	}
	var highWater uint64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence), 0) FROM knowledge_dirty_sources`).Scan(&highWater); err != nil {
		return fmt.Errorf("read knowledge snapshot high water: %w", err)
	}
	documents, err := listKnowledgeSourceDocuments(ctx, tx)
	if err != nil {
		return err
	}
	for _, document := range documents {
		if err := replaceKnowledgeProjection(ctx, tx, generation, document); err != nil {
			return err
		}
	}
	if err := verifyKnowledgeGenerationProjection(ctx, tx, generation, documents); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE knowledge_generation_progress SET snapshot_high_water = ?, applied_sequence = ? WHERE generation = ? AND snapshot_high_water = 0 AND applied_sequence = 0`, highWater, highWater, generation)
	if err != nil {
		return fmt.Errorf("record knowledge snapshot progress: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read knowledge snapshot progress: %w", err)
	}
	if affected != 1 {
		return fmt.Errorf("knowledge generation %d snapshot progress changed", generation)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit knowledge snapshot: %w", err)
	}
	return nil
}

func (s *Store) KnowledgeGenerationProgress(ctx context.Context, generation uint64) (KnowledgeGenerationProgress, error) {
	var progress KnowledgeGenerationProgress
	if err := s.db.QueryRowContext(ctx, `SELECT generation, snapshot_high_water, applied_sequence FROM knowledge_generation_progress WHERE generation = ?`, generation).Scan(&progress.Generation, &progress.SnapshotHighWater, &progress.AppliedSequence); err != nil {
		return KnowledgeGenerationProgress{}, fmt.Errorf("read knowledge generation progress: %w", err)
	}
	return progress, nil
}

func (s *Store) NextKnowledgeDirtySource(ctx context.Context, generation uint64) (KnowledgeDirtySource, bool, error) {
	progress, err := s.KnowledgeGenerationProgress(ctx, generation)
	if err != nil {
		return KnowledgeDirtySource{}, false, err
	}
	return readKnowledgeDirtySource(ctx, s.db, progress.AppliedSequence+1)
}

func (s *Store) ReadKnowledgeSourceDocument(ctx context.Context, source KnowledgeSource) (KnowledgeSourceDocument, bool, error) {
	if err := validateKnowledgeSource(source); err != nil {
		return KnowledgeSourceDocument{}, false, err
	}
	return readKnowledgeSourceDocument(ctx, s.db, source)
}

func (s *Store) ApplyKnowledgeProjection(ctx context.Context, generation, sequence uint64, document KnowledgeSourceDocument) (bool, error) {
	if generation == 0 || sequence == 0 {
		return false, errors.New("knowledge generation and sequence must be positive")
	}
	if err := validateKnowledgeSource(document.Source); err != nil {
		return false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin knowledge projection: %w", err)
	}
	defer tx.Rollback()
	progress, err := readKnowledgeGenerationProgress(ctx, tx, generation)
	if err != nil {
		return false, err
	}
	if sequence != progress.AppliedSequence+1 {
		return false, fmt.Errorf("knowledge sequence %d is not next after %d", sequence, progress.AppliedSequence)
	}
	dirty, found, err := readKnowledgeDirtySource(ctx, tx, sequence)
	if err != nil {
		return false, err
	}
	if !found {
		return false, fmt.Errorf("knowledge dirty source %d is missing", sequence)
	}
	current, currentFound, err := readKnowledgeSourceDocument(ctx, tx, dirty.Source)
	if err != nil {
		return false, err
	}
	if !currentFound || current.Revision != dirty.Revision {
		if err := advanceKnowledgeGeneration(ctx, tx, generation, sequence); err != nil {
			return false, err
		}
		if err := tx.Commit(); err != nil {
			return false, fmt.Errorf("commit stale knowledge projection: %w", err)
		}
		return false, nil
	}
	if document.Source != dirty.Source || document.Revision != dirty.Revision || document.Body != current.Body {
		return false, errors.New("knowledge source document is not current")
	}
	if err := replaceKnowledgeProjection(ctx, tx, generation, document); err != nil {
		return false, err
	}
	if err := advanceKnowledgeGeneration(ctx, tx, generation, sequence); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit knowledge projection: %w", err)
	}
	return true, nil
}

func (s *Store) CompleteKnowledgeGeneration(ctx context.Context, generation uint64) error {
	if generation == 0 {
		return errors.New("knowledge generation must be positive")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin knowledge generation completion: %w", err)
	}
	defer tx.Rollback()
	if err := requireBuildingKnowledgeGeneration(ctx, tx, generation); err != nil {
		return err
	}
	var next uint64
	if err := tx.QueryRowContext(ctx, `SELECT next_generation FROM knowledge_index_metadata WHERE singleton = 1`).Scan(&next); err != nil {
		return fmt.Errorf("read next knowledge generation: %w", err)
	}
	if next != generation {
		return fmt.Errorf("knowledge generation %d is not the pending generation", generation)
	}
	progress, err := readKnowledgeGenerationProgress(ctx, tx, generation)
	if err != nil {
		return err
	}
	if progress.AppliedSequence < progress.SnapshotHighWater {
		return fmt.Errorf("knowledge generation %d has not reached its snapshot high water", generation)
	}
	documents, err := listKnowledgeSourceDocuments(ctx, tx)
	if err != nil {
		return err
	}
	if err := verifyKnowledgeGenerationProjection(ctx, tx, generation, documents); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE knowledge_index_generations SET state = 'complete' WHERE generation = ? AND state = 'building'`, generation); err != nil {
		return fmt.Errorf("complete knowledge generation: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit knowledge generation completion: %w", err)
	}
	return nil
}

func (s *Store) DiscardKnowledgeGeneration(ctx context.Context, generation uint64) (KnowledgeIndexMetadata, error) {
	if generation == 0 {
		return KnowledgeIndexMetadata{}, errors.New("knowledge generation must be positive")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return KnowledgeIndexMetadata{}, fmt.Errorf("begin knowledge generation discard: %w", err)
	}
	defer tx.Rollback()
	metadata, err := readKnowledgeIndexMetadata(ctx, tx)
	if err != nil {
		return KnowledgeIndexMetadata{}, err
	}
	if metadata.NextGeneration != generation {
		return KnowledgeIndexMetadata{}, fmt.Errorf("knowledge generation %d is not pending", generation)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE knowledge_index_generations SET state = 'corrupt' WHERE generation = ?`, generation); err != nil {
		return KnowledgeIndexMetadata{}, fmt.Errorf("discard knowledge generation: %w", err)
	}
	status := KnowledgeIndexDegraded
	if metadata.ActiveGeneration != 0 {
		status = KnowledgeIndexReady
	}
	if _, err := tx.ExecContext(ctx, `UPDATE knowledge_index_metadata SET next_generation = 0, status = ? WHERE singleton = 1`, status); err != nil {
		return KnowledgeIndexMetadata{}, fmt.Errorf("clear pending knowledge generation: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return KnowledgeIndexMetadata{}, fmt.Errorf("commit knowledge generation discard: %w", err)
	}
	return KnowledgeIndexMetadata{ActiveGeneration: metadata.ActiveGeneration, Status: status}, nil
}

func (s *Store) MarkKnowledgeActiveGenerationCorrupt(ctx context.Context, generation uint64) (KnowledgeIndexMetadata, error) {
	if generation == 0 {
		return KnowledgeIndexMetadata{}, errors.New("knowledge generation must be positive")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return KnowledgeIndexMetadata{}, fmt.Errorf("begin active knowledge corruption: %w", err)
	}
	defer tx.Rollback()
	metadata, err := readKnowledgeIndexMetadata(ctx, tx)
	if err != nil {
		return KnowledgeIndexMetadata{}, err
	}
	if metadata.ActiveGeneration != generation {
		return KnowledgeIndexMetadata{}, fmt.Errorf("knowledge generation %d is not active", generation)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE knowledge_index_generations SET state = 'corrupt' WHERE generation = ?`, generation); err != nil {
		return KnowledgeIndexMetadata{}, fmt.Errorf("mark active knowledge generation corrupt: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE knowledge_index_metadata SET active_generation = 0, status = 'degraded' WHERE singleton = 1`); err != nil {
		return KnowledgeIndexMetadata{}, fmt.Errorf("degrade knowledge index: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return KnowledgeIndexMetadata{}, fmt.Errorf("commit active knowledge corruption: %w", err)
	}
	return KnowledgeIndexMetadata{NextGeneration: metadata.NextGeneration, Status: KnowledgeIndexDegraded}, nil
}

func (s *Store) RepairKnowledgeFTS(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin knowledge fts repair: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DROP TABLE IF EXISTS knowledge_fts`); err != nil {
		return fmt.Errorf("drop knowledge fts: %w", err)
	}
	if _, err := tx.ExecContext(ctx, knowledgeFTSSchema); err != nil {
		return fmt.Errorf("create knowledge fts: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM knowledge_projection_rows`); err != nil {
		return fmt.Errorf("clear knowledge projection bookkeeping: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE knowledge_index_generations SET state = 'corrupt' WHERE state != 'corrupt'`); err != nil {
		return fmt.Errorf("mark knowledge generations corrupt: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE knowledge_index_metadata SET active_generation = 0, next_generation = 0, status = 'degraded' WHERE singleton = 1`); err != nil {
		return fmt.Errorf("degrade knowledge index after fts repair: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit knowledge fts repair: %w", err)
	}
	return nil
}

func (s *Store) ActivateKnowledgeGeneration(ctx context.Context, generation uint64) (KnowledgeIndexMetadata, error) {
	if generation == 0 {
		return KnowledgeIndexMetadata{}, errors.New("knowledge generation must be positive")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return KnowledgeIndexMetadata{}, fmt.Errorf("begin knowledge generation activation: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE knowledge_index_metadata SET status = status WHERE singleton = 1`); err != nil {
		return KnowledgeIndexMetadata{}, fmt.Errorf("lock knowledge activation: %w", err)
	}
	var next uint64
	if err := tx.QueryRowContext(ctx, `SELECT next_generation FROM knowledge_index_metadata WHERE singleton = 1`).Scan(&next); err != nil {
		return KnowledgeIndexMetadata{}, fmt.Errorf("read pending knowledge generation: %w", err)
	}
	if next != generation {
		return KnowledgeIndexMetadata{}, fmt.Errorf("knowledge generation %d is not the pending generation", generation)
	}
	var state string
	if err := tx.QueryRowContext(ctx, `SELECT state FROM knowledge_index_generations WHERE generation = ?`, generation).Scan(&state); err != nil {
		return KnowledgeIndexMetadata{}, fmt.Errorf("read knowledge generation: %w", err)
	}
	if state != "complete" {
		return KnowledgeIndexMetadata{}, fmt.Errorf("knowledge generation %d is not complete", generation)
	}
	var maximum, applied uint64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence), 0) FROM knowledge_dirty_sources`).Scan(&maximum); err != nil {
		return KnowledgeIndexMetadata{}, fmt.Errorf("read knowledge activation high water: %w", err)
	}
	if err := tx.QueryRowContext(ctx, `SELECT applied_sequence FROM knowledge_generation_progress WHERE generation = ?`, generation).Scan(&applied); err != nil {
		return KnowledgeIndexMetadata{}, fmt.Errorf("read knowledge activation progress: %w", err)
	}
	if applied != maximum {
		return KnowledgeIndexMetadata{}, fmt.Errorf("knowledge generation %d is applied through %d, not %d", generation, applied, maximum)
	}
	documents, err := listKnowledgeSourceDocuments(ctx, tx)
	if err != nil {
		return KnowledgeIndexMetadata{}, err
	}
	if err := verifyKnowledgeGenerationProjection(ctx, tx, generation, documents); err != nil {
		return KnowledgeIndexMetadata{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE knowledge_index_metadata SET active_generation = ?, next_generation = 0, status = 'ready' WHERE singleton = 1`, generation); err != nil {
		return KnowledgeIndexMetadata{}, fmt.Errorf("activate knowledge generation: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return KnowledgeIndexMetadata{}, fmt.Errorf("commit knowledge generation activation: %w", err)
	}
	return KnowledgeIndexMetadata{ActiveGeneration: generation, Status: KnowledgeIndexReady}, nil
}

func (s *Store) KnowledgeFTSAvailable(ctx context.Context) (bool, error) {
	var schema string
	err := s.db.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'knowledge_fts'`).Scan(&schema)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read knowledge fts metadata: %w", err)
	}
	if normalizeKnowledgeSchema(schema) != normalizeKnowledgeSchema(knowledgeFTSSchema) {
		return false, nil
	}
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM knowledge_fts WHERE knowledge_fts MATCH ?`, "knowledgecapabilityprobe").Scan(&count); err != nil {
		return false, fmt.Errorf("read knowledge fts capability: %w", err)
	}
	return true, nil
}

func (s *Store) CheckKnowledgeIndexHealth(ctx context.Context) (KnowledgeIndexHealth, error) {
	available, err := s.KnowledgeFTSAvailable(ctx)
	if err != nil {
		return KnowledgeIndexHealthy, err
	}
	if !available {
		return KnowledgeIndexCorrupt, nil
	}
	metadata, err := s.KnowledgeIndexMetadata(ctx)
	if err != nil {
		return KnowledgeIndexHealthy, err
	}
	if metadata.ActiveGeneration == 0 {
		return KnowledgeIndexHealthy, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return KnowledgeIndexHealthy, err
	}
	defer tx.Rollback()
	var maximum, applied uint64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence), 0) FROM knowledge_dirty_sources`).Scan(&maximum); err != nil {
		return KnowledgeIndexHealthy, fmt.Errorf("read knowledge health high water: %w", err)
	}
	if err := tx.QueryRowContext(ctx, `SELECT applied_sequence FROM knowledge_generation_progress WHERE generation = ?`, metadata.ActiveGeneration).Scan(&applied); err != nil {
		return KnowledgeIndexHealthy, fmt.Errorf("read knowledge health progress: %w", err)
	}
	if applied < maximum {
		return KnowledgeIndexLagging, nil
	}
	if applied != maximum {
		return KnowledgeIndexCorrupt, nil
	}
	documents, err := listKnowledgeSourceDocuments(ctx, tx)
	if err != nil {
		return KnowledgeIndexHealthy, err
	}
	if err := verifyKnowledgeGenerationProjection(ctx, tx, metadata.ActiveGeneration, documents); err != nil {
		if errors.Is(err, errKnowledgeProjectionInvariant) {
			return KnowledgeIndexCorrupt, nil
		}
		return KnowledgeIndexHealthy, err
	}
	return KnowledgeIndexHealthy, nil
}

func enqueueKnowledgeDirtySource(ctx context.Context, tx *sql.Tx, source KnowledgeSource, revision [sha256.Size]byte, now time.Time) (KnowledgeDirtySource, error) {
	if err := validateKnowledgeSource(source); err != nil {
		return KnowledgeDirtySource{}, err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO knowledge_dirty_sources(source_kind, source_id, source_version, revision, enqueued_at) VALUES(?, ?, ?, ?, ?)`, source.Kind, source.ID, source.Version, revision[:], unixNano(now))
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "no such table: knowledge_dirty_sources") {
			return KnowledgeDirtySource{}, nil
		}
		return KnowledgeDirtySource{}, fmt.Errorf("enqueue knowledge dirty source: %w", err)
	}
	sequence, err := result.LastInsertId()
	if err != nil {
		return KnowledgeDirtySource{}, fmt.Errorf("read knowledge dirty sequence: %w", err)
	}
	return KnowledgeDirtySource{Sequence: uint64(sequence), Source: source, Revision: revision, Enqueued: now}, nil
}

type knowledgeQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type knowledgeSourceQueryer interface {
	knowledgeQueryer
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

type knowledgeRowsErrorContextKey struct{}

type knowledgeRowsErrorFunc func(string, *sql.Rows) error

func rowsErr(ctx context.Context, sourceKind string, rows *sql.Rows) error {
	if read, ok := ctx.Value(knowledgeRowsErrorContextKey{}).(knowledgeRowsErrorFunc); ok {
		return read(sourceKind, rows)
	}
	return rows.Err()
}

type knowledgeProjectionCheckErrorContextKey struct{}

type knowledgeProjectionCheckErrorFunc func(string) error

// WithKnowledgeProjectionCheckError is for integration tests only to inject verify-stage errors; production callers must not use it.
func WithKnowledgeProjectionCheckError(ctx context.Context, check func(string) error) context.Context {
	return context.WithValue(ctx, knowledgeProjectionCheckErrorContextKey{}, knowledgeProjectionCheckErrorFunc(check))
}

func projectionCheckErr(ctx context.Context, stage string) error {
	if check, ok := ctx.Value(knowledgeProjectionCheckErrorContextKey{}).(knowledgeProjectionCheckErrorFunc); ok {
		return check(stage)
	}
	return nil
}

func readKnowledgeIndexMetadata(ctx context.Context, queryer knowledgeQueryer) (KnowledgeIndexMetadata, error) {
	var metadata KnowledgeIndexMetadata
	if err := queryer.QueryRowContext(ctx, `SELECT active_generation, next_generation, status FROM knowledge_index_metadata WHERE singleton = 1`).Scan(&metadata.ActiveGeneration, &metadata.NextGeneration, &metadata.Status); err != nil {
		return KnowledgeIndexMetadata{}, fmt.Errorf("read knowledge index metadata: %w", err)
	}
	return metadata, nil
}

func requireBuildingKnowledgeGeneration(ctx context.Context, tx *sql.Tx, generation uint64) error {
	var state string
	if err := tx.QueryRowContext(ctx, `SELECT state FROM knowledge_index_generations WHERE generation = ?`, generation).Scan(&state); err != nil {
		return fmt.Errorf("read knowledge generation: %w", err)
	}
	if state != "building" {
		return fmt.Errorf("knowledge generation %d is not building", generation)
	}
	var next uint64
	if err := tx.QueryRowContext(ctx, `SELECT next_generation FROM knowledge_index_metadata WHERE singleton = 1`).Scan(&next); err != nil {
		return fmt.Errorf("read next knowledge generation: %w", err)
	}
	if next != generation {
		return fmt.Errorf("knowledge generation %d is not the pending generation", generation)
	}
	return nil
}

func readKnowledgeGenerationProgress(ctx context.Context, queryer knowledgeQueryer, generation uint64) (KnowledgeGenerationProgress, error) {
	var progress KnowledgeGenerationProgress
	if err := queryer.QueryRowContext(ctx, `SELECT generation, snapshot_high_water, applied_sequence FROM knowledge_generation_progress WHERE generation = ?`, generation).Scan(&progress.Generation, &progress.SnapshotHighWater, &progress.AppliedSequence); err != nil {
		return KnowledgeGenerationProgress{}, fmt.Errorf("read knowledge generation progress: %w", err)
	}
	return progress, nil
}

func readKnowledgeDirtySource(ctx context.Context, queryer knowledgeQueryer, sequence uint64) (KnowledgeDirtySource, bool, error) {
	var entry KnowledgeDirtySource
	var revision []byte
	var enqueued int64
	err := queryer.QueryRowContext(ctx, `SELECT sequence, source_kind, source_id, source_version, revision, enqueued_at FROM knowledge_dirty_sources WHERE sequence = ?`, sequence).Scan(&entry.Sequence, &entry.Source.Kind, &entry.Source.ID, &entry.Source.Version, &revision, &enqueued)
	if errors.Is(err, sql.ErrNoRows) {
		return KnowledgeDirtySource{}, false, nil
	}
	if err != nil {
		return KnowledgeDirtySource{}, false, fmt.Errorf("read knowledge dirty source: %w", err)
	}
	if len(revision) != sha256.Size {
		return KnowledgeDirtySource{}, false, errors.New("knowledge dirty source revision is invalid")
	}
	copy(entry.Revision[:], revision)
	entry.Enqueued = timeFromUnixNano(enqueued)
	return entry, true, nil
}

func listKnowledgeSourceDocuments(ctx context.Context, queryer knowledgeSourceQueryer) ([]KnowledgeSourceDocument, error) {
	var documents []KnowledgeSourceDocument
	rows, err := queryer.QueryContext(ctx, `SELECT id, target_sequence, body FROM messages ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list knowledge message sources: %w", err)
	}
	for rows.Next() {
		var document KnowledgeSourceDocument
		var sequence uint64
		if err := rows.Scan(&document.Source.ID, &sequence, &document.Body); err != nil {
			rows.Close()
			return nil, err
		}
		document.Source.Kind = KnowledgeSourceMessage
		document.Revision = KnowledgeMessageRevision(document.Source.ID, sequence)
		documents = append(documents, document)
	}
	if err := rowsErr(ctx, KnowledgeSourceMessage, rows); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate knowledge message sources: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	rows, err = queryer.QueryContext(ctx, workSelect()+` ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list knowledge work sources: %w", err)
	}
	for rows.Next() {
		work, err := scanWork(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		if err := loadWorkParts(ctx, queryer, &work); err != nil {
			rows.Close()
			return nil, fmt.Errorf("read knowledge work fields: %w", err)
		}
		documents = append(documents, KnowledgeSourceDocument{Source: KnowledgeSource{Kind: KnowledgeSourceWork, ID: work.ID}, Revision: knowledgeWorkRevision(work), Body: knowledgeWorkBody(work)})
	}
	if err := rowsErr(ctx, KnowledgeSourceWork, rows); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate knowledge work sources: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	rows, err = queryer.QueryContext(ctx, `SELECT v.artifact_id, v.version, a.name, a.media_type, v.summary, v.digest FROM artifact_versions v JOIN artifacts a ON a.id = v.artifact_id AND a.organization_id = v.organization_id ORDER BY v.artifact_id, v.version`)
	if err != nil {
		return nil, fmt.Errorf("list knowledge artifact sources: %w", err)
	}
	for rows.Next() {
		var document KnowledgeSourceDocument
		var name, mediaType, summary string
		var digest []byte
		if err := rows.Scan(&document.Source.ID, &document.Source.Version, &name, &mediaType, &summary, &digest); err != nil {
			rows.Close()
			return nil, err
		}
		if len(digest) != sha256.Size {
			rows.Close()
			return nil, errors.New("knowledge artifact digest is invalid")
		}
		var contentDigest [sha256.Size]byte
		copy(contentDigest[:], digest)
		document.Source.Kind = KnowledgeSourceArtifactVersion
		document.Revision = KnowledgeArtifactVersionRevision(document.Source.ID, document.Source.Version, contentDigest)
		document.Body = strings.Join([]string{name, mediaType, summary}, "\n")
		documents = append(documents, document)
	}
	if err := rowsErr(ctx, KnowledgeSourceArtifactVersion, rows); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate knowledge artifact sources: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	return documents, nil
}

func verifyKnowledgeGenerationProjection(ctx context.Context, tx *sql.Tx, generation uint64, documents []KnowledgeSourceDocument) error {
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'knowledge_fts')`).Scan(&exists); err != nil {
		return fmt.Errorf("read knowledge fts metadata: %w", err)
	}
	if !exists {
		return fmt.Errorf("%w: knowledge fts is unavailable", errKnowledgeProjectionInvariant)
	}
	var probe int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM knowledge_fts WHERE knowledge_fts MATCH ?`, "knowledgecapabilityprobe").Scan(&probe); err != nil {
		return fmt.Errorf("read knowledge fts capability: %w", err)
	}
	for _, document := range documents {
		var rowID int64
		var revision []byte
		if err := projectionCheckErr(ctx, "projection row"); err != nil {
			return fmt.Errorf("read knowledge projection: %w", err)
		}
		err := tx.QueryRowContext(ctx, `SELECT fts_rowid, revision FROM knowledge_projection_rows WHERE generation = ? AND source_kind = ? AND source_id = ? AND source_version = ?`, generation, document.Source.Kind, document.Source.ID, document.Source.Version).Scan(&rowID, &revision)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: knowledge generation %d lacks projection for %s %s", errKnowledgeProjectionInvariant, generation, document.Source.Kind, document.Source.ID)
		}
		if err != nil {
			return fmt.Errorf("read knowledge projection: %w", err)
		}
		if len(revision) != sha256.Size || string(revision) != string(document.Revision[:]) {
			return fmt.Errorf("%w: knowledge generation %d projection revision is stale", errKnowledgeProjectionInvariant, generation)
		}
		var source KnowledgeSource
		var projectionGeneration uint64
		var ftsRevision []byte
		var body string
		if err := projectionCheckErr(ctx, "fts row"); err != nil {
			return fmt.Errorf("read knowledge fts projection: %w", err)
		}
		if err := tx.QueryRowContext(ctx, `SELECT source_kind, source_id, source_version, generation, revision, body FROM knowledge_fts WHERE rowid = ?`, rowID).Scan(&source.Kind, &source.ID, &source.Version, &projectionGeneration, &ftsRevision, &body); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("%w: knowledge generation %d lacks fts projection", errKnowledgeProjectionInvariant, generation)
			}
			return fmt.Errorf("read knowledge fts projection: %w", err)
		}
		if source != document.Source || projectionGeneration != generation || len(ftsRevision) != sha256.Size || string(ftsRevision) != string(document.Revision[:]) || body != document.Body {
			return fmt.Errorf("%w: knowledge generation %d fts projection is incomplete", errKnowledgeProjectionInvariant, generation)
		}
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM knowledge_projection_rows WHERE generation = ?`, generation).Scan(&count); err != nil {
		return fmt.Errorf("count knowledge projections: %w", err)
	}
	if count != len(documents) {
		return fmt.Errorf("%w: knowledge generation %d has unexpected projection rows", errKnowledgeProjectionInvariant, generation)
	}
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM knowledge_fts WHERE generation = ?`, generation).Scan(&count); err != nil {
		return fmt.Errorf("count knowledge fts projections: %w", err)
	}
	if count != len(documents) {
		return fmt.Errorf("%w: knowledge generation %d has unexpected fts rows", errKnowledgeProjectionInvariant, generation)
	}
	return nil
}

func normalizeKnowledgeSchema(schema string) string {
	return strings.ToLower(strings.Join(strings.Fields(schema), " "))
}

func readKnowledgeSourceDocument(ctx context.Context, queryer knowledgeSourceQueryer, source KnowledgeSource) (KnowledgeSourceDocument, bool, error) {
	document := KnowledgeSourceDocument{Source: source}
	switch source.Kind {
	case KnowledgeSourceMessage:
		var sequence uint64
		err := queryer.QueryRowContext(ctx, `SELECT target_sequence, body FROM messages WHERE id = ?`, source.ID).Scan(&sequence, &document.Body)
		if errors.Is(err, sql.ErrNoRows) {
			return KnowledgeSourceDocument{}, false, nil
		}
		if err != nil {
			return KnowledgeSourceDocument{}, false, fmt.Errorf("read knowledge message source: %w", err)
		}
		document.Revision = KnowledgeMessageRevision(source.ID, sequence)
	case KnowledgeSourceWork:
		work, err := scanWork(queryer.QueryRowContext(ctx, workSelect()+` WHERE id = ?`, source.ID))
		if errors.Is(err, sql.ErrNoRows) {
			return KnowledgeSourceDocument{}, false, nil
		}
		if err != nil {
			return KnowledgeSourceDocument{}, false, fmt.Errorf("read knowledge work source: %w", err)
		}
		if err := loadWorkParts(ctx, queryer, &work); err != nil {
			return KnowledgeSourceDocument{}, false, fmt.Errorf("read knowledge work fields: %w", err)
		}
		document.Revision = knowledgeWorkRevision(work)
		document.Body = knowledgeWorkBody(work)
	case KnowledgeSourceArtifactVersion:
		var name, mediaType, summary string
		var digest []byte
		err := queryer.QueryRowContext(ctx, `SELECT a.name, a.media_type, v.summary, v.digest FROM artifact_versions v JOIN artifacts a ON a.id = v.artifact_id AND a.organization_id = v.organization_id WHERE v.artifact_id = ? AND v.version = ?`, source.ID, source.Version).Scan(&name, &mediaType, &summary, &digest)
		if errors.Is(err, sql.ErrNoRows) {
			return KnowledgeSourceDocument{}, false, nil
		}
		if err != nil {
			return KnowledgeSourceDocument{}, false, fmt.Errorf("read knowledge artifact source: %w", err)
		}
		if len(digest) != sha256.Size {
			return KnowledgeSourceDocument{}, false, errors.New("knowledge artifact digest is invalid")
		}
		var contentDigest [sha256.Size]byte
		copy(contentDigest[:], digest)
		document.Body = strings.Join([]string{name, mediaType, summary}, "\n")
		document.Revision = KnowledgeArtifactVersionRevision(source.ID, source.Version, contentDigest)
	}
	return document, true, nil
}

func knowledgeWorkBody(work Work) string {
	criteria := make([]string, len(work.AcceptanceCriteria))
	for index, criterion := range work.AcceptanceCriteria {
		criteria[index] = criterion.Body
	}
	constraints := make([]string, len(work.Constraints))
	for index, constraint := range work.Constraints {
		constraints[index] = constraint.Body
	}
	return strings.Join([]string{work.Goal, strings.Join(criteria, "\n"), strings.Join(constraints, "\n"), work.BlockingReason, work.Result}, "\n")
}

func replaceKnowledgeProjection(ctx context.Context, tx *sql.Tx, generation uint64, document KnowledgeSourceDocument) error {
	var rowID int64
	err := tx.QueryRowContext(ctx, `SELECT fts_rowid FROM knowledge_projection_rows WHERE generation = ? AND source_kind = ? AND source_id = ? AND source_version = ?`, generation, document.Source.Kind, document.Source.ID, document.Source.Version).Scan(&rowID)
	if err == nil {
		if _, err := tx.ExecContext(ctx, `DELETE FROM knowledge_fts WHERE rowid = ?`, rowID); err != nil {
			return fmt.Errorf("delete knowledge fts row: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM knowledge_projection_rows WHERE generation = ? AND source_kind = ? AND source_id = ? AND source_version = ?`, generation, document.Source.Kind, document.Source.ID, document.Source.Version); err != nil {
			return fmt.Errorf("delete knowledge projection row: %w", err)
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read knowledge projection row: %w", err)
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO knowledge_fts(source_kind, source_id, source_version, generation, revision, body) VALUES(?, ?, ?, ?, ?, ?)`, document.Source.Kind, document.Source.ID, document.Source.Version, generation, document.Revision[:], document.Body)
	if err != nil {
		return fmt.Errorf("insert knowledge fts row: %w", err)
	}
	rowID, err = result.LastInsertId()
	if err != nil {
		return fmt.Errorf("read knowledge fts row id: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO knowledge_projection_rows(generation, source_kind, source_id, source_version, revision, fts_rowid) VALUES(?, ?, ?, ?, ?, ?)`, generation, document.Source.Kind, document.Source.ID, document.Source.Version, document.Revision[:], rowID); err != nil {
		return fmt.Errorf("insert knowledge projection row: %w", err)
	}
	return nil
}

func advanceKnowledgeGeneration(ctx context.Context, tx *sql.Tx, generation, sequence uint64) error {
	result, err := tx.ExecContext(ctx, `UPDATE knowledge_generation_progress SET applied_sequence = ? WHERE generation = ? AND applied_sequence + 1 = ?`, sequence, generation, sequence)
	if err != nil {
		return fmt.Errorf("advance knowledge generation: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read knowledge generation advance: %w", err)
	}
	if affected != 1 {
		return fmt.Errorf("knowledge generation %d cannot acknowledge sequence %d", generation, sequence)
	}
	return nil
}

func validateKnowledgeSource(source KnowledgeSource) error {
	if _, err := uuid.Parse(source.ID); err != nil {
		return fmt.Errorf("invalid knowledge source id: %w", err)
	}
	switch source.Kind {
	case KnowledgeSourceMessage, KnowledgeSourceWork:
		if source.Version != 0 {
			return fmt.Errorf("knowledge source %s cannot have a version", source.Kind)
		}
	case KnowledgeSourceArtifactVersion:
		if source.Version == 0 {
			return errors.New("knowledge artifact source version must be positive")
		}
	default:
		return fmt.Errorf("unknown knowledge source kind %q", source.Kind)
	}
	return nil
}

func writeKnowledgeField(hash interface{ Write([]byte) (int, error) }, name, value string) {
	writeKnowledgeBytes(hash, []byte(name))
	writeKnowledgeBytes(hash, []byte(value))
}

func writeKnowledgeList(hash interface{ Write([]byte) (int, error) }, name string, values []string) {
	writeKnowledgeBytes(hash, []byte(name))
	writeKnowledgeUint64(hash, uint64(len(values)))
	for _, value := range values {
		writeKnowledgeBytes(hash, []byte(value))
	}
}

func writeKnowledgeBytes(hash interface{ Write([]byte) (int, error) }, value []byte) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	hash.Write(length[:])
	hash.Write(value)
}

func writeKnowledgeUint64(hash interface{ Write([]byte) (int, error) }, value uint64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], value)
	hash.Write(encoded[:])
}

func knowledgeHash(hash interface{ Sum([]byte) []byte }) [sha256.Size]byte {
	var revision [sha256.Size]byte
	copy(revision[:], hash.Sum(nil))
	return revision
}
