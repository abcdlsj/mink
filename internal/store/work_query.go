package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

func (s *Store) GetWork(ctx context.Context, params WorkReadParams) (Work, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Work{}, fmt.Errorf("begin work read: %w", err)
	}
	defer tx.Rollback()
	work, err := scanWork(tx.QueryRowContext(ctx, workSelect()+` WHERE id = ?`, params.WorkID))
	if errors.Is(err, sql.ErrNoRows) {
		return Work{}, ErrWorkNotFound
	} else if err != nil {
		return Work{}, err
	}
	if work.OrganizationID != params.Actor.OrganizationID {
		return Work{}, ErrWorkNotFound
	}
	if err := validatePrincipalInOrganization(ctx, tx, params.Actor, params.Actor.OrganizationID); err != nil {
		return Work{}, ErrPermissionDenied
	}
	if reason, err := requireGrant(ctx, tx, params.Actor, CapabilityWorkRead, Scope{Kind: "work", ID: work.ID}, params.Now, ""); err != nil {
		return Work{}, err
	} else if reason != "" {
		return Work{}, ErrPermissionDenied
	}
	if err := loadWorkParts(ctx, tx, &work); err != nil {
		return Work{}, err
	}
	if err := tx.Commit(); err != nil {
		return Work{}, fmt.Errorf("commit work read: %w", err)
	}
	return work, nil
}

func (s *Store) ListWorks(ctx context.Context, params ListWorksParams) ([]Work, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin work list: %w", err)
	}
	defer tx.Rollback()
	if err := validatePrincipalInOrganization(ctx, tx, params.Actor, params.Actor.OrganizationID); err != nil {
		return nil, ErrPermissionDenied
	}
	rows, err := tx.QueryContext(ctx, workSelect()+` WHERE organization_id = ? ORDER BY created_at, id`, params.Actor.OrganizationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var works []Work
	for rows.Next() {
		work, err := scanWork(rows)
		if err != nil {
			return nil, err
		}
		if reason, err := requireGrant(ctx, tx, params.Actor, CapabilityWorkRead, Scope{Kind: "work", ID: work.ID}, params.Now, ""); err != nil {
			return nil, err
		} else if reason != "" {
			continue
		}
		if err := loadWorkParts(ctx, tx, &work); err != nil {
			return nil, err
		}
		works = append(works, work)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return works, nil
}
