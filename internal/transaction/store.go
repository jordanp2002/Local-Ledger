// Package transaction implements add-transaction domain rules.
package transaction

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jordanp2002/local-finance-mcp/internal/contract"
)

var (
	ErrCategoryNotFound         = errors.New("category not found")
	ErrCategoryInactive         = errors.New("category inactive")
	ErrMerchantCategoryRequired = errors.New("merchant category required")
	ErrMerchantCategoryInactive = errors.New("merchant category inactive")
)

const (
	CategorySourceProvided      = "provided"
	CategorySourceKnownMerchant = "known_merchant"

	MappingActionCreated          = "created"
	MappingActionMatched          = "matched"
	MappingActionPreserved        = "preserved"
	MappingActionReplacedInactive = "replaced_inactive"
)

// AddInput is one add_transaction request at the store boundary.
type AddInput struct {
	Amount   string
	Merchant string
	Category *string
	Date     *string
	Note     *string
}

// AddResult is the canonical result of recording one transaction.
type AddResult struct {
	Transaction           contract.Transaction
	CategorySource        string
	MerchantMappingAction string
}

// Store owns transaction validation and persistence.
type Store struct {
	DB *sql.DB
	// Now supplies the current local time for date derivation. nil uses time.Now.
	Now func() time.Time
}

// CategoryNotFoundError identifies a missing supplied category.
type CategoryNotFoundError struct {
	Requested        string
	ActiveCategories []contract.Category
}

func (e *CategoryNotFoundError) Error() string {
	if e == nil {
		return ErrCategoryNotFound.Error()
	}
	return fmt.Sprintf("category %q not found", e.Requested)
}

func (e *CategoryNotFoundError) Is(target error) bool {
	return target == ErrCategoryNotFound
}

// CategoryInactiveError identifies an inactive supplied category.
type CategoryInactiveError struct {
	Category         contract.Category
	ActiveCategories []contract.Category
}

func (e *CategoryInactiveError) Error() string {
	if e == nil {
		return ErrCategoryInactive.Error()
	}
	return fmt.Sprintf("category %q is inactive", e.Category.Name)
}

func (e *CategoryInactiveError) Is(target error) bool {
	return target == ErrCategoryInactive
}

// MerchantCategoryRequiredError identifies an omitted category with no mapping.
type MerchantCategoryRequiredError struct {
	Merchant string
}

func (e *MerchantCategoryRequiredError) Error() string {
	if e == nil {
		return ErrMerchantCategoryRequired.Error()
	}
	return fmt.Sprintf("merchant %q has no exact category mapping", e.Merchant)
}

func (e *MerchantCategoryRequiredError) Is(target error) bool {
	return target == ErrMerchantCategoryRequired
}

// MerchantCategoryInactiveError identifies an omitted category whose mapping is inactive.
type MerchantCategoryInactiveError struct {
	KnownMerchant    contract.KnownMerchant
	ActiveCategories []contract.Category
}

func (e *MerchantCategoryInactiveError) Error() string {
	if e == nil {
		return ErrMerchantCategoryInactive.Error()
	}
	return fmt.Sprintf("merchant %q maps to inactive category %q", e.KnownMerchant.Merchant, e.KnownMerchant.Category)
}

func (e *MerchantCategoryInactiveError) Is(target error) bool {
	return target == ErrMerchantCategoryInactive
}

type validatedAdd struct {
	amountHundredths int64
	merchant         string
	category         *string
	date             string
	note             sql.NullString
}

// LocalDate formats YYYY-MM-DD in t's location without converting to UTC first.
func LocalDate(t time.Time) string {
	return t.Format("2006-01-02")
}

// Add validates and records one expense, applying exact merchant-default rules atomically.
func (s *Store) Add(ctx context.Context, in AddInput) (AddResult, []contract.FieldIssue, error) {
	now := time.Now()
	if s != nil && s.Now != nil {
		now = s.Now()
	}

	validated, fields := validateAdd(in, now)
	if len(fields) != 0 {
		return AddResult{}, fields, nil
	}
	if s == nil || s.DB == nil {
		return AddResult{}, nil, errors.New("transaction store database is nil")
	}
	return s.add(ctx, validated)
}

