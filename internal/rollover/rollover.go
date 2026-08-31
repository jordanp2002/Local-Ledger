// Package rollover implements explicit one-month budget adjustments.
package rollover

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/jordanp2002/local-finance-mcp/internal/contract"
	"github.com/jordanp2002/local-finance-mcp/internal/sinkingfund"
)

const (
	StatusPending       = "pending"
	StatusApplied       = "applied"
	DefaultLimit  int64 = 50
	MaxLimit      int64 = 200
)

var (
	ErrNotEligible         = errors.New("budget rollover not eligible")
	ErrNotFound            = errors.New("budget rollover not found")
	ErrDependencyConflict  = errors.New("budget rollover dependency conflict")
	ErrCategoryNotFound    = errors.New("category not found")
	ErrCategoryInactive    = errors.New("category inactive")
	ErrTransactionNotFound = errors.New("source transaction not found")
)

// Key identifies a source-month/category accounting bucket.
type Key struct {
	SourceMonth string
	CategoryID  int64
}

// Eligibility is the complete accounting state for one source-month/category.
type Eligibility struct {
	Key                Key
	Category           string
	HasBudget          bool
	BaseBudget         int64
	IncomingAdjustment int64
	AvailableBudget    int64
	Spending           int64
	SourceOverspending int64
	OutgoingRollover   int64
	EligibleRollover   int64
}

// OfferChange supplies the pre-mutation eligible amount used to decide whether
// a transaction write increased uncovered overspending.
type OfferChange struct {
	Key                 Key
	PreviousEligible    int64
	SourceTransactionID *int64
}

// OfferChangesForTransaction converts a recorded transaction into the source
// buckets that may have changed after its write.
func OfferChangesForTransaction(recorded contract.Transaction) []OfferChange {
	changes := make([]OfferChange, 0, len(recorded.Allocations))
	id := recorded.ID
	for _, allocation := range recorded.Allocations {
		changes = append(changes, OfferChange{
			Key: Key{
				SourceMonth: recorded.Date[:7],
				CategoryID:  allocation.CategoryID,
			},
			SourceTransactionID: &id,
		})
	}
	return changes
}

// Conflict describes one outgoing rollover set that would exceed real
// overspending after a mutation.
type Conflict struct {
	SourceMonth        string
	CategoryID         int64
	RolloverIDs        []int64
	SourceOverspending int64
	OutgoingRollover   int64
	EligibleRollover   int64
}

// DependencyConflictError identifies every outgoing rollover that must be
// reduced or removed before a mutation can proceed.
type DependencyConflictError struct {
	Conflicts   []Conflict
	RolloverIDs []int64
}

func (e *DependencyConflictError) Error() string {
	if e == nil {
		return ErrDependencyConflict.Error()
	}
	return fmt.Sprintf("outgoing rollovers %v exceed eligible overspending", e.RolloverIDs)
}

func (e *DependencyConflictError) Is(target error) bool {
	return target == ErrDependencyConflict
}

// NotEligibleError contains the accounting values needed to explain why a
// requested amount cannot be recorded.
type NotEligibleError struct {
	SourceMonth        string
	TargetMonth        string
	Category           contract.Category
	RequestedAmount    int64
	SourceOverspending int64
	AlreadyRolled      int64
	EligibleRollover   int64
	Reason             string
}

func (e *NotEligibleError) Error() string {
	if e == nil {
		return ErrNotEligible.Error()
	}
	if e.Reason != "" {
		return e.Reason
	}
	return fmt.Sprintf("%s has only %d hundredths eligible for rollover", e.SourceMonth, e.EligibleRollover)
}

func (e *NotEligibleError) Is(target error) bool {
	return target == ErrNotEligible
}

// NotFoundError identifies one removed or missing rollover.
type NotFoundError struct {
	ID int64
}

func (e *NotFoundError) Error() string {
	if e == nil {
		return ErrNotFound.Error()
	}
	return fmt.Sprintf("budget rollover %d was not found", e.ID)
}

func (e *NotFoundError) Is(target error) bool {
	return target == ErrNotFound
}

// CategoryNotFoundError identifies a missing category name.
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

// CategoryInactiveError identifies a category that cannot receive a new
// rollover. Historical rows may still be listed after a category is disabled.
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

