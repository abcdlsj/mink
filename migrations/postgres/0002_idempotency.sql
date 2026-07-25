CREATE TABLE idempotency_records (
    scope TEXT NOT NULL,
    idempotency_key UUID NOT NULL,
    request_hash BYTEA NOT NULL,
    response_status SMALLINT,
    response_json JSONB,
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (scope, idempotency_key),
    CHECK (
        (response_status IS NULL AND response_json IS NULL)
        OR (response_status BETWEEN 200 AND 299 AND response_json IS NOT NULL)
    ),
    CHECK (expires_at > created_at)
);

CREATE INDEX idempotency_records_expiry_idx ON idempotency_records (expires_at);
