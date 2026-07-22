package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
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
	KnowledgeIndexCorrupt
)

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
	var exists bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'knowledge_fts')`).Scan(&exists); err != nil {
		return false, fmt.Errorf("read knowledge fts metadata: %w", err)
	}
	if !exists {
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
	documents, err := listKnowledgeSourceDocuments(ctx, tx)
	if err != nil {
		return KnowledgeIndexHealthy, err
	}
	if err := verifyKnowledgeGenerationProjection(ctx, tx, metadata.ActiveGeneration, documents); err != nil {
		return KnowledgeIndexCorrupt, nil
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
		return errors.New("knowledge fts is unavailable")
	}
	var probe int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM knowledge_fts WHERE knowledge_fts MATCH ?`, "knowledgecapabilityprobe").Scan(&probe); err != nil {
		return fmt.Errorf("read knowledge fts capability: %w", err)
	}
	for _, document := range documents {
		var rowID int64
		var revision []byte
		err := tx.QueryRowContext(ctx, `SELECT fts_rowid, revision FROM knowledge_projection_rows WHERE generation = ? AND source_kind = ? AND source_id = ? AND source_version = ?`, generation, document.Source.Kind, document.Source.ID, document.Source.Version).Scan(&rowID, &revision)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("knowledge generation %d lacks projection for %s %s", generation, document.Source.Kind, document.Source.ID)
		}
		if err != nil {
			return fmt.Errorf("read knowledge projection: %w", err)
		}
		if len(revision) != sha256.Size || string(revision) != string(document.Revision[:]) {
			return fmt.Errorf("knowledge generation %d projection revision is stale", generation)
		}
		var source KnowledgeSource
		var projectionGeneration uint64
		var ftsRevision []byte
		var body string
		if err := tx.QueryRowContext(ctx, `SELECT source_kind, source_id, source_version, generation, revision, body FROM knowledge_fts WHERE rowid = ?`, rowID).Scan(&source.Kind, &source.ID, &source.Version, &projectionGeneration, &ftsRevision, &body); err != nil {
			return fmt.Errorf("read knowledge fts projection: %w", err)
		}
		if source != document.Source || projectionGeneration != generation || len(ftsRevision) != sha256.Size || string(ftsRevision) != string(document.Revision[:]) || body != document.Body {
			return fmt.Errorf("knowledge generation %d fts projection is incomplete", generation)
		}
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM knowledge_projection_rows WHERE generation = ?`, generation).Scan(&count); err != nil {
		return fmt.Errorf("count knowledge projections: %w", err)
	}
	if count != len(documents) {
		return fmt.Errorf("knowledge generation %d has unexpected projection rows", generation)
	}
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM knowledge_fts WHERE generation = ?`, generation).Scan(&count); err != nil {
		return fmt.Errorf("count knowledge fts projections: %w", err)
	}
	if count != len(documents) {
		return fmt.Errorf("knowledge generation %d has unexpected fts rows", generation)
	}
	return nil
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