// SourceTransactionNotFoundError identifies a missing optional audit link.
type SourceTransactionNotFoundError struct {
	ID int64
}

func (e *SourceTransactionNotFoundError) Error() string {
	if e == nil {
		return ErrTransactionNotFound.Error()
	}
	return fmt.Sprintf("source transaction %d was not found", e.ID)
}

func (e *SourceTransactionNotFoundError) Is(target error) bool {
	return target == ErrTransactionNotFound
}

// CreateInput is the domain form of create_budget_rollover. TargetMonth is
// intentionally absent: it is always derived from SourceMonth.
type CreateInput struct {
	SourceMonth         string
	Category            string
	Amount              string
	SourceTransactionID *int64
	Note                *string
}

type CreateResult struct {
	Rollover contract.BudgetRollover
}

type ListInput struct {
	SourceMonth *string
	TargetMonth *string
	Category    *string
	Limit       *int64
	Offset      *int64
}

type ListResult struct {
	Rollovers []contract.BudgetRollover
	Page      contract.Page
}

// Store owns rollover validation and persistence.
type Store struct {
	DB  *sql.DB
	Now func() time.Time
}

type validatedCreate struct {
	sourceMonth         string
	targetMonth         string
	category            string
	amount              int64
	sourceTransactionID *int64
	note                *string
}

type validatedList struct {
	sourceMonth *string
	targetMonth *string
	category    *string
	limit       int64
	offset      int64
}

type storedRollover struct {
	row    contract.BudgetRollover
	amount int64
}

const rolloverColumns = `
	r.id,
	r.category_id,
	c.name,
	r.source_month,
	r.target_month,
	r.amount_hundredths,
	r.source_transaction_id,
	r.note,
	r.created_at,
	r.updated_at
`

