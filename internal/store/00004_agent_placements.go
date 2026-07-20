package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/pressly/goose/v3"
)

func init() {
	goose.AddMigrationContext(upAgentPlacements, downAgentPlacements)
}

func upAgentPlacements(ctx context.Context, tx *sql.Tx) error {
	if err := createVersionFourIdentityTables(ctx, tx); err != nil {
		return err
	}
	agents, err := copyVersionThreeAgents(ctx, tx)
	if err != nil {
		return err
	}
	if err := copyVersionThreeComputers(ctx, tx); err != nil {
		return err
	}
	if err := copyVersionThreeAgentReceipts(ctx, tx, agents); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		DROP TABLE agent_create_requests;
		DROP TABLE agents;
		DROP TABLE computers;
		ALTER TABLE computers_v4 RENAME TO computers;
		ALTER TABLE agents_v4 RENAME TO agents;
		ALTER TABLE agent_create_requests_v4 RENAME TO agent_create_requests;
	`); err != nil {
		return fmt.Errorf("replace version three identity tables: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		CREATE TABLE agent_placements (
			agent_id TEXT PRIMARY KEY REFERENCES agents(id) ON DELETE RESTRICT,
			computer_id TEXT NOT NULL REFERENCES computers(id) ON DELETE RESTRICT,
			generation INTEGER NOT NULL CHECK (generation > 0),
			state TEXT NOT NULL CHECK (state IN ('pending', 'active', 'failed')),
			error_code TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			CHECK (
				(state = 'failed' AND length(error_code) BETWEEN 1 AND 64)
				OR (state IN ('pending', 'active') AND error_code = '')
			)
		);
		CREATE INDEX agent_placements_computer_state_agent
		ON agent_placements(computer_id, state, agent_id);
	`); err != nil {
		return fmt.Errorf("create agent placements: %w", err)
	}
	return nil
}

func downAgentPlacements(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `
		DROP INDEX agent_placements_computer_state_agent;
		DROP TABLE agent_placements;
	`); err != nil {
		return fmt.Errorf("drop agent placements: %w", err)
	}
	return nil
}

func createVersionFourIdentityTables(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `
		CREATE TABLE computers_v4 (
			id TEXT PRIMARY KEY CHECK (length(id) = 36),
			registration_key_hash BLOB NOT NULL UNIQUE CHECK (length(registration_key_hash) = 32),
			name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 100),
			os TEXT NOT NULL CHECK (os IN ('macos', 'linux')),
			arch TEXT NOT NULL CHECK (arch IN ('arm64', 'amd64')),
			created_at INTEGER NOT NULL,
			last_seen_at INTEGER NOT NULL
		);
		CREATE TABLE agents_v4 (
			id TEXT PRIMARY KEY CHECK (length(id) = 36),
			name TEXT NOT NULL UNIQUE CHECK (length(name) BETWEEN 1 AND 32),
			description TEXT NOT NULL CHECK (length(description) <= 1000),
			driver TEXT NOT NULL CHECK (driver IN ('native', 'codex', 'claude')),
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		);
		CREATE TABLE agent_create_requests_v4 (
			request_id TEXT PRIMARY KEY CHECK (length(request_id) = 36),
			agent_id TEXT NOT NULL UNIQUE REFERENCES agents_v4(id) ON DELETE RESTRICT,
			payload_fingerprint BLOB NOT NULL CHECK (length(payload_fingerprint) = 32)
		);
	`); err != nil {
		return fmt.Errorf("create version four identity tables: %w", err)
	}
	return nil
}

func copyVersionThreeComputers(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT id, registration_key_hash, name, os, arch, created_at, last_seen_at
		FROM computers
	`)
	if err != nil {
		return fmt.Errorf("read version three computers: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, name, operatingSystem, architecture string
		var keyHash []byte
		var createdValue, lastSeenValue any
		if err := rows.Scan(&id, &keyHash, &name, &operatingSystem, &architecture, &createdValue, &lastSeenValue); err != nil {
			return fmt.Errorf("scan version three computer: %w", err)
		}
		createdAt, err := migrationUnixNano(createdValue)
		if err != nil {
			return fmt.Errorf("computer %s created_at: %w", id, err)
		}
		lastSeenAt, err := migrationUnixNano(lastSeenValue)
		if err != nil {
			return fmt.Errorf("computer %s last_seen_at: %w", id, err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO computers_v4(id, registration_key_hash, name, os, arch, created_at, last_seen_at)
			VALUES(?, ?, ?, ?, ?, ?, ?)
		`, id, keyHash, name, operatingSystem, architecture, createdAt, lastSeenAt); err != nil {
			return fmt.Errorf("copy version three computer %s: %w", id, err)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate version three computers: %w", err)
	}
	return nil
}

