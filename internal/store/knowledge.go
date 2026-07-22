package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
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
	next := metadata.ActiveGeneration + 1
	if _, err := tx.ExecContext(ctx, `INSERT INTO knowledge_index_generations(generation, state, created_at) VALUES(?, 'building', ?)`, next, unixNano(now)); err != nil {
		return KnowledgeIndexMetadata{}, fmt.Errorf("create knowledge generation: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE knowledge_index_metadata SET next_generation = ?, status = 'rebuilding' WHERE singleton = 1`, next); err != nil {
		return KnowledgeIndexMetadata{}, fmt.Errorf("start knowledge rebuild: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return KnowledgeIndexMetadata{}, fmt.Errorf("commit knowledge rebuild: %w", err)
	}
	return KnowledgeIndexMetadata{ActiveGeneration: metadata.ActiveGeneration, NextGeneration: next, Status: KnowledgeIndexRebuilding}, nil
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
	result, err := tx.ExecContext(ctx, `UPDATE knowledge_index_generations SET state = 'complete' WHERE generation = ? AND state = 'building'`, generation)
	if err != nil {
		return fmt.Errorf("complete knowledge generation: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read completed knowledge generation: %w", err)
	}
	if affected != 1 {
		return fmt.Errorf("knowledge generation %d is not building", generation)
	}
	var next uint64
	if err := tx.QueryRowContext(ctx, `SELECT next_generation FROM knowledge_index_metadata WHERE singleton = 1`).Scan(&next); err != nil {
		return fmt.Errorf("read next knowledge generation: %w", err)
	}
	if next != generation {
		return fmt.Errorf("knowledge generation %d is not the pending generation", generation)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit knowledge generation completion: %w", err)
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

func enqueueKnowledgeDirtySource(ctx context.Context, tx *sql.Tx, source KnowledgeSource, revision [sha256.Size]byte, now time.Time) (KnowledgeDirtySource, error) {
	if err := validateKnowledgeSource(source); err != nil {
		return KnowledgeDirtySource{}, err
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

func readKnowledgeIndexMetadata(ctx context.Context, queryer knowledgeQueryer) (KnowledgeIndexMetadata, error) {
	var metadata KnowledgeIndexMetadata
	if err := queryer.QueryRowContext(ctx, `SELECT active_generation, next_generation, status FROM knowledge_index_metadata WHERE singleton = 1`).Scan(&metadata.ActiveGeneration, &metadata.NextGeneration, &metadata.Status); err != nil {
		return KnowledgeIndexMetadata{}, fmt.Errorf("read knowledge index metadata: %w", err)
	}
	return metadata, nil
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