// Create validates and records one explicit adjustment in one SQLite write
// transaction.
func (s *Store) Create(ctx context.Context, in CreateInput) (CreateResult, []contract.FieldIssue, error) {
	now := s.now()
	validated, fields := validateCreate(in, now)
	if len(fields) != 0 {
		return CreateResult{}, fields, nil
	}
	if s == nil || s.DB == nil {
		return CreateResult{}, nil, errors.New("rollover store database is nil")
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return CreateResult{}, nil, err
	}
	defer func() { _ = tx.Rollback() }()

	category, err := resolveCategory(ctx, tx, validated.category)
	if err != nil {
		return CreateResult{}, nil, err
	}
	if !category.Active {
		active, activeErr := listActiveCategories(ctx, tx)
		if activeErr != nil {
			return CreateResult{}, nil, activeErr
		}
		return CreateResult{}, nil, &CategoryInactiveError{Category: category, ActiveCategories: active}
	}
	if periods, err := sinkingfund.OverlappingMonths(ctx, tx, category.ID, validated.sourceMonth, validated.targetMonth); err != nil {
		return CreateResult{}, nil, err
	} else if len(periods) != 0 {
		return CreateResult{}, nil, &sinkingfund.RolloverConflictError{CategoryID: category.ID, Category: category.Name, Months: periods}
	}

	if validated.sourceTransactionID != nil {
		if err := validateSourceTransaction(ctx, tx, *validated.sourceTransactionID, validated.sourceMonth, category.ID); err != nil {
			return CreateResult{}, nil, err
		}
	}

	eligibility, err := Evaluate(ctx, tx, Key{SourceMonth: validated.sourceMonth, CategoryID: category.ID})
	if err != nil {
		return CreateResult{}, nil, err
	}
	if !eligibility.HasBudget {
		return CreateResult{}, nil, newNotEligible(validated, category, eligibility, "the source month must have a budget row for this category")
	}
	if validated.amount > eligibility.EligibleRollover {
		return CreateResult{}, nil, newNotEligible(validated, category, eligibility, "the requested amount exceeds eligible overspending")
	}

	timestamp := formatTimestamp(now)
	result, err := tx.ExecContext(ctx, `
		INSERT INTO budget_rollovers (
			category_id, source_month, target_month, amount_hundredths,
			source_transaction_id, note, created_at, updated_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, category.ID, validated.sourceMonth, validated.targetMonth, validated.amount,
		validated.sourceTransactionID, validated.note, timestamp, timestamp)
	if err != nil {
		return CreateResult{}, nil, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return CreateResult{}, nil, err
	}

	// Re-check the invariant after the insert while still owning the write
	// transaction. This closes the window between the eligibility read and the
	// new outgoing row for callers that share a database across connections.
	if err := ValidateOutgoing(ctx, tx); err != nil {
		return CreateResult{}, nil, err
	}

	created, err := loadRollover(ctx, tx, id)
	if err != nil {
		return CreateResult{}, nil, err
	}
	created.row.Status, err = statusForTarget(ctx, tx, created.row.TargetMonth)
	if err != nil {
		return CreateResult{}, nil, err
	}
	if err := tx.Commit(); err != nil {
		return CreateResult{}, nil, err
	}
	return CreateResult{Rollover: created.row}, nil, nil
}

func newNotEligible(in validatedCreate, category contract.Category, eligibility Eligibility, reason string) *NotEligibleError {
	return &NotEligibleError{
		SourceMonth:        in.sourceMonth,
		TargetMonth:        in.targetMonth,
		Category:           category,
		RequestedAmount:    in.amount,
		SourceOverspending: eligibility.SourceOverspending,
		AlreadyRolled:      eligibility.OutgoingRollover,
		EligibleRollover:   eligibility.EligibleRollover,
		Reason:             reason,
	}
}

// List returns explicit adjustments with derived pending/applied status.
func (s *Store) List(ctx context.Context, in ListInput) (ListResult, []contract.FieldIssue, error) {
	validated, fields := validateList(in)
	if len(fields) != 0 {
		return ListResult{}, fields, nil
	}
	if s == nil || s.DB == nil {
		return ListResult{}, nil, errors.New("rollover store database is nil")
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return ListResult{}, nil, err
	}
	defer func() { _ = tx.Rollback() }()

	var categoryID *int64
	if validated.category != nil {
		category, found, err := lookupCategory(ctx, tx, *validated.category)
		if err != nil {
			return ListResult{}, nil, err
		}
		if !found {
			active, activeErr := listActiveCategories(ctx, tx)
			if activeErr != nil {
				return ListResult{}, nil, activeErr
			}
			return ListResult{}, nil, &CategoryNotFoundError{Requested: *validated.category, ActiveCategories: active}
		}
		categoryID = &category.ID
	}

	where, args := listFilter(validated.sourceMonth, validated.targetMonth, categoryID)
	var total int64
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM budget_rollovers AS r`+where, args...).Scan(&total); err != nil {
		return ListResult{}, nil, err
	}

	queryArgs := append(append([]any(nil), args...), validated.limit, validated.offset)
	rows, err := tx.QueryContext(ctx, `
		SELECT `+rolloverColumns+`
		FROM budget_rollovers AS r
		INNER JOIN categories AS c ON c.id = r.category_id`+where+`
		ORDER BY r.target_month ASC, c.name COLLATE NOCASE ASC, r.source_month ASC, r.id ASC
		LIMIT ? OFFSET ?
	`, queryArgs...)
	if err != nil {
		return ListResult{}, nil, err
	}
	defer func() { _ = rows.Close() }()

	rollovers := make([]contract.BudgetRollover, 0)
	for rows.Next() {
		stored, err := scanRollover(rows)
		if err != nil {
			return ListResult{}, nil, err
		}
		stored.row.Status, err = statusForTarget(ctx, tx, stored.row.TargetMonth)
		if err != nil {
			return ListResult{}, nil, err
		}
		rollovers = append(rollovers, stored.row)
	}
	if err := rows.Err(); err != nil {
		return ListResult{}, nil, err
	}
	if err := rows.Close(); err != nil {
		return ListResult{}, nil, err
	}

	result := ListResult{
		Rollovers: rollovers,
		Page: contract.Page{
			Limit:    validated.limit,
			Offset:   validated.offset,
			Returned: int64(len(rollovers)),
			Total:    total,
			HasMore:  validated.offset < total && int64(len(rollovers)) < total-validated.offset,
		},
	}
	if err := tx.Commit(); err != nil {
		return ListResult{}, nil, err
	}
	return result, nil, nil
}

