CREATE TABLE account_entries (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    account_id INTEGER NOT NULL,
    kind TEXT NOT NULL,
    delta_hundredths INTEGER NOT NULL,
    date TEXT NOT NULL,
    note TEXT NULL,
    idempotency_key TEXT NOT NULL,
    fingerprint TEXT NOT NULL,
    reversal_of_entry_id INTEGER NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    FOREIGN KEY (account_id) REFERENCES accounts (id) ON DELETE RESTRICT,
    FOREIGN KEY (reversal_of_entry_id) REFERENCES account_entries (id) ON DELETE RESTRICT,
    CHECK (kind IN ('deposit', 'withdrawal', 'reconciliation', 'reversal')),
    CHECK (typeof(delta_hundredths) = 'integer' AND delta_hundredths != 0),
    CHECK (
        date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'
        AND date(date) IS date
    ),
    CHECK (length(idempotency_key) > 0 AND idempotency_key = trim(idempotency_key)),
    CHECK (length(fingerprint) > 0)
);

CREATE UNIQUE INDEX idx_account_entries_idempotency_key ON account_entries (idempotency_key);
CREATE INDEX idx_account_entries_account_date_id ON account_entries (account_id, date ASC, created_at ASC, id ASC);
CREATE INDEX idx_account_entries_account ON account_entries (account_id);
CREATE UNIQUE INDEX idx_account_entries_reversal_unique ON account_entries (reversal_of_entry_id) WHERE reversal_of_entry_id IS NOT NULL;

CREATE TABLE account_reconcile_noops (
    idempotency_key TEXT NOT NULL PRIMARY KEY COLLATE BINARY,
    request_fingerprint TEXT NOT NULL,
    account_id INTEGER NOT NULL,
    balance_hundredths INTEGER NOT NULL,
    previous_balance_hundredths INTEGER NOT NULL,
    date TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    FOREIGN KEY (account_id) REFERENCES accounts (id) ON DELETE RESTRICT,
    CHECK (length(idempotency_key) > 0 AND idempotency_key = trim(idempotency_key)),
    CHECK (length(request_fingerprint) > 0),
    CHECK (typeof(balance_hundredths) = 'integer'),
    CHECK (typeof(previous_balance_hundredths) = 'integer'),
    CHECK (
        date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'
        AND date(date) IS date
    )
);