func validateAdd(in AddInput, now time.Time) (validatedAdd, []contract.FieldIssue) {
	fields := make([]contract.FieldIssue, 0)
	validated := validatedAdd{}

	amount, amountErr := contract.ParseAmount(in.Amount)
	if amountErr != nil {
		fields = append(fields, contract.FieldIssue{
			Field:  "amount",
			Reason: "must be a positive amount with at most two decimal places",
		})
	} else if amount == 0 {
		fields = append(fields, contract.FieldIssue{
			Field:  "amount",
			Reason: "must be greater than zero",
		})
	} else {
		validated.amountHundredths = amount
	}

	merchant := contract.TrimASCIIWhitespace(in.Merchant)
	switch {
	case merchant == "":
		fields = append(fields, contract.FieldIssue{
			Field:  "merchant",
			Reason: "must not be empty",
		})
	case strings.ContainsRune(merchant, '\x00'):
		fields = append(fields, contract.FieldIssue{
			Field:  "merchant",
			Reason: "must not contain NUL characters",
		})
	default:
		validated.merchant = merchant
	}

	if in.Category != nil {
		category := contract.TrimASCIIWhitespace(*in.Category)
		switch {
		case category == "":
			fields = append(fields, contract.FieldIssue{
				Field:  "category",
				Reason: "must not be empty",
			})
		case strings.ContainsRune(category, '\x00'):
			fields = append(fields, contract.FieldIssue{
				Field:  "category",
				Reason: "must not contain NUL characters",
			})
		default:
			validated.category = &category
		}
	}

	today := LocalDate(now)
	if in.Date != nil {
		parsed, dateErr := contract.ParseDate(*in.Date)
		if dateErr != nil {
			fields = append(fields, contract.FieldIssue{
				Field:  "date",
				Reason: "must be a valid YYYY-MM-DD date",
			})
		} else if parsed > today {
			fields = append(fields, contract.FieldIssue{
				Field:  "date",
				Reason: "must not be in the future",
			})
		} else {
			validated.date = parsed
		}
	} else {
		validated.date = today
	}

	if in.Note != nil {
		note := contract.TrimASCIIWhitespace(*in.Note)
		if strings.ContainsRune(note, '\x00') {
			fields = append(fields, contract.FieldIssue{
				Field:  "note",
				Reason: "must not contain NUL characters",
			})
		} else if note != "" {
			validated.note = sql.NullString{String: note, Valid: true}
		}
	}

	return validated, fields
}

