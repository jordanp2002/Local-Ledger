PRAGMA defer_foreign_keys = ON;

CREATE TABLE account_transfers (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    source_account_id INTEGER NOT NULL,
    destination_account_id INTEGER NOT NULL,
    amount_hundredths INTEGER NOT NULL,
    date TEXT NOT NULL,
    note TEXT NULL,
    idempotency_key TEXT NOT NULL COLLATE BINARY,
    fingerprint TEXT NOT NULL,
    reversal_of_transfer_id INTEGER NULL,
    status TEXT NOT NULL DEFAULT 'recorded',
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    FOREIGN KEY (source_account_id) REFERENCES accounts (id) ON DELETE RESTRICT,
    FOREIGN KEY (destination_account_id) REFERENCES accounts (id) ON DELETE RESTRICT,
    FOREIGN KEY (reversal_of_transfer_id) REFERENCES account_transfers (id) ON DELETE RESTRICT,
    CHECK (source_account_id <> destination_account_id),
    CHECK (typeof(amount_hundredths) = 'integer' AND amount_hundredths > 0),
    CHECK (
        date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'
        AND date(date) IS date
    ),
    CHECK (length(idempotency_key) > 0 AND idempotency_key = trim(idempotency_key)),
    CHECK (length(fingerprint) > 0),
    CHECK (reversal_of_transfer_id IS NULL OR reversal_of_transfer_id <> id),
    CHECK (status IN ('recorded', 'reversed'))
);

CREATE UNIQUE INDEX idx_account_transfers_idempotency_key ON account_transfers (idempotency_key);
CREATE INDEX idx_account_transfers_date_id ON account_transfers (date DESC, created_at DESC, id DESC);
CREATE INDEX idx_account_transfers_source_account ON account_transfers (source_account_id);
CREATE INDEX idx_account_transfers_destination_account ON account_transfers (destination_account_id);
CREATE UNIQUE INDEX idx_account_transfers_reversal_unique ON account_transfers (reversal_of_transfer_id) WHERE reversal_of_transfer_id IS NOT NULL;

DROP INDEX idx_account_entries_idempotency_key;
DROP INDEX idx_account_entries_account_date_id;
DROP INDEX idx_account_entries_account;
DROP INDEX idx_account_entries_reversal_unique;

ALTER TABLE account_entries RENAME TO account_entries_legacy;

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
    transfer_id INTEGER NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    FOREIGN KEY (account_id) REFERENCES accounts (id) ON DELETE RESTRICT,
    FOREIGN KEY (reversal_of_entry_id) REFERENCES account_entries (id) ON DELETE RESTRICT,
    FOREIGN KEY (transfer_id) REFERENCES account_transfers (id) ON DELETE RESTRICT,
    CHECK (
        (transfer_id IS NULL AND kind IN ('deposit', 'withdrawal', 'reconciliation', 'reversal'))
        OR (transfer_id IS NOT NULL AND kind IN ('transfer_out', 'transfer_in') AND reversal_of_entry_id IS NULL)
    ),
    CHECK (typeof(delta_hundredths) = 'integer' AND delta_hundredths != 0),
    CHECK (
        date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'
        AND date(date) IS date
    ),
    CHECK (length(idempotency_key) > 0 AND idempotency_key = trim(idempotency_key)),
    CHECK (length(fingerprint) > 0)
);

INSERT INTO account_entries (
    id,
    account_id,
    kind,
    delta_hundredths,
    date,
    note,
    idempotency_key,
    fingerprint,
    reversal_of_entry_id,
    transfer_id,
    created_at
)
SELECT
    id,
    account_id,
    kind,
    delta_hundredths,
    date,
    note,
    idempotency_key,
    fingerprint,
    reversal_of_entry_id,
    NULL,
    created_at
FROM account_entries_legacy;

DROP TABLE account_entries_legacy;

CREATE UNIQUE INDEX idx_account_entries_idempotency_key ON account_entries (idempotency_key);
CREATE INDEX idx_account_entries_account_date_id ON account_entries (account_id, date ASC, created_at ASC, id ASC);
CREATE INDEX idx_account_entries_account ON account_entries (account_id);
CREATE INDEX idx_account_entries_transfer ON account_entries (transfer_id);
CREATE UNIQUE INDEX idx_account_entries_transfer_kind ON account_entries (transfer_id, kind) WHERE transfer_id IS NOT NULL;
CREATE UNIQUE INDEX idx_account_entries_reversal_unique ON account_entries (reversal_of_entry_id) WHERE reversal_of_entry_id IS NOT NULL;

CREATE TRIGGER account_entries_transfer_insert_guard
BEFORE INSERT ON account_entries
WHEN NEW.transfer_id IS NOT NULL
BEGIN
    SELECT CASE
        WHEN NOT EXISTS (
            SELECT 1
            FROM account_transfers AS t
            WHERE t.id = NEW.transfer_id
              AND (
                  (NEW.kind = 'transfer_out' AND NEW.account_id = t.source_account_id AND NEW.delta_hundredths = -t.amount_hundredths)
                  OR (NEW.kind = 'transfer_in' AND NEW.account_id = t.destination_account_id AND NEW.delta_hundredths = t.amount_hundredths)
              )
        ) THEN RAISE(ABORT, 'account transfer entry does not match transfer')
    END;
END;

CREATE TRIGGER account_entries_transfer_update_guard
BEFORE UPDATE ON account_entries
WHEN OLD.transfer_id IS NOT NULL OR NEW.transfer_id IS NOT NULL
BEGIN
    SELECT RAISE(ABORT, 'account transfer entries are immutable');
END;

CREATE TRIGGER account_entries_transfer_delete_guard
BEFORE DELETE ON account_entries
WHEN OLD.transfer_id IS NOT NULL
BEGIN
    SELECT RAISE(ABORT, 'account transfer entries are immutable');
END;
