package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"
)

func (s *State) EnqueueOutbox(ctx context.Context, event OutboxEvent) error {
	if !validID(event.OutboxEventID) || !validID(event.RequestID) || !validID(event.AgentID) || !validID(event.RunID) || event.Attempt == 0 || event.PlacementDesiredRevision == 0 || event.Fence == 0 || (event.Outcome != CompletionSucceeded && event.Outcome != CompletionFailed) || (event.Outcome == CompletionSucceeded && event.ErrorCode != "") || (event.Outcome == CompletionFailed && event.ErrorCode == "") || len(event.ErrorCode) > 255 || event.CreatedAt.IsZero() || ValidateCompletionPayload(event.Body, event.MentionedAgentIDs) != nil {
		return errors.New("outbox event is invalid")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin enqueue outbox: %w", err)
	}
	defer tx.Rollback()
	existing, found, err := outboxCollision(ctx, tx, event)
	if err != nil {
		return err
	}
	if found {
		if !sameOutboxEvent(existing, event) {
			return errors.New("outbox event conflicts with persisted event")
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit outbox replay: %w", err)
		}
		return nil
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO outbox_events(
			outbox_event_id, request_id, agent_id, placement_desired_revision, run_id, attempt,
			fence, outcome, error_code, body, usage_input_units, usage_output_units, state, created_at
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'pending', ?)
	`, event.OutboxEventID, event.RequestID, event.AgentID, event.PlacementDesiredRevision, event.RunID, event.Attempt, event.Fence, event.Outcome, event.ErrorCode, event.Body, event.UsageInputUnits, event.UsageOutputUnits, unixNano(event.CreatedAt)); err != nil {
		return fmt.Errorf("persist outbox event: %w", err)
	}
	for ordinal, agentID := range event.MentionedAgentIDs {
		if !validID(agentID) {
			return errors.New("outbox mention agent id is invalid")
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO outbox_mentions(outbox_event_id, ordinal, agent_id) VALUES(?, ?, ?)`, event.OutboxEventID, ordinal, agentID); err != nil {
			return fmt.Errorf("persist outbox mention: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit enqueue outbox: %w", err)
	}
	return s.secureSQLiteFiles()
}

const (
	maxCompletionBodyRunes = 400_000
	maxCompletionMentions  = 64
)

func ValidateCompletionPayload(body string, mentionedAgentIDs []string) error {
	if !utf8.ValidString(body) {
		return errors.New("completion body must be valid UTF-8")
	}
	runes := utf8.RuneCountInString(body)
	if runes < 1 || runes > maxCompletionBodyRunes || len(mentionedAgentIDs) > maxCompletionMentions {
		return errors.New("completion payload exceeds its limits")
	}
	seen := make(map[string]struct{}, len(mentionedAgentIDs))
	for _, agentID := range mentionedAgentIDs {
		if !validID(agentID) {
			return errors.New("completion mention agent ID is invalid")
		}
		if _, found := seen[agentID]; found {
			return errors.New("completion mention agent ID is duplicated")
		}
		seen[agentID] = struct{}{}
	}
	return nil
}

func outboxCollision(ctx context.Context, tx *sql.Tx, candidate OutboxEvent) (OutboxEvent, bool, error) {
	var event OutboxEvent
	var createdAt int64
	err := tx.QueryRowContext(ctx, `
		SELECT sequence, outbox_event_id, request_id, agent_id, placement_desired_revision, run_id, attempt,
		       fence, outcome, error_code, body, usage_input_units, usage_output_units, state, rejection_code, created_at, attempts
		FROM outbox_events
		WHERE outbox_event_id = ? OR request_id = ? OR (run_id = ? AND attempt = ? AND fence = ?)
		LIMIT 1
	`, candidate.OutboxEventID, candidate.RequestID, candidate.RunID, candidate.Attempt, candidate.Fence).Scan(
		&event.Sequence, &event.OutboxEventID, &event.RequestID, &event.AgentID, &event.PlacementDesiredRevision,
		&event.RunID, &event.Attempt, &event.Fence, &event.Outcome, &event.ErrorCode, &event.Body,
		&event.UsageInputUnits, &event.UsageOutputUnits, &event.State,
		&event.RejectionCode, &createdAt, &event.Attempts,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return OutboxEvent{}, false, nil
	}
	if err != nil {
		return OutboxEvent{}, false, fmt.Errorf("read outbox collision: %w", err)
	}
	event.CreatedAt = fromUnixNano(createdAt)
	rows, err := tx.QueryContext(ctx, `SELECT agent_id FROM outbox_mentions WHERE outbox_event_id = ? ORDER BY ordinal`, event.OutboxEventID)
	if err != nil {
		return OutboxEvent{}, false, fmt.Errorf("list outbox collision mentions: %w", err)
	}
	for rows.Next() {
		var agentID string
		if err := rows.Scan(&agentID); err != nil {
			rows.Close()
			return OutboxEvent{}, false, fmt.Errorf("scan outbox collision mention: %w", err)
		}
		event.MentionedAgentIDs = append(event.MentionedAgentIDs, agentID)
	}
	if err := rows.Close(); err != nil {
		return OutboxEvent{}, false, fmt.Errorf("close outbox collision mentions: %w", err)
	}
	return event, true, nil
}

