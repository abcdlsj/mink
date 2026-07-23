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

	knowledgeapp "github.com/abcdlsj/sumi/internal/knowledge/application"
	"github.com/google/uuid"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

const (
	KnowledgeSourceMessage         = knowledgeapp.SourceMessage
	KnowledgeSourceWork            = knowledgeapp.SourceWork
	KnowledgeSourceArtifactVersion = knowledgeapp.SourceArtifactVersion

	KnowledgeIndexReady    = knowledgeapp.IndexReady
	KnowledgeIndexDegraded = knowledgeapp.IndexDegraded

	knowledgeFTSSchema = `CREATE VIRTUAL TABLE knowledge_fts USING fts5(
		source_kind UNINDEXED,
		source_id UNINDEXED,
		source_version UNINDEXED,
		revision UNINDEXED,
		body
	)`

	knowledgeSearchQueryMaxBytes     = 256
	knowledgeSearchTermsMax          = 8
	knowledgeSearchTermMaxBytes      = 64
	knowledgeSearchDefaultLimit      = 20
	knowledgeSearchMaxLimit          = 50
	knowledgeSearchCandidateLimit    = 400
	knowledgeSearchSnippetMaxBytes   = 512
	knowledgeSearchAggregateMaxBytes = 16 << 10
)

type KnowledgeSource = knowledgeapp.Source
type KnowledgeDirtySource = knowledgeapp.DirtySource
type KnowledgeIndexState = knowledgeapp.IndexState
type KnowledgeIndexHealth = knowledgeapp.IndexHealth

const (
	KnowledgeIndexHealthy = knowledgeapp.IndexHealthy
	KnowledgeIndexLagging = knowledgeapp.IndexLagging
	KnowledgeIndexCorrupt = knowledgeapp.IndexCorrupt
)

var errKnowledgeProjectionInvariant = errors.New("knowledge projection invariant violated")

type KnowledgeSourceDocument = knowledgeapp.SourceDocument
type KnowledgeSearchParams = knowledgeapp.SearchQuery
type KnowledgeSearchResult = knowledgeapp.SearchResult
type KnowledgeSearchOutput = knowledgeapp.SearchOutput

var (
	ErrKnowledgeSearchInvalid         = knowledgeapp.ErrSearchInvalid
	ErrKnowledgeSearchUnauthenticated = knowledgeapp.ErrSearchUnauthenticated
	errKnowledgeSearchCorrupt         = errors.New("knowledge search index is corrupt")
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
	return knowledgeHash(hash)
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
	return KnowledgeWorkRevision(KnowledgeWorkFields{
		Goal: work.Goal, AcceptanceCriteria: criteria, Constraints: constraints,
		BlockingReason: work.BlockingReason, Result: work.Result,
	})
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

func (s *Store) KnowledgeIndexState(ctx context.Context) (KnowledgeIndexState, error) {
	return readKnowledgeIndexState(ctx, s.db)
}

func (s *Store) RebuildKnowledgeIndex(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin knowledge rebuild: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DROP TABLE IF EXISTS knowledge_fts`); err != nil {
		return fmt.Errorf("drop knowledge fts: %w", err)
	}
	if _, err := tx.ExecContext(ctx, knowledgeFTSSchema); err != nil {
		return fmt.Errorf("create knowledge fts: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM knowledge_projection_rows`); err != nil {
		return fmt.Errorf("clear knowledge projections: %w", err)
	}
	documents, err := listKnowledgeSourceDocuments(ctx, tx)
	if err != nil {
		return err
	}
	for _, document := range documents {
		if err := replaceKnowledgeProjection(ctx, tx, document); err != nil {
			return err
		}
	}
	if err := verifyKnowledgeProjection(ctx, tx, documents); err != nil {
		return err
	}
	var highWater uint64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence), 0) FROM knowledge_dirty_sources`).Scan(&highWater); err != nil {
		return fmt.Errorf("read knowledge rebuild high water: %w", err)
	}
	result, err := tx.ExecContext(ctx, `UPDATE knowledge_index_state SET applied_sequence = ?, status = 'ready' WHERE singleton = 1`, highWater)
	if err != nil {
		return fmt.Errorf("activate knowledge index: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil || updated != 1 {
		return errors.New("activate knowledge index: state row unavailable")
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM knowledge_dirty_sources WHERE sequence <= ?`, highWater); err != nil {
		return fmt.Errorf("prune rebuilt knowledge sources: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit knowledge rebuild: %w", err)
	}
	return nil
}

