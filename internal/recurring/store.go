// Package recurring implements domain rules and storage for recurring expense templates.
package recurring

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jordanp2002/Local-Ledger/internal/contract"
)

var (
	ErrNotFound                  = errors.New("recurring transaction not found")
	ErrCategoryNotFound          = errors.New("category not found")
	ErrCategoryInactive          = errors.New("category inactive")
	ErrRecurringCategoryInactive = errors.New("recurring category inactive")
)

type queryer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// Store owns recurring expense template validation and persistence.
type Store struct {
	DB *sql.DB
	// Now supplies the current local time.
	Now func() time.Time
}

// Service is the recurring-template behavior consumed by the MCP adapter.
type Service interface {
	Create(context.Context, CreateInput) (CreateResult, []contract.FieldIssue, error)
	List(context.Context) ([]contract.RecurringTransaction, error)
	Disable(context.Context, int64) (DisableResult, []contract.FieldIssue, error)
	Update(context.Context, UpdateInput) (UpdateResult, []contract.FieldIssue, error)
	Enable(context.Context, int64) (EnableResult, []contract.FieldIssue, error)
	PreviewDue(context.Context) (PreviewDueResult, error)
	PreviewUpcoming(context.Context) (PreviewUpcomingResult, error)
	MaterializeDue(context.Context) (MaterializeDueResult, error)
}

func (s *Store) timestamp() (string, error) {
	if s == nil || s.Now == nil {
		return "", errors.New("recurring store clock is nil")
	}
	return s.Now().UTC().Format("2006-01-02T15:04:05.000Z"), nil
}

// CreateInput defines inputs for creating a recurring transaction template.
type CreateInput struct {
	Merchant   string
	Amount     string
	Category   string
	DayOfMonth int64
	Note       *string
}

// CreateResult contains the canonical created template.
type CreateResult struct {
	RecurringTransaction contract.RecurringTransaction
}

// DisableResult contains the canonical updated template and change status.
type DisableResult struct {
	RecurringTransaction contract.RecurringTransaction
	Changed              bool
}

type PreviewDueResult struct {
	AsOfDate        string
	Month           string
	TotalAmount     string
	DueTransactions []contract.DueTransaction
	Blocked         []contract.BlockedDueTransaction
}

type MaterializeDueResult struct {
	AsOfDate       string
	Month          string
	Created        int64
	TotalAmount    string
	Transactions   []contract.Transaction
	RolloverOffers []contract.RolloverOffer
}

type RecurringCategoryInactiveError struct {
	RecurringTransactionID int64
	Merchant               string
	Category               string
	DueDate                string
}

func (e *RecurringCategoryInactiveError) Error() string {
	if e == nil {
		return ErrRecurringCategoryInactive.Error()
	}
	return fmt.Sprintf("recurring transaction %d for %q references inactive category %q", e.RecurringTransactionID, e.Merchant, e.Category)
}

func (e *RecurringCategoryInactiveError) Is(target error) bool {
	return target == ErrRecurringCategoryInactive
}

// NotFoundError identifies a missing recurring transaction ID.
type NotFoundError struct {
	ID int64
}

func (e *NotFoundError) Error() string {
	if e == nil {
		return ErrNotFound.Error()
	}
	return fmt.Sprintf("recurring transaction %d was not found", e.ID)
}

