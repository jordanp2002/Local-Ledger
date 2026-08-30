CREATE TABLE recurring_transactions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    merchant TEXT NOT NULL,
    amount_hundredths INTEGER NOT NULL,
    category_id INTEGER NOT NULL,
    day_of_month INTEGER NOT NULL,
    note TEXT NULL,
    active INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    FOREIGN KEY (category_id) REFERENCES categories (id) ON DELETE RESTRICT,
    CHECK (length(merchant) > 0 AND merchant = trim(merchant)),
    CHECK (typeof(amount_hundredths) = 'integer' AND amount_hundredths > 0),
    CHECK (typeof(day_of_month) = 'integer' AND day_of_month BETWEEN 1 AND 31),
    CHECK (active IN (0, 1))
);

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
