CREATE TABLE categories (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL COLLATE NOCASE UNIQUE,
    active INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    CHECK (length(name) > 0 AND name = trim(name)),
    CHECK (active IN (0, 1))
);

CREATE TABLE budgets (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    category_id INTEGER NOT NULL,
    month TEXT NOT NULL,
    amount_hundredths INTEGER NOT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    FOREIGN KEY (category_id) REFERENCES categories (id) ON DELETE RESTRICT,
    UNIQUE (category_id, month),
    CHECK (
        month GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]'
        AND substr(month, 6, 2) BETWEEN '01' AND '12'
    ),
    CHECK (typeof(amount_hundredths) = 'integer' AND amount_hundredths >= 0)
);

CREATE INDEX idx_budgets_month ON budgets (month);

CREATE TABLE transactions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    merchant TEXT NOT NULL,
    amount_hundredths INTEGER NOT NULL,
    date TEXT NOT NULL,
    category_id INTEGER NOT NULL,
    note TEXT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    FOREIGN KEY (category_id) REFERENCES categories (id) ON DELETE RESTRICT,
    CHECK (length(merchant) > 0 AND merchant = trim(merchant)),
    CHECK (
        date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'
        AND date(date) IS date
    ),
    CHECK (typeof(amount_hundredths) = 'integer' AND amount_hundredths > 0)
);

CREATE INDEX idx_transactions_date_id ON transactions (date DESC, id DESC);
CREATE INDEX idx_transactions_category_date_id
    ON transactions (category_id, date DESC, id DESC);

CREATE TABLE known_merchants (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    merchant TEXT NOT NULL COLLATE NOCASE UNIQUE,
    category_id INTEGER NOT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    FOREIGN KEY (category_id) REFERENCES categories (id) ON DELETE RESTRICT,
    CHECK (length(merchant) > 0 AND merchant = trim(merchant))
);
