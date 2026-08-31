ALTER TABLE transactions RENAME TO transactions_legacy;
DROP INDEX idx_transactions_date_id;
DROP INDEX idx_transactions_category_date_id;

CREATE TABLE transactions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    merchant TEXT NOT NULL,
    date TEXT NOT NULL,
    note TEXT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    CHECK (length(merchant) > 0 AND merchant = trim(merchant)),
    CHECK (
        date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'
        AND date(date) IS date
    )
);

CREATE INDEX idx_transactions_date_id ON transactions (date DESC, id DESC);

CREATE TABLE transaction_allocations (
    transaction_id INTEGER NOT NULL,
    category_id INTEGER NOT NULL,
    amount_hundredths INTEGER NOT NULL,
    PRIMARY KEY (transaction_id, category_id),
    FOREIGN KEY (transaction_id) REFERENCES transactions (id) ON DELETE CASCADE,
    FOREIGN KEY (category_id) REFERENCES categories (id) ON DELETE RESTRICT,
    CHECK (typeof(amount_hundredths) = 'integer' AND amount_hundredths > 0)
);

CREATE INDEX idx_transaction_allocations_category
    ON transaction_allocations (category_id, transaction_id);

INSERT INTO transactions (id, merchant, date, note, created_at, updated_at)
SELECT id, merchant, date, note, created_at, updated_at
FROM transactions_legacy;

INSERT INTO transaction_allocations (transaction_id, category_id, amount_hundredths)
SELECT id, category_id, amount_hundredths
FROM transactions_legacy;

ALTER TABLE transaction_idempotency RENAME TO transaction_idempotency_legacy;
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
INSERT INTO transaction_idempotency
    (idempotency_key, request_fingerprint, transaction_id, category_source, merchant_mapping_action, created_at)
SELECT idempotency_key, request_fingerprint, transaction_id, category_source, merchant_mapping_action, created_at
FROM transaction_idempotency_legacy;
DROP TABLE transaction_idempotency_legacy;

ALTER TABLE transaction_import_items RENAME TO transaction_import_items_legacy;
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
INSERT INTO transaction_import_items
    (import_id, item_index, transaction_id, category_source, merchant_mapping_action)
SELECT import_id, item_index, transaction_id, category_source, merchant_mapping_action
FROM transaction_import_items_legacy;
DROP TABLE transaction_import_items_legacy;

ALTER TABLE recurring_transaction_runs RENAME TO recurring_transaction_runs_legacy;
CREATE TABLE recurring_transaction_runs (
    recurring_transaction_id INTEGER NOT NULL,
    month TEXT NOT NULL,
    transaction_id INTEGER NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    PRIMARY KEY (recurring_transaction_id, month),
    FOREIGN KEY (recurring_transaction_id) REFERENCES recurring_transactions (id) ON DELETE RESTRICT,
    FOREIGN KEY (transaction_id) REFERENCES transactions (id) ON DELETE SET NULL,
    CHECK (
        month GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]'
        AND substr(month, 6, 2) BETWEEN '01' AND '12'
    )
);
INSERT INTO recurring_transaction_runs
    (recurring_transaction_id, month, transaction_id, created_at)
SELECT recurring_transaction_id, month, transaction_id, created_at
FROM recurring_transaction_runs_legacy;
DROP TABLE recurring_transaction_runs_legacy;

DROP TABLE transactions_legacy;
