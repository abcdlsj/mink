package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

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