func (e *NotFoundError) Is(target error) bool {
	return target == ErrNotFound
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

type validatedCreate struct {
	merchant         string
	amountHundredths int64
	category         string
	dayOfMonth       int64
	note             sql.NullString
}

const recurringColumns = `
	r.id,
	r.merchant,
	r.amount_hundredths,
	r.category_id,
	c.name,
	c.active,
	r.day_of_month,
	r.note,
	r.active,
	r.created_at,
	r.updated_at
`

const categoryColumns = `id, name, active, created_at, updated_at`

// Create validates and saves one recurring transaction template without writing transactions or mappings.
func (s *Store) Create(ctx context.Context, in CreateInput) (CreateResult, []contract.FieldIssue, error) {
	validated, issues := validateCreate(in)
	if len(issues) > 0 {
		return CreateResult{}, issues, nil
	}

	if s == nil || s.DB == nil {
		return CreateResult{}, nil, errors.New("recurring store database is nil")
	}
	timestamp, err := s.timestamp()
	if err != nil {
		return CreateResult{}, nil, err
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return CreateResult{}, nil, err
	}
	defer func() { _ = tx.Rollback() }()

	cat, err := resolveActiveCategory(ctx, tx, validated.category)
	if err != nil {
		return CreateResult{}, nil, err
	}

	res, err := tx.ExecContext(ctx, `
		INSERT INTO recurring_transactions (
			merchant, amount_hundredths, category_id, day_of_month, note, active, created_at, updated_at
		)
		VALUES (?, ?, ?, ?, ?, 1, ?, ?)
	`, validated.merchant, validated.amountHundredths, cat.ID, validated.dayOfMonth, validated.note, timestamp, timestamp)
	if err != nil {
		return CreateResult{}, nil, err
	}

	id, err := res.LastInsertId()
	if err != nil {
		return CreateResult{}, nil, err
	}

	created, err := getRecurringByID(ctx, tx, id)
	if err != nil {
		return CreateResult{}, nil, err
	}

	if err := tx.Commit(); err != nil {
		return CreateResult{}, nil, err
	}

	return CreateResult{
		RecurringTransaction: created,
	}, nil, nil
}

// List returns all recurring transaction templates ordered by status, day of month, merchant, and ID.
func (s *Store) List(ctx context.Context) ([]contract.RecurringTransaction, error) {
	if s == nil || s.DB == nil {
		return nil, errors.New("recurring store database is nil")
	}

	rows, err := s.DB.QueryContext(ctx, `
		SELECT `+recurringColumns+`
		FROM recurring_transactions r
		INNER JOIN categories c ON c.id = r.category_id
		ORDER BY r.active DESC, r.day_of_month ASC, r.merchant COLLATE NOCASE ASC, r.id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]contract.RecurringTransaction, 0)
	for rows.Next() {
		item, err := scanRecurringTransaction(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

// Disable deactivates a recurring expense template by ID.
func (s *Store) Disable(ctx context.Context, id int64) (DisableResult, []contract.FieldIssue, error) {
	if issue := validateID(id); issue != nil {
		return DisableResult{}, []contract.FieldIssue{*issue}, nil
	}

	if s == nil || s.DB == nil {
		return DisableResult{}, nil, errors.New("recurring store database is nil")
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return DisableResult{}, nil, err
	}
	defer func() { _ = tx.Rollback() }()

	existing, err := getRecurringByID(ctx, tx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return DisableResult{}, nil, &NotFoundError{ID: id}
	}
	if err != nil {
		return DisableResult{}, nil, err
	}

	if !existing.Active {
		return DisableResult{
			RecurringTransaction: existing,
			Changed:              false,
		}, nil, nil
	}
	timestamp, err := s.timestamp()
	if err != nil {
		return DisableResult{}, nil, err
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE recurring_transactions
		SET active = 0,
		    updated_at = ?
		WHERE id = ?
	`, timestamp, id); err != nil {
		return DisableResult{}, nil, err
	}

	updated, err := getRecurringByID(ctx, tx, id)
	if err != nil {
		return DisableResult{}, nil, err
	}

	if err := tx.Commit(); err != nil {
		return DisableResult{}, nil, err
	}

	return DisableResult{
		RecurringTransaction: updated,
		Changed:              true,
	}, nil, nil
}

func validateCreate(in CreateInput) (validatedCreate, []contract.FieldIssue) {
	issues := make([]contract.FieldIssue, 0)
	validated := validatedCreate{}

	if merchant, issue := validateMerchant(in.Merchant); issue != nil {
		issues = append(issues, *issue)
	} else {
		validated.merchant = merchant
	}

	if amount, issue := validateAmount(in.Amount); issue != nil {
		issues = append(issues, *issue)
	} else {
		validated.amountHundredths = amount
	}

	if category, issue := validateCategoryName(in.Category); issue != nil {
		issues = append(issues, *issue)
	} else {
		validated.category = category
	}

	if day, issue := validateDayOfMonth(in.DayOfMonth); issue != nil {
		issues = append(issues, *issue)
	} else {
		validated.dayOfMonth = day
	}

	if note, issue := validateNote(in.Note); issue != nil {
		issues = append(issues, *issue)
	} else {
		validated.note = note
	}

	return validated, issues
}

func validateMerchant(value string) (string, *contract.FieldIssue) {
	merchant := contract.TrimASCIIWhitespace(value)
	switch {
	case merchant == "":
		return "", &contract.FieldIssue{
			Field:  "merchant",
			Reason: "must not be empty",
		}
	case strings.ContainsRune(merchant, '\x00'):
		return "", &contract.FieldIssue{
			Field:  "merchant",
			Reason: "must not contain NUL characters",
		}
	default:
		return merchant, nil
	}
}

func validateAmount(value string) (int64, *contract.FieldIssue) {
	amount, err := contract.ParseAmount(value)
	if err != nil {
		return 0, &contract.FieldIssue{
			Field:  "amount",
			Reason: "must be a positive amount with at most two decimal places",
		}
	}
	if amount == 0 {
		return 0, &contract.FieldIssue{
			Field:  "amount",
			Reason: "must be greater than zero",
		}
	}
	return amount, nil
}

func validateCategoryName(value string) (string, *contract.FieldIssue) {
	category := contract.TrimASCIIWhitespace(value)
	switch {
	case category == "":
		return "", &contract.FieldIssue{
			Field:  "category",
			Reason: "must not be empty",
		}
	case strings.ContainsRune(category, '\x00'):
		return "", &contract.FieldIssue{
			Field:  "category",
			Reason: "must not contain NUL characters",
		}
	default:
		return category, nil
	}
}

func validateDayOfMonth(value int64) (int64, *contract.FieldIssue) {
	if value < 1 || value > 31 {
		return 0, &contract.FieldIssue{
			Field:  "day_of_month",
			Reason: "must be an integer between 1 and 31",
		}
	}
	return value, nil
}

func validateNote(value *string) (sql.NullString, *contract.FieldIssue) {
	if value == nil {
		return sql.NullString{}, nil
	}
	note := contract.TrimASCIIWhitespace(*value)
	if strings.ContainsRune(note, '\x00') {
		return sql.NullString{}, &contract.FieldIssue{
			Field:  "note",
			Reason: "must not contain NUL characters",
		}
	}
	if note == "" {
		return sql.NullString{}, nil
	}
	return sql.NullString{String: note, Valid: true}, nil
}

func validateID(id int64) *contract.FieldIssue {
	if id <= 0 {
		return &contract.FieldIssue{
			Field:  "id",
			Reason: "must be a positive integer",
		}
	}
	return nil
}

func getRecurringByID(ctx context.Context, q queryer, id int64) (contract.RecurringTransaction, error) {
	return scanRecurringTransaction(q.QueryRowContext(ctx, `
		SELECT `+recurringColumns+`
		FROM recurring_transactions r
		INNER JOIN categories c ON c.id = r.category_id
		WHERE r.id = ?
	`, id))
}

func scanRecurringTransaction(row interface{ Scan(dest ...any) error }) (contract.RecurringTransaction, error) {
	var (
		r                contract.RecurringTransaction
		amountHundredths int64
		note             sql.NullString
		categoryActive   int64
		active           int64
	)
	err := row.Scan(
		&r.ID,
		&r.Merchant,
		&amountHundredths,
		&r.CategoryID,
		&r.Category,
		&categoryActive,
		&r.DayOfMonth,
		&note,
		&active,
		&r.CreatedAt,
		&r.UpdatedAt,
	)
	if err != nil {
		return contract.RecurringTransaction{}, err
	}
	formatted, err := contract.FormatAmount(amountHundredths)
	if err != nil {
		return contract.RecurringTransaction{}, err
	}
	r.Amount = formatted
	r.CategoryActive = categoryActive == 1
	r.Active = active == 1
	if note.Valid {
		r.Note = &note.String
	} else {
		r.Note = nil
	}
	return r, nil
}

func resolveActiveCategory(ctx context.Context, q queryer, name string) (contract.Category, error) {
	category, found, err := lookupCategory(ctx, q, name)
	if err != nil {
		return contract.Category{}, err
	}
	if !found {
		activeCategories, err := listActiveCategories(ctx, q)
		if err != nil {
			return contract.Category{}, err
		}
		return contract.Category{}, &CategoryNotFoundError{
			Requested:        name,
			ActiveCategories: activeCategories,
		}
	}
	if !category.Active {
		activeCategories, err := listActiveCategories(ctx, q)
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

func lookupCategory(ctx context.Context, q queryer, name string) (contract.Category, bool, error) {
	category, err := scanCategory(q.QueryRowContext(ctx, `
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

func listActiveCategories(ctx context.Context, q queryer) ([]contract.Category, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT `+categoryColumns+`
		FROM categories
		WHERE active = 1
		ORDER BY name COLLATE NOCASE ASC, id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

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
	return categories, nil
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