// Remove explicitly deletes one adjustment after checking dependent outgoing
// rollovers in the same transaction.
func (s *Store) Remove(ctx context.Context, id int64) (contract.BudgetRollover, []contract.FieldIssue, error) {
	if id < 1 {
		return contract.BudgetRollover{}, []contract.FieldIssue{{Field: "id", Reason: "must be a positive integer"}}, nil
	}
	if s == nil || s.DB == nil {
		return contract.BudgetRollover{}, nil, errors.New("rollover store database is nil")
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return contract.BudgetRollover{}, nil, err
	}
	defer func() { _ = tx.Rollback() }()

	removed, err := loadRollover(ctx, tx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return contract.BudgetRollover{}, nil, &NotFoundError{ID: id}
	}
	if err != nil {
		return contract.BudgetRollover{}, nil, err
	}
	removed.row.Status, err = statusForTarget(ctx, tx, removed.row.TargetMonth)
	if err != nil {
		return contract.BudgetRollover{}, nil, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM budget_rollovers WHERE id = ?`, id); err != nil {
		return contract.BudgetRollover{}, nil, err
	}
	if err := ValidateOutgoing(ctx, tx); err != nil {
		return contract.BudgetRollover{}, nil, err
	}
	if err := tx.Commit(); err != nil {
		return contract.BudgetRollover{}, nil, err
	}
	return removed.row, nil, nil
}

func validateCreate(in CreateInput, now time.Time) (validatedCreate, []contract.FieldIssue) {
	fields := make([]contract.FieldIssue, 0, 5)
	validated := validatedCreate{}
	month, err := contract.ParseMonth(in.SourceMonth)
	if err != nil {
		fields = append(fields, contract.FieldIssue{Field: "source_month", Reason: "must be a valid YYYY-MM month"})
	} else {
		validated.sourceMonth = month
		if month > now.Format("2006-01") {
			fields = append(fields, contract.FieldIssue{Field: "source_month", Reason: "must not be in the future"})
		}
		target, targetErr := contract.NextMonth(month)
		if targetErr != nil {
			fields = append(fields, contract.FieldIssue{Field: "source_month", Reason: "must be a valid YYYY-MM month"})
		} else {
			validated.targetMonth = target
		}
	}

	category := contract.TrimASCIIWhitespace(in.Category)
	if category == "" {
		fields = append(fields, contract.FieldIssue{Field: "category", Reason: "must not be empty"})
	} else if strings.ContainsRune(category, '\x00') {
		fields = append(fields, contract.FieldIssue{Field: "category", Reason: "must not contain NUL characters"})
	} else {
		validated.category = category
	}

	amount, amountErr := contract.ParseAmount(in.Amount)
	if amountErr != nil || amount <= 0 {
		fields = append(fields, contract.FieldIssue{Field: "amount", Reason: "must be a positive amount with at most two decimal places"})
	} else {
		validated.amount = amount
	}

	if in.SourceTransactionID != nil {
		if *in.SourceTransactionID < 1 {
			fields = append(fields, contract.FieldIssue{Field: "source_transaction_id", Reason: "must be a positive integer"})
		} else {
			id := *in.SourceTransactionID
			validated.sourceTransactionID = &id
		}
	}

	if in.Note != nil {
		note := contract.TrimASCIIWhitespace(*in.Note)
		if strings.ContainsRune(note, '\x00') {
			fields = append(fields, contract.FieldIssue{Field: "note", Reason: "must not contain NUL characters"})
		} else if note != "" {
			validated.note = &note
		}
	}
	return validated, fields
}

func validateList(in ListInput) (validatedList, []contract.FieldIssue) {
	fields := make([]contract.FieldIssue, 0, 5)
	validated := validatedList{limit: DefaultLimit}
	if in.SourceMonth != nil {
		month, err := contract.ParseMonth(*in.SourceMonth)
		if err != nil {
			fields = append(fields, contract.FieldIssue{Field: "source_month", Reason: "must be a valid YYYY-MM month"})
		} else {
			validated.sourceMonth = &month
		}
	}
	if in.TargetMonth != nil {
		month, err := contract.ParseMonth(*in.TargetMonth)
		if err != nil {
			fields = append(fields, contract.FieldIssue{Field: "target_month", Reason: "must be a valid YYYY-MM month"})
		} else {
			validated.targetMonth = &month
		}
	}
	if in.Category != nil {
		category := contract.TrimASCIIWhitespace(*in.Category)
		if category == "" {
			fields = append(fields, contract.FieldIssue{Field: "category", Reason: "must not be empty"})
		} else if strings.ContainsRune(category, '\x00') {
			fields = append(fields, contract.FieldIssue{Field: "category", Reason: "must not contain NUL characters"})
		} else {
			validated.category = &category
		}
	}
	if in.Limit != nil {
		validated.limit = *in.Limit
		if validated.limit < 1 || validated.limit > MaxLimit {
			fields = append(fields, contract.FieldIssue{Field: "limit", Reason: "must be between 1 and 200"})
		}
	}
	if in.Offset != nil {
		validated.offset = *in.Offset
		if validated.offset < 0 {
			fields = append(fields, contract.FieldIssue{Field: "offset", Reason: "must be zero or greater"})
		}
	}
	return validated, fields
}

func (s *Store) now() time.Time {
	if s != nil && s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func formatTimestamp(now time.Time) string {
	return now.UTC().Format("2006-01-02T15:04:05.000Z")
}

func listFilter(sourceMonth, targetMonth *string, categoryID *int64) (string, []any) {
	clauses := make([]string, 0, 3)
	args := make([]any, 0, 3)
	if sourceMonth != nil {
		clauses = append(clauses, "r.source_month = ?")
		args = append(args, *sourceMonth)
	}
	if targetMonth != nil {
		clauses = append(clauses, "r.target_month = ?")
		args = append(args, *targetMonth)
	}
	if categoryID != nil {
		clauses = append(clauses, "r.category_id = ?")
		args = append(args, *categoryID)
	}
	if len(clauses) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

func loadRollover(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, id int64) (storedRollover, error) {
	return scanRollover(q.QueryRowContext(ctx, `
		SELECT `+rolloverColumns+`
		FROM budget_rollovers AS r
		INNER JOIN categories AS c ON c.id = r.category_id
		WHERE r.id = ?
	`, id))
}

func scanRollover(row interface{ Scan(dest ...any) error }) (storedRollover, error) {
	var (
		stored              storedRollover
		amount              int64
		sourceTransactionID sql.NullInt64
		note                sql.NullString
	)
	if err := row.Scan(
		&stored.row.ID,
		&stored.row.CategoryID,
		&stored.row.Category,
		&stored.row.SourceMonth,
		&stored.row.TargetMonth,
		&amount,
		&sourceTransactionID,
		&note,
		&stored.row.CreatedAt,
		&stored.row.UpdatedAt,
	); err != nil {
		return storedRollover{}, err
	}
	formatted, err := contract.FormatAmount(amount)
	if err != nil {
		return storedRollover{}, err
	}
	stored.amount = amount
	stored.row.Amount = formatted
	if sourceTransactionID.Valid {
		id := sourceTransactionID.Int64
		stored.row.SourceTransactionID = &id
	}
	if note.Valid {
		stored.row.Note = &note.String
	}
	return stored, nil
}

func statusForTarget(ctx context.Context, tx *sql.Tx, month string) (string, error) {
	var marker int64
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM budgets WHERE month = ? LIMIT 1`, month).Scan(&marker)
	if errors.Is(err, sql.ErrNoRows) {
		return StatusPending, nil
	}
	if err != nil {
		return "", err
	}
	return StatusApplied, nil
}

