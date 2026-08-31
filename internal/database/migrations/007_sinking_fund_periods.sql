CREATE TABLE sinking_fund_periods (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    category_id INTEGER NOT NULL,
    start_month TEXT NOT NULL,
    end_month TEXT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    FOREIGN KEY (category_id) REFERENCES categories (id) ON DELETE RESTRICT,
    CHECK (start_month GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]' AND substr(start_month, 6, 2) BETWEEN '01' AND '12'),
    CHECK (end_month IS NULL OR (end_month GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]' AND substr(end_month, 6, 2) BETWEEN '01' AND '12')),
    CHECK (end_month IS NULL OR end_month >= start_month)
);

CREATE UNIQUE INDEX idx_sinking_fund_periods_open_category
    ON sinking_fund_periods (category_id) WHERE end_month IS NULL;

CREATE INDEX idx_sinking_fund_periods_category_month
    ON sinking_fund_periods (category_id, start_month, end_month);