type migrationAgent struct {
	name        string
	description string
	driver      string
}

func copyVersionThreeAgents(ctx context.Context, tx *sql.Tx) (map[string]migrationAgent, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT id, name, description, driver, created_at, updated_at
		FROM agents
	`)
	if err != nil {
		return nil, fmt.Errorf("read version three agents: %w", err)
	}
	defer rows.Close()
	agents := make(map[string]migrationAgent)
	for rows.Next() {
		var id, name, description, driver string
		var createdValue, updatedValue any
		if err := rows.Scan(&id, &name, &description, &driver, &createdValue, &updatedValue); err != nil {
			return nil, fmt.Errorf("scan version three agent: %w", err)
		}
		createdAt, err := migrationUnixNano(createdValue)
		if err != nil {
			return nil, fmt.Errorf("agent %s created_at: %w", id, err)
		}
		updatedAt, err := migrationUnixNano(updatedValue)
		if err != nil {
			return nil, fmt.Errorf("agent %s updated_at: %w", id, err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO agents_v4(id, name, description, driver, created_at, updated_at)
			VALUES(?, ?, ?, ?, ?, ?)
		`, id, name, description, driver, createdAt, updatedAt); err != nil {
			return nil, fmt.Errorf("copy version three agent %s: %w", id, err)
		}
		agents[id] = migrationAgent{name: name, description: description, driver: driver}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate version three agents: %w", err)
	}
	return agents, nil
}

func copyVersionThreeAgentReceipts(ctx context.Context, tx *sql.Tx, agents map[string]migrationAgent) error {
	var hasFingerprint bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM pragma_table_info('agent_create_requests')
			WHERE name = 'payload_fingerprint'
		)
	`).Scan(&hasFingerprint); err != nil {
		return fmt.Errorf("inspect version three agent receipts: %w", err)
	}
	if hasFingerprint {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO agent_create_requests_v4(request_id, agent_id, payload_fingerprint)
			SELECT request_id, agent_id, payload_fingerprint
			FROM agent_create_requests
		`)
		if err != nil {
			return fmt.Errorf("copy version three agent receipts: %w", err)
		}
		return nil
	}
	rows, err := tx.QueryContext(ctx, "SELECT request_id, agent_id FROM agent_create_requests")
	if err != nil {
		return fmt.Errorf("read legacy agent receipts: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var requestID, agentID string
		if err := rows.Scan(&requestID, &agentID); err != nil {
			return fmt.Errorf("scan legacy agent receipt: %w", err)
		}
		agent, ok := agents[agentID]
		if !ok {
			return fmt.Errorf("legacy agent receipt %s references unknown agent %s", requestID, agentID)
		}
		fingerprint, err := agentPayloadFingerprint(CreateAgentParams{
			Name:        agent.name,
			Description: agent.description,
			Driver:      agent.driver,
		})
		if err != nil {
			return fmt.Errorf("fingerprint legacy agent receipt %s: %w", requestID, err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO agent_create_requests_v4(request_id, agent_id, payload_fingerprint)
			VALUES(?, ?, ?)
		`, requestID, agentID, fingerprint[:]); err != nil {
			return fmt.Errorf("copy legacy agent receipt %s: %w", requestID, err)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate legacy agent receipts: %w", err)
	}
	return nil
}

func migrationUnixNano(value any) (int64, error) {
	switch value := value.(type) {
	case int64:
		return value, nil
	case string:
		parsed, err := time.Parse(time.RFC3339Nano, value)
		if err != nil {
			return 0, fmt.Errorf("parse %q as RFC3339: %w", value, err)
		}
		return parsed.UnixNano(), nil
	default:
		return 0, fmt.Errorf("unsupported timestamp type %T", value)
	}
}
