CREATE TABLE budget_rollovers (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    category_id INTEGER NOT NULL,
    source_month TEXT NOT NULL,
    target_month TEXT NOT NULL,
    amount_hundredths INTEGER NOT NULL,
    source_transaction_id INTEGER NULL,
    note TEXT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    FOREIGN KEY (category_id) REFERENCES categories (id) ON DELETE RESTRICT,
    FOREIGN KEY (source_transaction_id) REFERENCES transactions (id) ON DELETE SET NULL,
    CHECK (
        source_month GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]'
        AND substr(source_month, 6, 2) BETWEEN '01' AND '12'
    ),
    CHECK (
        target_month GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]'
        AND substr(target_month, 6, 2) BETWEEN '01' AND '12'
    ),
    CHECK (typeof(amount_hundredths) = 'integer' AND amount_hundredths > 0)
);

CREATE INDEX idx_budget_rollovers_source_category
    ON budget_rollovers (source_month, category_id);

CREATE INDEX idx_budget_rollovers_target_category
    ON budget_rollovers (target_month, category_id);