func (s *Store) add(ctx context.Context, in validatedAdd) (AddResult, []contract.FieldIssue, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return AddResult{}, nil, err
	}
	defer func() { _ = tx.Rollback() }()

	var supplied *contract.Category
	if in.category != nil {
		category, err := resolveActiveCategory(ctx, tx, *in.category)
		if err != nil {
			return AddResult{}, nil, err
		}
		supplied = &category
	}

	mapping, found, err := lookupKnownMerchant(ctx, tx, in.merchant)
	if err != nil {
		return AddResult{}, nil, err
	}

	var (
		categorySource string
		mappingAction  string
		categoryID     int64
	)
	if supplied == nil {
		categorySource = CategorySourceKnownMerchant
		if !found {
			return AddResult{}, nil, &MerchantCategoryRequiredError{Merchant: in.merchant}
		}
		if !mapping.CategoryActive {
			activeCategories, err := listActiveCategories(ctx, tx)
			if err != nil {
				return AddResult{}, nil, err
			}
			return AddResult{}, nil, &MerchantCategoryInactiveError{
				KnownMerchant:    mapping,
				ActiveCategories: activeCategories,
			}
		}
		mappingAction = MappingActionMatched
		categoryID = mapping.CategoryID
	} else {
		categorySource = CategorySourceProvided
		categoryID = supplied.ID
		switch {
		case !found:
			mappingAction = MappingActionCreated
		case !mapping.CategoryActive:
			mappingAction = MappingActionReplacedInactive
		case mapping.CategoryID == supplied.ID:
			mappingAction = MappingActionMatched
		default:
			mappingAction = MappingActionPreserved
		}
	}

	result, err := tx.ExecContext(ctx, `
		INSERT INTO transactions (merchant, amount_hundredths, date, category_id, note)
		VALUES (?, ?, ?, ?, ?)
	`, in.merchant, in.amountHundredths, in.date, categoryID, in.note)
	if err != nil {
		return AddResult{}, nil, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return AddResult{}, nil, err
	}

	switch mappingAction {
	case MappingActionCreated:
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO known_merchants (merchant, category_id)
			VALUES (?, ?)
		`, in.merchant, categoryID); err != nil {
			return AddResult{}, nil, err
		}
	case MappingActionReplacedInactive:
		if _, err := tx.ExecContext(ctx, `
			UPDATE known_merchants
			SET category_id = ?,
			    updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
			WHERE id = ?
		`, categoryID, mapping.ID); err != nil {
			return AddResult{}, nil, err
		}
	}

	recorded, err := getTransactionByID(ctx, tx, id)
	if err != nil {
		return AddResult{}, nil, err
	}
	if err := tx.Commit(); err != nil {
		return AddResult{}, nil, err
	}
	return AddResult{
		Transaction:           recorded,
		CategorySource:        categorySource,
		MerchantMappingAction: mappingAction,
	}, nil, nil
}

const categoryColumns = `id, name, active, created_at, updated_at`

const knownMerchantColumns = `
	m.id,
	m.merchant,
	m.category_id,
	c.name,
	c.active,
	m.created_at,
	m.updated_at
`

func resolveActiveCategory(ctx context.Context, tx *sql.Tx, name string) (contract.Category, error) {
	category, found, err := lookupCategory(ctx, tx, name)
	if err != nil {
		return contract.Category{}, err
	}
	if !found {
		activeCategories, err := listActiveCategories(ctx, tx)
		if err != nil {
			return contract.Category{}, err
		}
		return contract.Category{}, &CategoryNotFoundError{
			Requested:        name,
			ActiveCategories: activeCategories,
		}
	}
	if !category.Active {
		activeCategories, err := listActiveCategories(ctx, tx)
		if err != nil {
			return contract.Category{}, err
		}
		return contract.Category{}, &CategoryInactiveError{
			Category:         category,
			ActiveCategories: activeCategories,
		}
	}
	return category, nil
}

func lookupCategory(ctx context.Context, tx *sql.Tx, name string) (contract.Category, bool, error) {
	category, err := scanCategory(tx.QueryRowContext(ctx, `
		SELECT `+categoryColumns+`
		FROM categories
		WHERE name = ? COLLATE NOCASE
	`, name))
	if errors.Is(err, sql.ErrNoRows) {
		return contract.Category{}, false, nil
	}
	if err != nil {
		return contract.Category{}, false, err
	}
	return category, true, nil
}

func listActiveCategories(ctx context.Context, tx *sql.Tx) ([]contract.Category, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT `+categoryColumns+`
		FROM categories
		WHERE active = 1
		ORDER BY name COLLATE NOCASE ASC, id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	categories := make([]contract.Category, 0)
	for rows.Next() {
		category, err := scanCategory(rows)
		if err != nil {
			return nil, err
		}
		categories = append(categories, category)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	return categories, nil
}

func lookupKnownMerchant(ctx context.Context, tx *sql.Tx, merchantName string) (contract.KnownMerchant, bool, error) {
	knownMerchant, err := scanKnownMerchant(tx.QueryRowContext(ctx, `
		SELECT `+knownMerchantColumns+`
		FROM known_merchants AS m
		INNER JOIN categories AS c ON c.id = m.category_id
		WHERE m.merchant = ? COLLATE NOCASE
	`, merchantName))
	if errors.Is(err, sql.ErrNoRows) {
		return contract.KnownMerchant{}, false, nil
	}
	if err != nil {
		return contract.KnownMerchant{}, false, err
	}
	return knownMerchant, true, nil
}

func getTransactionByID(ctx context.Context, tx *sql.Tx, id int64) (contract.Transaction, error) {
	var recorded contract.Transaction
	var hundredths int64
	var note sql.NullString
	if err := tx.QueryRowContext(ctx, `
		SELECT
			t.id,
			t.amount_hundredths,
			t.merchant,
			t.date,
			t.category_id,
			c.name,
			t.note,
			t.created_at,
			t.updated_at
		FROM transactions AS t
		INNER JOIN categories AS c ON c.id = t.category_id
		WHERE t.id = ?
	`, id).Scan(
		&recorded.ID,
		&hundredths,
		&recorded.Merchant,
		&recorded.Date,
		&recorded.CategoryID,
		&recorded.Category,
		&note,
		&recorded.CreatedAt,
		&recorded.UpdatedAt,
	); err != nil {
		return contract.Transaction{}, err
	}
	formatted, err := contract.FormatAmount(hundredths)
	if err != nil {
		return contract.Transaction{}, err
	}
	recorded.Amount = formatted
	if note.Valid {
		recorded.Note = &note.String
	}
	return recorded, nil
}

func scanCategory(row interface{ Scan(dest ...any) error }) (contract.Category, error) {
	var category contract.Category
	err := row.Scan(
		&category.ID,
		&category.Name,
		&category.Active,
		&category.CreatedAt,
		&category.UpdatedAt,
	)
	return category, err
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
