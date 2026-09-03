CREATE TABLE accounts (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL COLLATE NOCASE UNIQUE,
    type TEXT NOT NULL,
    opening_balance_hundredths INTEGER NOT NULL,
    active INTEGER NOT NULL DEFAULT 1,
    note TEXT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    CHECK (length(name) > 0 AND name = trim(name)),
    CHECK (type IN ('checking', 'savings', 'cash', 'other')),
    CHECK (typeof(opening_balance_hundredths) = 'integer'),
    CHECK (active IN (0, 1))
);
