CREATE TABLE savings_goals (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL COLLATE NOCASE UNIQUE,
    account_id INTEGER NOT NULL,
    target_amount_hundredths INTEGER NOT NULL,
    target_date TEXT NULL,
    note TEXT NULL,
    status TEXT NOT NULL DEFAULT 'active',
    completed_at TEXT NULL,
    cancelled_at TEXT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    FOREIGN KEY (account_id) REFERENCES accounts (id) ON DELETE RESTRICT,
    CHECK (length(name) > 0 AND name = trim(name)),
    CHECK (typeof(target_amount_hundredths) = 'integer' AND target_amount_hundredths > 0),
    CHECK (
        target_date IS NULL OR (
            target_date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'
            AND date(target_date) IS target_date
        )
    ),
    CHECK (status IN ('active', 'completed', 'cancelled'))
);

CREATE INDEX idx_savings_goals_account ON savings_goals (account_id);

CREATE TABLE savings_goal_entries (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    goal_id INTEGER NOT NULL,
    account_id INTEGER NOT NULL,
    delta_hundredths INTEGER NOT NULL,
    kind TEXT NOT NULL,
    date TEXT NOT NULL,
    note TEXT NULL,
    transfer_id INTEGER NULL,
    reversal_of_entry_id INTEGER NULL,
    idempotency_key TEXT NOT NULL,
    fingerprint TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    FOREIGN KEY (goal_id) REFERENCES savings_goals (id) ON DELETE RESTRICT,
    FOREIGN KEY (account_id) REFERENCES accounts (id) ON DELETE RESTRICT,
    FOREIGN KEY (transfer_id) REFERENCES account_transfers (id) ON DELETE RESTRICT,
    FOREIGN KEY (reversal_of_entry_id) REFERENCES savings_goal_entries (id) ON DELETE RESTRICT,
    CHECK (typeof(delta_hundredths) = 'integer' AND delta_hundredths != 0),
    CHECK (kind IN ('allocation', 'release', 'transfer_funding', 'cancellation_release', 'reversal')),
    CHECK (
        date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'
        AND date(date) IS date
    ),
    CHECK (length(idempotency_key) > 0 AND idempotency_key = trim(idempotency_key)),
    CHECK (length(fingerprint) > 0)
);

CREATE UNIQUE INDEX idx_savings_goal_entries_idempotency_key ON savings_goal_entries (idempotency_key);
CREATE UNIQUE INDEX idx_savings_goal_entries_reversal_unique ON savings_goal_entries (reversal_of_entry_id) WHERE reversal_of_entry_id IS NOT NULL;
CREATE INDEX idx_savings_goal_entries_goal ON savings_goal_entries (goal_id);
CREATE INDEX idx_savings_goal_entries_account ON savings_goal_entries (account_id);
CREATE INDEX idx_savings_goal_entries_transfer ON savings_goal_entries (transfer_id);