func sameOutboxEvent(existing, candidate OutboxEvent) bool {
	if existing.OutboxEventID != candidate.OutboxEventID || existing.RequestID != candidate.RequestID ||
		existing.AgentID != candidate.AgentID || existing.PlacementDesiredRevision != candidate.PlacementDesiredRevision ||
		existing.RunID != candidate.RunID || existing.Attempt != candidate.Attempt || existing.Fence != candidate.Fence ||
		existing.Outcome != candidate.Outcome || existing.ErrorCode != candidate.ErrorCode || existing.Body != candidate.Body ||
		existing.UsageInputUnits != candidate.UsageInputUnits || existing.UsageOutputUnits != candidate.UsageOutputUnits || existing.State != OutboxPending ||
		existing.RejectionCode != "" || !existing.CreatedAt.Equal(candidate.CreatedAt) ||
		len(existing.MentionedAgentIDs) != len(candidate.MentionedAgentIDs) {
		return false
	}
	for index := range existing.MentionedAgentIDs {
		if existing.MentionedAgentIDs[index] != candidate.MentionedAgentIDs[index] {
			return false
		}
	}
	return true
}

func (s *State) TombstoneOutbox(ctx context.Context, eventID, rejectionCode string) error {
	if !validID(eventID) || rejectionCode == "" {
		return errors.New("outbox tombstone is invalid")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tombstone outbox: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM outbox_mentions WHERE outbox_event_id = ?`, eventID); err != nil {
		return fmt.Errorf("delete tombstone mentions: %w", err)
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE outbox_events SET state = 'tombstone', body = '', rejection_code = ?
		WHERE outbox_event_id = ? AND state = 'pending'
	`, rejectionCode, eventID)
	if err != nil {
		return fmt.Errorf("tombstone outbox event: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil || updated != 1 {
		return errors.New("pending outbox event not found")
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tombstone outbox: %w", err)
	}
	return s.secureSQLiteFiles()
}

func (s *State) RecordOutboxAttempt(ctx context.Context, eventID string, attemptedAt time.Time) error {
	if !validID(eventID) || attemptedAt.IsZero() {
		return errors.New("outbox attempt is invalid")
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE outbox_events
		SET last_attempt_at = ?, attempts = attempts + 1
		WHERE outbox_event_id = ? AND state = 'pending'
	`, unixNano(attemptedAt), eventID)
	if err != nil {
		return fmt.Errorf("record outbox attempt: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil || updated != 1 {
		return errors.New("pending outbox event not found")
	}
	return s.secureSQLiteFiles()
}

func (s *State) AckOutbox(ctx context.Context, eventID string) error {
	if !validID(eventID) {
		return errors.New("outbox event id is invalid")
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM outbox_events WHERE outbox_event_id = ? AND state = 'pending'`, eventID)
	if err != nil {
		return fmt.Errorf("ack outbox event: %w", err)
	}
	deleted, err := result.RowsAffected()
	if err != nil || deleted != 1 {
		return errors.New("pending outbox event not found")
	}
	return s.secureSQLiteFiles()
}

func (s *State) Outbox(ctx context.Context) ([]OutboxEvent, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT sequence, outbox_event_id, request_id, agent_id, placement_desired_revision, run_id, attempt,
		       fence, outcome, error_code, body, usage_input_units, usage_output_units, state, rejection_code, created_at, last_attempt_at, attempts
		FROM outbox_events ORDER BY sequence
	`)
	if err != nil {
		return nil, fmt.Errorf("list outbox events: %w", err)
	}
	var events []OutboxEvent
	for rows.Next() {
		var event OutboxEvent
		var createdAt int64
		var lastAttemptAt sql.NullInt64
		if err := rows.Scan(&event.Sequence, &event.OutboxEventID, &event.RequestID, &event.AgentID, &event.PlacementDesiredRevision, &event.RunID, &event.Attempt, &event.Fence, &event.Outcome, &event.ErrorCode, &event.Body, &event.UsageInputUnits, &event.UsageOutputUnits, &event.State, &event.RejectionCode, &createdAt, &lastAttemptAt, &event.Attempts); err != nil {
			return nil, fmt.Errorf("scan outbox event: %w", err)
		}
		event.CreatedAt = fromUnixNano(createdAt)
		if lastAttemptAt.Valid {
			value := fromUnixNano(lastAttemptAt.Int64)
			event.LastAttemptAt = &value
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate outbox events: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close outbox events: %w", err)
	}
	for index := range events {
		mentions, err := s.outboxMentions(ctx, events[index].OutboxEventID)
		if err != nil {
			return nil, err
		}
		events[index].MentionedAgentIDs = mentions
	}
	return events, nil
}

func (s *State) PendingOutbox(ctx context.Context, limit uint32) ([]OutboxEvent, error) {
	if limit == 0 || limit > 200 {
		return nil, errors.New("pending outbox limit is invalid")
	}
	rows, err := s.db.QueryContext(ctx, `
		WITH pending AS (
			SELECT sequence, outbox_event_id, request_id, agent_id, placement_desired_revision, run_id, attempt,
			       fence, outcome, error_code, body, usage_input_units, usage_output_units, state, rejection_code, created_at, last_attempt_at, attempts
			FROM outbox_events
			WHERE state = 'pending'
			ORDER BY sequence
			LIMIT ?
		)
		SELECT pending.sequence, pending.outbox_event_id, pending.request_id, pending.agent_id,
		       pending.placement_desired_revision, pending.run_id, pending.attempt, pending.fence,
		       pending.outcome, pending.error_code, pending.body, pending.usage_input_units, pending.usage_output_units,
		       pending.state, pending.rejection_code, pending.created_at,
		       pending.last_attempt_at, pending.attempts, outbox_mentions.agent_id
		FROM pending
		LEFT JOIN outbox_mentions ON outbox_mentions.outbox_event_id = pending.outbox_event_id
		ORDER BY pending.sequence, outbox_mentions.ordinal
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("list pending outbox events: %w", err)
	}
	defer rows.Close()
	var events []OutboxEvent
	for rows.Next() {
		var event OutboxEvent
		var createdAt int64
		var lastAttemptAt sql.NullInt64
		var mentionedAgentID sql.NullString
		if err := rows.Scan(
			&event.Sequence, &event.OutboxEventID, &event.RequestID, &event.AgentID,
			&event.PlacementDesiredRevision, &event.RunID, &event.Attempt, &event.Fence,
			&event.Outcome, &event.ErrorCode, &event.Body, &event.UsageInputUnits, &event.UsageOutputUnits,
			&event.State, &event.RejectionCode, &createdAt,
			&lastAttemptAt, &event.Attempts, &mentionedAgentID,
		); err != nil {
			return nil, fmt.Errorf("scan pending outbox event: %w", err)
		}
		if len(events) == 0 || events[len(events)-1].Sequence != event.Sequence {
			event.CreatedAt = fromUnixNano(createdAt)
			if lastAttemptAt.Valid {
				value := fromUnixNano(lastAttemptAt.Int64)
				event.LastAttemptAt = &value
			}
			events = append(events, event)
		}
		if mentionedAgentID.Valid {
			last := &events[len(events)-1]
			last.MentionedAgentIDs = append(last.MentionedAgentIDs, mentionedAgentID.String)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pending outbox events: %w", err)
	}
	return events, nil
}

func (s *State) HasOutboxCompletion(ctx context.Context, runID string, attempt, fence uint64) (bool, error) {
	if !validID(runID) || attempt == 0 || fence == 0 {
		return false, errors.New("outbox completion binding is invalid")
	}
	var found bool
	if err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM outbox_events
			WHERE run_id = ? AND attempt = ? AND fence = ?
		)
	`, runID, attempt, fence).Scan(&found); err != nil {
		return false, fmt.Errorf("check outbox completion: %w", err)
	}
	return found, nil
}

func (s *State) outboxMentions(ctx context.Context, eventID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT agent_id FROM outbox_mentions WHERE outbox_event_id = ? ORDER BY ordinal`, eventID)
	if err != nil {
		return nil, fmt.Errorf("list outbox mentions: %w", err)
	}
	defer rows.Close()
	var mentions []string
	for rows.Next() {
		var agentID string
		if err := rows.Scan(&agentID); err != nil {
			return nil, fmt.Errorf("scan outbox mention: %w", err)
		}
		mentions = append(mentions, agentID)
	}
	return mentions, rows.Err()
}
