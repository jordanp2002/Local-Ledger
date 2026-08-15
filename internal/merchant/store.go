package merchant

import (
	"context"
	"database/sql"
	"errors"

	"github.com/jordanp2002/local-finance-mcp/internal/contract"
)

// ListOptions contains optional filtering and pagination. Nil values use defaults.
type ListOptions struct {
	Query  string
	Limit  *int64
	Offset *int64
}

type SetResult struct {
	KnownMerchant    contract.KnownMerchant
	Created          bool
	PreviousCategory *string
	// TargetCategory is returned when the target category is inactive.
	TargetCategory contract.Category
}

type ListResult struct {
	KnownMerchants []contract.KnownMerchant
	Page           contract.Page
}

type Store struct {
	DB *sql.DB
}

// Set creates or replaces an exact merchant mapping in one write transaction.
func (s *Store) Set(ctx context.Context, merchantName, categoryName string) (SetResult, error) {
	merchantName, categoryName, validationErr := validateSetInputs(merchantName, categoryName)
	if validationErr != nil {
		return SetResult{}, validationErr
	}
	if s == nil || s.DB == nil {
		return SetResult{}, errors.New("merchant store database is nil")
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return SetResult{}, err
	}
	defer func() { _ = tx.Rollback() }()

	targetCategory, err := lookupCategory(ctx, tx, categoryName)
	if err != nil {
		return SetResult{}, err
	}
	if targetCategory == nil {
		return SetResult{}, &CategoryNotFoundError{Requested: categoryName}
	}
	if !targetCategory.Active {
		return SetResult{TargetCategory: *targetCategory}, &CategoryInactiveError{Category: *targetCategory}
	}

	existing, found, err := lookupKnownMerchant(ctx, tx, merchantName)
	if err != nil {
		return SetResult{}, err
	}
	if !found {
		result, err := tx.ExecContext(ctx, `
			INSERT INTO known_merchants (merchant, category_id)
			VALUES (?, ?)
		`, merchantName, targetCategory.ID)
		if err != nil {
			return SetResult{}, err
		}
		id, err := result.LastInsertId()
		if err != nil {
			return SetResult{}, err
		}
		created, err := getKnownMerchantByID(ctx, tx, id)
		if err != nil {
			return SetResult{}, err
		}
		if err := tx.Commit(); err != nil {
			return SetResult{}, err
		}
		return SetResult{KnownMerchant: created, Created: true}, nil
	}

	if existing.CategoryID == targetCategory.ID {
		if err := tx.Commit(); err != nil {
			return SetResult{}, err
		}
		return SetResult{KnownMerchant: *existing}, nil
	}

	previousCategory := existing.Category
	if _, err := tx.ExecContext(ctx, `
		UPDATE known_merchants
		SET category_id = ?,
		    updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
		WHERE id = ?
	`, targetCategory.ID, existing.ID); err != nil {
		return SetResult{}, err
	}
	updated, err := getKnownMerchantByID(ctx, tx, existing.ID)
	if err != nil {
		return SetResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return SetResult{}, err
	}
	return SetResult{
		KnownMerchant:    updated,
		PreviousCategory: &previousCategory,
	}, nil
}

// List returns a snapshot-consistent page of known merchant mappings.
func (s *Store) List(ctx context.Context, options ListOptions) (ListResult, []contract.FieldIssue, error) {
	effective, issues := validateListOptions(options)
	if len(issues) != 0 {
		return ListResult{}, issues, nil
	}
	if s == nil || s.DB == nil {
		return ListResult{}, nil, errors.New("merchant store database is nil")
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return ListResult{}, nil, err
	}
	defer func() { _ = tx.Rollback() }()

	where, args := merchantFilter(effective.query)
	var total int64
	countQuery := `SELECT count(*) FROM known_merchants AS m` + where
	if err := tx.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return ListResult{}, nil, err
	}

	pageArgs := append(append([]any(nil), args...), effective.limit, effective.offset)
	rows, err := tx.QueryContext(ctx, `
		SELECT
			m.id,
			m.merchant,
			m.category_id,
			c.name,
			c.active,
			m.created_at,
			m.updated_at
		FROM known_merchants AS m
		INNER JOIN categories AS c ON c.id = m.category_id`+where+`
		ORDER BY m.merchant COLLATE NOCASE ASC, m.id ASC
		LIMIT ? OFFSET ?
	`, pageArgs...)
	if err != nil {
		return ListResult{}, nil, err
	}
	defer rows.Close()

	knownMerchants := make([]contract.KnownMerchant, 0)
	for rows.Next() {
		knownMerchant, err := scanKnownMerchant(rows)
		if err != nil {
			return ListResult{}, nil, err
		}
		knownMerchants = append(knownMerchants, knownMerchant)
	}
	if err := rows.Err(); err != nil {
		return ListResult{}, nil, err
	}

	returned := int64(len(knownMerchants))
	hasMore := effective.offset < total && returned < total-effective.offset
	result := ListResult{
		KnownMerchants: knownMerchants,
		Page: contract.Page{
			Limit:    effective.limit,
			Offset:   effective.offset,
			Returned: returned,
			Total:    total,
			HasMore:  hasMore,
		},
	}
	if err := tx.Commit(); err != nil {
		return ListResult{}, nil, err
	}
	return result, nil, nil
}

const categoryColumns = `id, name, active, created_at, updated_at`

func lookupCategory(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, name string) (*contract.Category, error) {
	var category contract.Category
	err := q.QueryRowContext(ctx, `
		SELECT `+categoryColumns+`
		FROM categories
		WHERE name = ? COLLATE NOCASE
	`, name).Scan(
		&category.ID,
		&category.Name,
		&category.Active,
		&category.CreatedAt,
		&category.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &category, nil
}

const knownMerchantColumns = `
	m.id,
	m.merchant,
	m.category_id,
	c.name,
	c.active,
	m.created_at,
	m.updated_at
`

func lookupKnownMerchant(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, merchantName string) (*contract.KnownMerchant, bool, error) {
	knownMerchant, err := scanKnownMerchant(q.QueryRowContext(ctx, `
		SELECT `+knownMerchantColumns+`
		FROM known_merchants AS m
		INNER JOIN categories AS c ON c.id = m.category_id
		WHERE m.merchant = ? COLLATE NOCASE
	`, merchantName))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return &knownMerchant, true, nil
}

func getKnownMerchantByID(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, id int64) (contract.KnownMerchant, error) {
	return scanKnownMerchant(q.QueryRowContext(ctx, `
		SELECT `+knownMerchantColumns+`
		FROM known_merchants AS m
		INNER JOIN categories AS c ON c.id = m.category_id
		WHERE m.id = ?
	`, id))
}

func scanKnownMerchant(row interface{ Scan(dest ...any) error }) (contract.KnownMerchant, error) {
	var knownMerchant contract.KnownMerchant
	err := row.Scan(
		&knownMerchant.ID,
		&knownMerchant.Merchant,
		&knownMerchant.CategoryID,
		&knownMerchant.Category,
		&knownMerchant.CategoryActive,
		&knownMerchant.CreatedAt,
		&knownMerchant.UpdatedAt,
	)
	return knownMerchant, err
}

func merchantFilter(query string) (string, []any) {
	if query == "" {
		return "", nil
	}
	return `
		WHERE m.merchant LIKE '%' || ? || '%' ESCAPE '\'`, []any{escapeLikeLiteral(query)}
}
