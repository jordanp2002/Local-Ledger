CREATE TABLE transaction_imports (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    idempotency_key TEXT NOT NULL COLLATE BINARY UNIQUE,
    request_fingerprint TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    CHECK (
        length(idempotency_key) > 0
        AND idempotency_key = trim(idempotency_key)
    )
);

CREATE TABLE transaction_import_items (
    import_id INTEGER NOT NULL,
    item_index INTEGER NOT NULL,
    transaction_id INTEGER NULL UNIQUE,
    category_source TEXT NOT NULL,
    merchant_mapping_action TEXT NOT NULL,
    PRIMARY KEY (import_id, item_index),
    FOREIGN KEY (import_id) REFERENCES transaction_imports (id) ON DELETE RESTRICT,
    FOREIGN KEY (transaction_id) REFERENCES transactions (id) ON DELETE SET NULL,
    CHECK (typeof(item_index) = 'integer' AND item_index >= 0)
);