func (s *Store) NextKnowledgeDirtySource(ctx context.Context) (KnowledgeDirtySource, bool, error) {
	state, err := s.KnowledgeIndexState(ctx)
	if err != nil {
		return KnowledgeDirtySource{}, false, err
	}
	return readNextKnowledgeDirtySource(ctx, s.db, state.AppliedSequence)
}

func (s *Store) ReadKnowledgeSourceDocument(ctx context.Context, source KnowledgeSource) (KnowledgeSourceDocument, bool, error) {
	if err := validateKnowledgeSource(source); err != nil {
		return KnowledgeSourceDocument{}, false, err
	}
	return readKnowledgeSourceDocument(ctx, s.db, source)
}

func (s *Store) ApplyKnowledgeProjection(ctx context.Context, sequence uint64, document KnowledgeSourceDocument) (bool, error) {
	if sequence == 0 || validateKnowledgeSource(document.Source) != nil {
		return false, errors.New("knowledge projection input is invalid")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin knowledge projection: %w", err)
	}
	defer tx.Rollback()
	state, err := readKnowledgeIndexState(ctx, tx)
	if err != nil {
		return false, err
	}
	if state.Status != KnowledgeIndexReady || sequence <= state.AppliedSequence {
		return false, errors.New("knowledge projection state changed")
	}
	dirty, found, err := readKnowledgeDirtySource(ctx, tx, sequence)
	if err != nil {
		return false, err
	}
	if !found {
		return false, errors.New("knowledge dirty source is unavailable")
	}
	current, currentFound, err := readKnowledgeSourceDocument(ctx, tx, dirty.Source)
	if err != nil {
		return false, err
	}
	applied := false
	if currentFound && current.Revision == dirty.Revision {
		if document.Source != current.Source || document.Revision != current.Revision || document.Body != current.Body {
			return false, errors.New("knowledge source document is not current")
		}
		if err := replaceKnowledgeProjection(ctx, tx, current); err != nil {
			return false, err
		}
		applied = true
	}
	result, err := tx.ExecContext(ctx, `UPDATE knowledge_index_state SET applied_sequence = ? WHERE singleton = 1 AND status = 'ready' AND applied_sequence < ?`, sequence, sequence)
	if err != nil {
		return false, fmt.Errorf("advance knowledge index: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil || updated != 1 {
		return false, errors.New("advance knowledge index: state changed")
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM knowledge_dirty_sources WHERE sequence <= ?`, sequence); err != nil {
		return false, fmt.Errorf("prune knowledge dirty sources: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit knowledge projection: %w", err)
	}
	return applied, nil
}

func (s *Store) CheckKnowledgeIndexHealth(ctx context.Context) (KnowledgeIndexHealth, error) {
	available, err := knowledgeSearchFTSAvailable(ctx, s.db)
	if err != nil {
		if errors.Is(classifyKnowledgeSearchError(err), errKnowledgeSearchCorrupt) {
			return KnowledgeIndexCorrupt, nil
		}
		return KnowledgeIndexHealthy, err
	}
	if !available {
		return KnowledgeIndexCorrupt, nil
	}
	state, err := s.KnowledgeIndexState(ctx)
	if err != nil {
		return KnowledgeIndexHealthy, err
	}
	if state.Status == KnowledgeIndexDegraded {
		return KnowledgeIndexCorrupt, nil
	}
	var pending bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM knowledge_dirty_sources WHERE sequence > ?)`, state.AppliedSequence).Scan(&pending); err != nil {
		return KnowledgeIndexHealthy, err
	}
	if pending {
		return KnowledgeIndexLagging, nil
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return KnowledgeIndexHealthy, err
	}
	defer tx.Rollback()
	documents, err := listKnowledgeSourceDocuments(ctx, tx)
	if err != nil {
		return KnowledgeIndexHealthy, err
	}
	if err := verifyKnowledgeProjection(ctx, tx, documents); err != nil {
		if errors.Is(err, errKnowledgeProjectionInvariant) || errors.Is(classifyKnowledgeSearchError(err), errKnowledgeSearchCorrupt) {
			return KnowledgeIndexCorrupt, nil
		}
		return KnowledgeIndexHealthy, err
	}
	return KnowledgeIndexHealthy, nil
}

func enqueueKnowledgeDirtySource(ctx context.Context, tx *sql.Tx, source KnowledgeSource, revision [sha256.Size]byte, now time.Time) (KnowledgeDirtySource, error) {
	if err := validateKnowledgeSource(source); err != nil || now.IsZero() {
		return KnowledgeDirtySource{}, errors.New("knowledge dirty source is invalid")
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO knowledge_dirty_sources(source_kind, source_id, source_version, revision, enqueued_at) VALUES(?, ?, ?, ?, ?)`, source.Kind, source.ID, source.Version, revision[:], unixNano(now))
	if err != nil {
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

func readKnowledgeIndexState(ctx context.Context, queryer knowledgeQueryer) (KnowledgeIndexState, error) {
	var state KnowledgeIndexState
	if err := queryer.QueryRowContext(ctx, `SELECT applied_sequence, status FROM knowledge_index_state WHERE singleton = 1`).Scan(&state.AppliedSequence, &state.Status); err != nil {
		return KnowledgeIndexState{}, fmt.Errorf("read knowledge index state: %w", err)
	}
	if state.Status != KnowledgeIndexReady && state.Status != KnowledgeIndexDegraded {
		return KnowledgeIndexState{}, errors.New("knowledge index state is invalid")
	}
	return state, nil
}

func readKnowledgeDirtySource(ctx context.Context, queryer knowledgeQueryer, sequence uint64) (KnowledgeDirtySource, bool, error) {
	return scanKnowledgeDirtySource(queryer.QueryRowContext(ctx, `SELECT sequence, source_kind, source_id, source_version, revision, enqueued_at FROM knowledge_dirty_sources WHERE sequence = ?`, sequence))
}

func readNextKnowledgeDirtySource(ctx context.Context, queryer knowledgeQueryer, after uint64) (KnowledgeDirtySource, bool, error) {
	return scanKnowledgeDirtySource(queryer.QueryRowContext(ctx, `SELECT sequence, source_kind, source_id, source_version, revision, enqueued_at FROM knowledge_dirty_sources WHERE sequence > ? ORDER BY sequence LIMIT 1`, after))
}

func scanKnowledgeDirtySource(row *sql.Row) (KnowledgeDirtySource, bool, error) {
	var entry KnowledgeDirtySource
	var revision []byte
	var enqueued int64
	err := row.Scan(&entry.Sequence, &entry.Source.Kind, &entry.Source.ID, &entry.Source.Version, &revision, &enqueued)
	if errors.Is(err, sql.ErrNoRows) {
		return KnowledgeDirtySource{}, false, nil
	}
	if err != nil {
		return KnowledgeDirtySource{}, false, fmt.Errorf("read knowledge dirty source: %w", err)
	}
	if len(revision) != sha256.Size || validateKnowledgeSource(entry.Source) != nil {
		return KnowledgeDirtySource{}, false, errors.New("knowledge dirty source is invalid")
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
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate knowledge message sources: %w", err)
	}
	rows.Close()

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
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate knowledge work sources: %w", err)
	}
	rows.Close()

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
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate knowledge artifact sources: %w", err)
	}
	rows.Close()
	return documents, nil
}

func verifyKnowledgeProjection(ctx context.Context, tx *sql.Tx, documents []KnowledgeSourceDocument) error {
	available, err := knowledgeSearchFTSAvailable(ctx, tx)
	if err != nil {
		return err
	}
	if !available {
		return fmt.Errorf("%w: knowledge fts unavailable", errKnowledgeProjectionInvariant)
	}
	for _, document := range documents {
		var rowID int64
		var revision []byte
		err := tx.QueryRowContext(ctx, `SELECT fts_rowid, revision FROM knowledge_projection_rows WHERE source_kind = ? AND source_id = ? AND source_version = ?`, document.Source.Kind, document.Source.ID, document.Source.Version).Scan(&rowID, &revision)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: knowledge projection missing", errKnowledgeProjectionInvariant)
		}
		if err != nil {
			return err
		}
		var source KnowledgeSource
		var ftsRevision []byte
		var body string
		if rowID <= 0 || len(revision) != sha256.Size {
			return fmt.Errorf("%w: knowledge projection row invalid", errKnowledgeProjectionInvariant)
		}
		err = tx.QueryRowContext(ctx, `SELECT source_kind, source_id, source_version, revision, body FROM knowledge_fts WHERE rowid = ?`, rowID).Scan(&source.Kind, &source.ID, &source.Version, &ftsRevision, &body)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: knowledge fts projection missing", errKnowledgeProjectionInvariant)
		}
		if err != nil {
			return err
		}
		if source != document.Source || string(revision) != string(document.Revision[:]) || string(ftsRevision) != string(document.Revision[:]) || body != document.Body {
			return fmt.Errorf("%w: knowledge projection stale", errKnowledgeProjectionInvariant)
		}
	}
	var projectionCount, ftsCount int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM knowledge_projection_rows`).Scan(&projectionCount); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM knowledge_fts`).Scan(&ftsCount); err != nil {
		return err
	}
	if projectionCount != len(documents) || ftsCount != len(documents) {
		return fmt.Errorf("%w: knowledge projection count mismatch", errKnowledgeProjectionInvariant)
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
	default:
		return KnowledgeSourceDocument{}, false, errors.New("knowledge source kind is invalid")
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

func replaceKnowledgeProjection(ctx context.Context, tx *sql.Tx, document KnowledgeSourceDocument) error {
	var rowID int64
	err := tx.QueryRowContext(ctx, `SELECT fts_rowid FROM knowledge_projection_rows WHERE source_kind = ? AND source_id = ? AND source_version = ?`, document.Source.Kind, document.Source.ID, document.Source.Version).Scan(&rowID)
	if err == nil {
		if _, err := tx.ExecContext(ctx, `DELETE FROM knowledge_fts WHERE rowid = ?`, rowID); err != nil {
			return fmt.Errorf("delete knowledge fts row: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM knowledge_projection_rows WHERE source_kind = ? AND source_id = ? AND source_version = ?`, document.Source.Kind, document.Source.ID, document.Source.Version); err != nil {
			return fmt.Errorf("delete knowledge projection row: %w", err)
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read knowledge projection row: %w", err)
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO knowledge_fts(source_kind, source_id, source_version, revision, body) VALUES(?, ?, ?, ?, ?)`, document.Source.Kind, document.Source.ID, document.Source.Version, document.Revision[:], document.Body)
	if err != nil {
		return fmt.Errorf("insert knowledge fts row: %w", err)
	}
	rowID, err = result.LastInsertId()
	if err != nil {
		return fmt.Errorf("read knowledge fts row id: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO knowledge_projection_rows(source_kind, source_id, source_version, revision, fts_rowid) VALUES(?, ?, ?, ?, ?)`, document.Source.Kind, document.Source.ID, document.Source.Version, document.Revision[:], rowID); err != nil {
		return fmt.Errorf("insert knowledge projection row: %w", err)
	}
	return nil
}

func validateKnowledgeSource(source KnowledgeSource) error {
	parsed, err := uuid.Parse(source.ID)
	if err != nil || parsed.String() != source.ID {
		return errors.New("invalid knowledge source id")
	}
	switch source.Kind {
	case KnowledgeSourceMessage, KnowledgeSourceWork:
		if source.Version != 0 {
			return errors.New("knowledge source cannot have a version")
		}
	case KnowledgeSourceArtifactVersion:
		if source.Version == 0 {
			return errors.New("knowledge artifact source version must be positive")
		}
	default:
		return errors.New("unknown knowledge source kind")
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