func validateSourceTransaction(ctx context.Context, tx *sql.Tx, id int64, sourceMonth string, categoryID int64) error {
	var date string
	if err := tx.QueryRowContext(ctx, `SELECT date FROM transactions WHERE id = ?`, id).Scan(&date); errors.Is(err, sql.ErrNoRows) {
		return &SourceTransactionNotFoundError{ID: id}
	} else if err != nil {
		return err
	} else if len(date) < 7 || date[:7] != sourceMonth {
		return &NotEligibleError{SourceMonth: sourceMonth, TargetMonth: mustNextMonth(sourceMonth), RequestedAmount: 0, Reason: "source transaction must fall within source_month"}
	}
	var marker int64
	err := tx.QueryRowContext(ctx, `
		SELECT 1 FROM transaction_allocations
		WHERE transaction_id = ? AND category_id = ?
	`, id, categoryID).Scan(&marker)
	if errors.Is(err, sql.ErrNoRows) {
		return &NotEligibleError{SourceMonth: sourceMonth, TargetMonth: mustNextMonth(sourceMonth), RequestedAmount: 0, Reason: "source transaction must contain an allocation for category"}
	}
	return err
}

func mustNextMonth(month string) string {
	target, _ := contract.NextMonth(month)
	return target
}

func resolveCategory(ctx context.Context, tx *sql.Tx, name string) (contract.Category, error) {
	category, found, err := lookupCategory(ctx, tx, name)
	if err != nil {
		return contract.Category{}, err
	}
	if !found {
		active, err := listActiveCategories(ctx, tx)
		if err != nil {
			return contract.Category{}, err
		}
		return contract.Category{}, &CategoryNotFoundError{Requested: name, ActiveCategories: active}
	}
	return category, nil
}

