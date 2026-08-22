CREATE TABLE transaction_idempotency (
    idempotency_key TEXT PRIMARY KEY COLLATE BINARY,
    request_fingerprint TEXT NOT NULL,
    transaction_id INTEGER NULL UNIQUE,
    category_source TEXT NOT NULL,
    merchant_mapping_action TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    FOREIGN KEY (transaction_id) REFERENCES transactions (id) ON DELETE SET NULL,
    CHECK (length(idempotency_key) > 0 AND idempotency_key = trim(idempotency_key))
);
