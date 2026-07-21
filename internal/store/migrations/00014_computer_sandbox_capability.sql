-- +goose Up
ALTER TABLE computers ADD COLUMN sandbox_provider TEXT NOT NULL DEFAULT 'unknown'
    CHECK (sandbox_provider IN ('unknown', 'trusted_local'));
ALTER TABLE computers ADD COLUMN sandbox_isolation TEXT NOT NULL DEFAULT 'unknown'
    CHECK (sandbox_isolation IN ('unknown', 'trusted_local'));
ALTER TABLE computers ADD COLUMN sandbox_workspace_access TEXT NOT NULL DEFAULT 'unknown'
    CHECK (sandbox_workspace_access IN ('unknown', 'direct_read_write'));
ALTER TABLE computers ADD COLUMN sandbox_process_control TEXT NOT NULL DEFAULT 'unknown'
    CHECK (sandbox_process_control IN ('unknown', 'context_process_group'));
ALTER TABLE computers ADD COLUMN sandbox_filesystem_isolation TEXT NOT NULL DEFAULT 'unknown'
    CHECK (sandbox_filesystem_isolation IN ('unknown', 'none'));
ALTER TABLE computers ADD COLUMN sandbox_network_isolation TEXT NOT NULL DEFAULT 'unknown'
    CHECK (sandbox_network_isolation IN ('unknown', 'none'));
ALTER TABLE computers ADD COLUMN sandbox_secret_materialization TEXT NOT NULL DEFAULT 'unknown'
    CHECK (sandbox_secret_materialization IN ('unknown', 'ephemeral_environment'));
ALTER TABLE computers ADD COLUMN sandbox_daemon_crash_cleanup TEXT NOT NULL DEFAULT 'unknown'
    CHECK (sandbox_daemon_crash_cleanup IN ('unknown', 'none'));
ALTER TABLE computers ADD COLUMN sandbox_declaration_revision INTEGER NOT NULL DEFAULT 0
    CHECK (sandbox_declaration_revision >= 0);

CREATE TABLE computer_pairing_sandbox_receipts (
    pairing_id TEXT PRIMARY KEY REFERENCES computer_pairings(id) ON DELETE RESTRICT,
    sandbox_provider TEXT NOT NULL CHECK (sandbox_provider IN ('unknown', 'trusted_local')),
    sandbox_isolation TEXT NOT NULL CHECK (sandbox_isolation IN ('unknown', 'trusted_local')),
    sandbox_workspace_access TEXT NOT NULL CHECK (sandbox_workspace_access IN ('unknown', 'direct_read_write')),
    sandbox_process_control TEXT NOT NULL CHECK (sandbox_process_control IN ('unknown', 'context_process_group')),
    sandbox_filesystem_isolation TEXT NOT NULL CHECK (sandbox_filesystem_isolation IN ('unknown', 'none')),
    sandbox_network_isolation TEXT NOT NULL CHECK (sandbox_network_isolation IN ('unknown', 'none')),
    sandbox_secret_materialization TEXT NOT NULL CHECK (sandbox_secret_materialization IN ('unknown', 'ephemeral_environment')),
    sandbox_daemon_crash_cleanup TEXT NOT NULL CHECK (sandbox_daemon_crash_cleanup IN ('unknown', 'none')),
    sandbox_declaration_revision INTEGER NOT NULL CHECK (sandbox_declaration_revision >= 0)
);

INSERT INTO computer_pairing_sandbox_receipts(
    pairing_id,
    sandbox_provider,
    sandbox_isolation,
    sandbox_workspace_access,
    sandbox_process_control,
    sandbox_filesystem_isolation,
    sandbox_network_isolation,
    sandbox_secret_materialization,
    sandbox_daemon_crash_cleanup,
    sandbox_declaration_revision
)
SELECT
    pairings.id,
    computers.sandbox_provider,
    computers.sandbox_isolation,
    computers.sandbox_workspace_access,
    computers.sandbox_process_control,
    computers.sandbox_filesystem_isolation,
    computers.sandbox_network_isolation,
    computers.sandbox_secret_materialization,
    computers.sandbox_daemon_crash_cleanup,
    computers.sandbox_declaration_revision
FROM computer_pairings AS pairings
JOIN computers ON computers.id = pairings.computer_id
WHERE pairings.consumed_at IS NOT NULL;

-- +goose Down
DROP TABLE computer_pairing_sandbox_receipts;
ALTER TABLE computers DROP COLUMN sandbox_declaration_revision;
ALTER TABLE computers DROP COLUMN sandbox_daemon_crash_cleanup;
ALTER TABLE computers DROP COLUMN sandbox_secret_materialization;
ALTER TABLE computers DROP COLUMN sandbox_network_isolation;
ALTER TABLE computers DROP COLUMN sandbox_filesystem_isolation;
ALTER TABLE computers DROP COLUMN sandbox_process_control;
ALTER TABLE computers DROP COLUMN sandbox_workspace_access;
ALTER TABLE computers DROP COLUMN sandbox_isolation;
ALTER TABLE computers DROP COLUMN sandbox_provider;