const categoryColumns = `id, name, active, created_at, updated_at`

func lookupCategory(ctx context.Context, tx *sql.Tx, name string) (contract.Category, bool, error) {
	var category contract.Category
	err := tx.QueryRowContext(ctx, `SELECT `+categoryColumns+` FROM categories WHERE name = ? COLLATE NOCASE`, name).Scan(
		&category.ID, &category.Name, &category.Active, &category.CreatedAt, &category.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return contract.Category{}, false, nil
	}
	if err != nil {
		return contract.Category{}, false, err
	}
	return category, true, nil
}

func listActiveCategories(ctx context.Context, tx *sql.Tx) ([]contract.Category, error) {
	return listCategories(ctx, tx, true)
}

func listCategories(ctx context.Context, tx *sql.Tx, activeOnly bool) ([]contract.Category, error) {
	query := `SELECT ` + categoryColumns + ` FROM categories`
	if activeOnly {
		query += ` WHERE active = 1`
	}
	query += ` ORDER BY name COLLATE NOCASE ASC, id ASC`
	rows, err := tx.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	categories := make([]contract.Category, 0)
	for rows.Next() {
		var category contract.Category
		if err := rows.Scan(&category.ID, &category.Name, &category.Active, &category.CreatedAt, &category.UpdatedAt); err != nil {
			return nil, err
		}
		categories = append(categories, category)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return categories, rows.Close()
}

// Evaluate calculates one bucket from base budgets, incoming adjustments,
// spending, and outgoing rollovers. It never mutates the database.
func Evaluate(ctx context.Context, tx *sql.Tx, key Key) (Eligibility, error) {
	if tx == nil {
		return Eligibility{}, errors.New("rollover SQL transaction is nil")
	}
	if _, err := contract.ParseMonth(key.SourceMonth); err != nil {
		return Eligibility{}, err
	}
	eligibility := Eligibility{Key: key}
	if err := tx.QueryRowContext(ctx, `
		SELECT b.amount_hundredths, c.name
		FROM budgets AS b
		INNER JOIN categories AS c ON c.id = b.category_id
		WHERE b.month = ? AND b.category_id = ?
	`, key.SourceMonth, key.CategoryID).Scan(&eligibility.BaseBudget, &eligibility.Category); errors.Is(err, sql.ErrNoRows) {
		return eligibility, nil
	} else if err != nil {
		return Eligibility{}, err
	}
	eligibility.HasBudget = true

	incoming, err := sumPositive(ctx, tx, `SELECT amount_hundredths FROM budget_rollovers WHERE target_month = ? AND category_id = ?`, key.SourceMonth, key.CategoryID, "incoming rollover")
	if err != nil {
		return Eligibility{}, err
	}
	eligibility.IncomingAdjustment = -incoming
	var ok bool
	eligibility.AvailableBudget, ok = checkedSignedAdd(eligibility.BaseBudget, eligibility.IncomingAdjustment)
	if !ok {
		return Eligibility{}, errors.New("available budget overflow")
	}

	start, end, err := contract.MonthDateRange(key.SourceMonth)
	if err != nil {
		return Eligibility{}, err
	}
	eligibility.Spending, err = sumPositive(ctx, tx, `
		SELECT a.amount_hundredths
		FROM transaction_allocations AS a
		INNER JOIN transactions AS t ON t.id = a.transaction_id
		WHERE a.category_id = ? AND t.date >= ? AND t.date <= ?
	`, key.CategoryID, start, end, "category spending")
	if err != nil {
		return Eligibility{}, err
	}
	overage, ok := checkedSignedSub(eligibility.Spending, eligibility.AvailableBudget)
	if !ok {
		return Eligibility{}, errors.New("source overspending overflow")
	}
	if overage > 0 {
		eligibility.SourceOverspending = overage
	}
	eligibility.OutgoingRollover, err = sumPositive(ctx, tx, `SELECT amount_hundredths FROM budget_rollovers WHERE source_month = ? AND category_id = ?`, key.SourceMonth, key.CategoryID, "outgoing rollover")
	if err != nil {
		return Eligibility{}, err
	}
	eligibility.EligibleRollover, ok = checkedSignedSub(eligibility.SourceOverspending, eligibility.OutgoingRollover)
	if !ok {
		return Eligibility{}, errors.New("eligible rollover overflow")
	}
	return eligibility, nil
}

func sumPositive(ctx context.Context, tx *sql.Tx, query string, args ...any) (int64, error) {
	kind := "amount"
	if len(args) > 0 {
		if value, ok := args[len(args)-1].(string); ok {
			kind = value
			args = args[:len(args)-1]
		}
	}
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	defer func() { _ = rows.Close() }()
	var total int64
	for rows.Next() {
		var amount int64
		if err := rows.Scan(&amount); err != nil {
			return 0, err
		}
		if amount < 0 || amount > math.MaxInt64-total {
			return 0, fmt.Errorf("%s total overflow", kind)
		}
		total += amount
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	return total, rows.Close()
}

func checkedSignedAdd(left, right int64) (int64, bool) {
	if right > 0 && left > math.MaxInt64-right {
		return 0, false
	}
	if right < 0 && left < math.MinInt64-right {
		return 0, false
	}
	return left + right, true
}

func checkedSignedSub(left, right int64) (int64, bool) {
	if right > 0 && left < math.MinInt64+right {
		return 0, false
	}
	if right < 0 && left > math.MaxInt64+right {
		return 0, false
	}
	return left - right, true
}

// Snapshot captures all stored budget buckets in one caller-owned read/write
// transaction. Missing keys are intentionally absent and treated as zero by
// callers when comparing before/after states.
func Snapshot(ctx context.Context, tx *sql.Tx) (map[Key]Eligibility, error) {
	rows, err := tx.QueryContext(ctx, `SELECT month, category_id FROM budgets ORDER BY month, category_id`)
	if err != nil {
		return nil, err
	}
	keys := make([]Key, 0)
	for rows.Next() {
		var key Key
		if err := rows.Scan(&key.SourceMonth, &key.CategoryID); err != nil {
			_ = rows.Close()
			return nil, err
		}
		keys = append(keys, key)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	snapshot := make(map[Key]Eligibility, len(keys))
	for _, key := range keys {
		eligibility, err := Evaluate(ctx, tx, key)
		if err != nil {
			return nil, err
		}
		snapshot[key] = eligibility
	}
	return snapshot, nil
}

// ValidateOutgoing enforces the mutation guard for every persisted outgoing
// rollover. It should be called after a prospective mutation and before commit.
func ValidateOutgoing(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT source_month, category_id FROM budget_rollovers ORDER BY source_month, category_id`)
	if err != nil {
		return err
	}
	keys := make([]Key, 0)
	for rows.Next() {
		var key Key
		if err := rows.Scan(&key.SourceMonth, &key.CategoryID); err != nil {
			_ = rows.Close()
			return err
		}
		keys = append(keys, key)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	conflicts := make([]Conflict, 0)
	for _, key := range keys {
		eligibility, err := Evaluate(ctx, tx, key)
		if err != nil {
			return err
		}
		if eligibility.OutgoingRollover == 0 || (eligibility.HasBudget && eligibility.OutgoingRollover <= eligibility.SourceOverspending) {
			continue
		}
		ids, err := outgoingIDs(ctx, tx, key)
		if err != nil {
			return err
		}
		conflicts = append(conflicts, Conflict{
			SourceMonth:        key.SourceMonth,
			CategoryID:         key.CategoryID,
			RolloverIDs:        ids,
			SourceOverspending: eligibility.SourceOverspending,
			OutgoingRollover:   eligibility.OutgoingRollover,
			EligibleRollover:   eligibility.EligibleRollover,
		})
	}
	if len(conflicts) == 0 {
		return nil
	}
	ids := make([]int64, 0)
	for _, conflict := range conflicts {
		ids = append(ids, conflict.RolloverIDs...)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return &DependencyConflictError{Conflicts: conflicts, RolloverIDs: ids}
}

func outgoingIDs(ctx context.Context, tx *sql.Tx, key Key) ([]int64, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id FROM budget_rollovers WHERE source_month = ? AND category_id = ? ORDER BY id`, key.SourceMonth, key.CategoryID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	ids := make([]int64, 0)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return ids, rows.Close()
}

// BuildOffers evaluates post-write state and returns offers only for buckets
// whose uncovered eligibility increased. The returned slice is always non-nil.
func BuildOffers(ctx context.Context, tx *sql.Tx, before map[Key]Eligibility, changes []OfferChange) ([]contract.RolloverOffer, error) {
	if len(changes) == 0 {
		return []contract.RolloverOffer{}, nil
	}
	byKey := make(map[Key]OfferChange, len(changes))
	for _, change := range changes {
		if existing, ok := byKey[change.Key]; ok {
			if change.PreviousEligible < existing.PreviousEligible {
				change.PreviousEligible = existing.PreviousEligible
			}
		}
		byKey[change.Key] = change
	}
	keys := make([]Key, 0, len(byKey))
	for key := range byKey {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].SourceMonth != keys[j].SourceMonth {
			return keys[i].SourceMonth < keys[j].SourceMonth
		}
		return keys[i].CategoryID < keys[j].CategoryID
	})
	offers := make([]contract.RolloverOffer, 0, len(keys))
	for _, key := range keys {
		activeFund, err := sinkingfund.ActiveAt(ctx, tx, key.CategoryID, key.SourceMonth)
		if err != nil {
			return nil, err
		}
		if activeFund {
			continue
		}
		after, err := Evaluate(ctx, tx, key)
		if err != nil {
			return nil, err
		}
		previous := byKey[key].PreviousEligible
		if previous == 0 {
			if prior, ok := before[key]; ok {
				previous = prior.EligibleRollover
			}
		}
		if !after.HasBudget || after.EligibleRollover <= 0 || after.EligibleRollover <= previous {
			continue
		}
		offer, err := formatOffer(after, byKey[key].SourceTransactionID)
		if err != nil {
			return nil, err
		}
		offers = append(offers, offer)
	}
	return offers, nil
}

func formatOffer(eligibility Eligibility, sourceTransactionID *int64) (contract.RolloverOffer, error) {
	target, err := contract.NextMonth(eligibility.Key.SourceMonth)
	if err != nil {
		return contract.RolloverOffer{}, err
	}
	base, err := contract.FormatAmount(eligibility.BaseBudget)
	if err != nil {
		return contract.RolloverOffer{}, err
	}
	available, err := contract.FormatSignedAmount(eligibility.AvailableBudget)
	if err != nil {
		return contract.RolloverOffer{}, err
	}
	spending, err := contract.FormatAmount(eligibility.Spending)
	if err != nil {
		return contract.RolloverOffer{}, err
	}
	eligible, err := contract.FormatAmount(eligibility.EligibleRollover)
	if err != nil {
		return contract.RolloverOffer{}, err
	}
	var sourceID *int64
	if sourceTransactionID != nil {
		value := *sourceTransactionID
		sourceID = &value
	}
	return contract.RolloverOffer{
		SourceMonth:         eligibility.Key.SourceMonth,
		TargetMonth:         target,
		CategoryID:          eligibility.Key.CategoryID,
		Category:            eligibility.Category,
		SourceTransactionID: sourceID,
		BaseBudget:          base,
		AvailableBudget:     available,
		SpendingAfter:       spending,
		EligibleRollover:    eligible,
	}, nil
}
