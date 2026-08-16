// Package budget implements current-month budget snapshots.
package budget

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/jordanp2002/local-finance-mcp/internal/contract"
)

var (
	ErrAlreadyExists    = errors.New("monthly budget already exists")
	ErrCategoryNotFound = errors.New("category not found")
	ErrCategoryInactive = errors.New("category inactive")
	ErrSourceNotFound   = errors.New("budget source not found")
	ErrSourceEmpty      = errors.New("budget source empty")

	// ErrMonthlyBudgetAlreadyExists aliases ErrAlreadyExists.
	ErrMonthlyBudgetAlreadyExists = ErrAlreadyExists
)

const (
	creationModeExplicit     = "explicit"
	creationModeCarryForward = "carry_forward"
)

// Allocation is one category amount in a monthly budget request.
type Allocation struct {
	Category string
	Amount   string
}

// CreateInput is one create_monthly_budget request at the store boundary.
type CreateInput struct {
	Month        string
	Budgets      []Allocation
	CarryForward *bool
	Overrides    []Allocation
}

// CreateResult is the canonical result of creating a monthly budget snapshot.
type CreateResult struct {
	Month        string
	CreationMode string
	SourceMonth  *string
	TotalBudget  string
	Budgets      []contract.Budget
}

// Store owns budget validation and persistence.
type Store struct {
	DB  *sql.DB
	Now func() time.Time
}

// AlreadyExistsError identifies a month with an existing snapshot.
type AlreadyExistsError struct {
	Month string
}

func (e *AlreadyExistsError) Error() string {
	if e == nil {
		return ErrAlreadyExists.Error()
	}
	return fmt.Sprintf("monthly budget already exists for %s", e.Month)
}

func (e *AlreadyExistsError) Is(target error) bool {
	return target == ErrAlreadyExists
}

// CategoryNotFoundError identifies a missing category.
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

// CategoryInactiveError identifies an inactive category.
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

// SourceNotFoundError identifies a carry-forward request with no earlier snapshot.
type SourceNotFoundError struct {
	Month string
}

func (e *SourceNotFoundError) Error() string {
	if e == nil {
		return ErrSourceNotFound.Error()
	}
	return fmt.Sprintf("no earlier monthly budget to carry forward into %s", e.Month)
}

func (e *SourceNotFoundError) Is(target error) bool {
	return target == ErrSourceNotFound
}

// SourceEmptyError identifies a source month whose active-category filter is empty.
type SourceEmptyError struct {
	Month       string
	SourceMonth string
}

func (e *SourceEmptyError) Error() string {
	if e == nil {
		return ErrSourceEmpty.Error()
	}
	return fmt.Sprintf("earlier monthly budget %s has no active categories to carry forward into %s", e.SourceMonth, e.Month)
}

func (e *SourceEmptyError) Is(target error) bool {
	return target == ErrSourceEmpty
}

type normalizedAllocation struct {
	category string
	amount   int64
}

type resolvedAllocation struct {
	categoryID int64
	amount     int64
}

type createClassification struct {
	mode       string
	normalized []normalizedAllocation
	total      int64
}

const categoryColumns = `id, name, active, created_at, updated_at`

// Create classifies the creation mode and writes the current month's snapshot.
func (s *Store) Create(ctx context.Context, in CreateInput) (CreateResult, []contract.FieldIssue, error) {
	now := time.Now()
	if s != nil && s.Now != nil {
		now = s.Now()
	}

	classified, fields := classifyCreate(in, now)
	if len(fields) != 0 {
		return CreateResult{}, fields, nil
	}
	if s == nil || s.DB == nil {
		return CreateResult{}, nil, errors.New("budget store database is nil")
	}

	switch classified.mode {
	case creationModeExplicit:
		return s.createExplicit(ctx, in.Month, classified.normalized, classified.total)
	case creationModeCarryForward:
		return s.createCarryForward(ctx, in.Month, classified.normalized)
	default:
		return CreateResult{}, nil, errors.New("budget create mode was not classified")
	}
}

// CreateExplicit validates and atomically creates an explicit current-month snapshot.
func (s *Store) CreateExplicit(ctx context.Context, month string, allocations []Allocation) (CreateResult, []contract.FieldIssue, error) {
	return s.Create(ctx, CreateInput{Month: month, Budgets: allocations})
}

// CreateCarryForward copies the latest earlier snapshot into the current month.
func (s *Store) CreateCarryForward(ctx context.Context, month string, overrides []Allocation) (CreateResult, []contract.FieldIssue, error) {
	carryForward := true
	return s.Create(ctx, CreateInput{
		Month:        month,
		CarryForward: &carryForward,
		Overrides:    overrides,
	})
}

func (s *Store) createExplicit(ctx context.Context, month string, normalized []normalizedAllocation, total int64) (CreateResult, []contract.FieldIssue, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return CreateResult{}, nil, err
	}
	defer func() { _ = tx.Rollback() }()

	exists, err := budgetMonthExists(ctx, tx, month)
	if err != nil {
		return CreateResult{}, nil, err
	}
	if exists {
		return CreateResult{}, nil, &AlreadyExistsError{Month: month}
	}

	resolved := make([]resolvedAllocation, 0, len(normalized))
	for _, allocation := range normalized {
		category, err := resolveActiveCategory(ctx, tx, allocation.category)
		if err != nil {
			return CreateResult{}, nil, err
		}
		resolved = append(resolved, resolvedAllocation{
			categoryID: category.ID,
			amount:     allocation.amount,
		})
	}

	// All categories are resolved before writing.
	if err := insertMonthAllocations(ctx, tx, month, resolved); err != nil {
		return CreateResult{}, nil, err
	}

	budgets, err := listBudgetsForMonth(ctx, tx, month, len(resolved))
	if err != nil {
		return CreateResult{}, nil, err
	}
	totalBudget, err := contract.FormatAmount(total)
	if err != nil {
		return CreateResult{}, nil, err
	}

	if err := tx.Commit(); err != nil {
		return CreateResult{}, nil, err
	}
	return CreateResult{
		Month:        month,
		CreationMode: creationModeExplicit,
		SourceMonth:  nil,
		TotalBudget:  totalBudget,
		Budgets:      budgets,
	}, nil, nil
}

func (s *Store) createCarryForward(ctx context.Context, month string, overrides []normalizedAllocation) (CreateResult, []contract.FieldIssue, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return CreateResult{}, nil, err
	}
	defer func() { _ = tx.Rollback() }()

	exists, err := budgetMonthExists(ctx, tx, month)
	if err != nil {
		return CreateResult{}, nil, err
	}
	if exists {
		return CreateResult{}, nil, &AlreadyExistsError{Month: month}
	}

	sourceMonth, found, err := lookupLatestEarlierMonth(ctx, tx, month)
	if err != nil {
		return CreateResult{}, nil, err
	}
	if !found {
		return CreateResult{}, nil, &SourceNotFoundError{Month: month}
	}

	sourceRows, err := loadActiveSourceAllocations(ctx, tx, sourceMonth)
	if err != nil {
		return CreateResult{}, nil, err
	}
	if len(sourceRows) == 0 {
		return CreateResult{}, nil, &SourceEmptyError{Month: month, SourceMonth: sourceMonth}
	}

	resolvedOverrides := make([]resolvedAllocation, 0, len(overrides))
	for _, allocation := range overrides {
		category, err := resolveActiveCategory(ctx, tx, allocation.category)
		if err != nil {
			return CreateResult{}, nil, err
		}
		resolvedOverrides = append(resolvedOverrides, resolvedAllocation{
			categoryID: category.ID,
			amount:     allocation.amount,
		})
	}

	merged := mergeAllocations(sourceRows, resolvedOverrides)
	var total int64
	for _, allocation := range merged {
		next, ok := checkedAdd(total, allocation.amount)
		if !ok {
			field := "carry_forward"
			if len(overrides) > 0 {
				field = "overrides"
			}
			return CreateResult{}, []contract.FieldIssue{{
				Field:  field,
				Reason: "total must fit the supported amount range",
			}}, nil
		}
		total = next
	}

	if err := insertMonthAllocations(ctx, tx, month, merged); err != nil {
		return CreateResult{}, nil, err
	}

	budgets, err := listBudgetsForMonth(ctx, tx, month, len(merged))
	if err != nil {
		return CreateResult{}, nil, err
	}
	totalBudget, err := contract.FormatAmount(total)
	if err != nil {
		return CreateResult{}, nil, err
	}

	if err := tx.Commit(); err != nil {
		return CreateResult{}, nil, err
	}
	return CreateResult{
		Month:        month,
		CreationMode: creationModeCarryForward,
		SourceMonth:  &sourceMonth,
		TotalBudget:  totalBudget,
		Budgets:      budgets,
	}, nil, nil
}

func classifyCreate(in CreateInput, now time.Time) (createClassification, []contract.FieldIssue) {
	fields := make([]contract.FieldIssue, 0)
	parsedMonth, monthErr := contract.ParseMonth(in.Month)
	if monthErr != nil {
		fields = append(fields, contract.FieldIssue{
			Field:  "month",
			Reason: "must be a valid YYYY-MM month",
		})
	} else if parsedMonth != localMonth(now) {
		fields = append(fields, contract.FieldIssue{
			Field:  "month",
			Reason: "must equal the current local month",
		})
	}

	carrySelected := in.CarryForward != nil && *in.CarryForward
	switch {
	case carrySelected && len(in.Budgets) > 0:
		fields = append(fields, contract.FieldIssue{
			Field:  "budgets",
			Reason: "cannot be combined with carry_forward",
		})
	case !carrySelected && len(in.Budgets) == 0:
		fields = append(fields, contract.FieldIssue{
			Field:  "budgets",
			Reason: "must contain at least one allocation",
		})
	}
	if in.CarryForward != nil && !*in.CarryForward {
		fields = append(fields, contract.FieldIssue{
			Field:  "carry_forward",
			Reason: "must be true when supplied",
		})
	}
	if !carrySelected && len(in.Overrides) > 0 {
		fields = append(fields, contract.FieldIssue{
			Field:  "overrides",
			Reason: "cannot be supplied unless carry_forward is true",
		})
	}

	switch {
	case carrySelected && len(in.Budgets) == 0:
		normalized, total, allocFields := validateAllocations("overrides", in.Overrides)
		fields = append(fields, allocFields...)
		return createClassification{mode: creationModeCarryForward, normalized: normalized, total: total}, fields
	case in.CarryForward == nil && len(in.Overrides) == 0 && len(in.Budgets) > 0:
		normalized, total, allocFields := validateAllocations("budgets", in.Budgets)
		fields = append(fields, allocFields...)
		return createClassification{mode: creationModeExplicit, normalized: normalized, total: total}, fields
	default:
		return createClassification{}, fields
	}
}

func validateAllocations(prefix string, allocations []Allocation) ([]normalizedAllocation, int64, []contract.FieldIssue) {
	fields := make([]contract.FieldIssue, 0)
	normalized := make([]normalizedAllocation, 0, len(allocations))
	seenCategories := make([]string, 0, len(allocations))
	allAmountsValid := true
	for index, allocation := range allocations {
		categoryName := contract.TrimASCIIWhitespace(allocation.Category)
		categoryValid := true
		switch {
		case categoryName == "":
			fields = append(fields, contract.FieldIssue{
				Field:  fmt.Sprintf("%s[%d].category", prefix, index),
				Reason: "must not be empty",
			})
			categoryValid = false
		case strings.ContainsRune(categoryName, '\x00'):
			fields = append(fields, contract.FieldIssue{
				Field:  fmt.Sprintf("%s[%d].category", prefix, index),
				Reason: "must not contain NUL characters",
			})
			categoryValid = false
		}

		if categoryValid {
			for _, previous := range seenCategories {
				if asciiNoCaseEqual(previous, categoryName) {
					fields = append(fields, contract.FieldIssue{
						Field:  fmt.Sprintf("%s[%d].category", prefix, index),
						Reason: "must not repeat a category",
					})
					break
				}
			}
			seenCategories = append(seenCategories, categoryName)
		}

		amount, amountErr := contract.ParseAmount(allocation.Amount)
		if amountErr != nil {
			fields = append(fields, contract.FieldIssue{
				Field:  fmt.Sprintf("%s[%d].amount", prefix, index),
				Reason: "must be a non-negative amount with at most two decimal places",
			})
			allAmountsValid = false
		}

		normalized = append(normalized, normalizedAllocation{
			category: categoryName,
			amount:   amount,
		})
	}

	var total int64
	if allAmountsValid {
		for _, allocation := range normalized {
			next, ok := checkedAdd(total, allocation.amount)
			if !ok {
				fields = append(fields, contract.FieldIssue{
					Field:  prefix,
					Reason: "total must fit the supported amount range",
				})
				break
			}
			total = next
		}
	}

	return normalized, total, fields
}

func localMonth(now time.Time) string {
	// Format uses the timestamp's location. Do not convert to UTC first.
	return now.Format("2006-01")
}

func checkedAdd(left, right int64) (int64, bool) {
	if left < 0 || right < 0 || right > math.MaxInt64-left {
		return 0, false
	}
	return left + right, true
}

func asciiNoCaseEqual(left, right string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := 0; index < len(left); index++ {
		leftByte, rightByte := left[index], right[index]
		if leftByte >= 'A' && leftByte <= 'Z' {
			leftByte += 'a' - 'A'
		}
		if rightByte >= 'A' && rightByte <= 'Z' {
			rightByte += 'a' - 'A'
		}
		if leftByte != rightByte {
			return false
		}
	}
	return true
}

func mergeAllocations(source, overrides []resolvedAllocation) []resolvedAllocation {
	merged := make([]resolvedAllocation, 0, len(source)+len(overrides))
	indexByCategory := make(map[int64]int, len(source)+len(overrides))
	for _, allocation := range source {
		indexByCategory[allocation.categoryID] = len(merged)
		merged = append(merged, allocation)
	}
	for _, allocation := range overrides {
		if index, ok := indexByCategory[allocation.categoryID]; ok {
			merged[index].amount = allocation.amount
			continue
		}
		indexByCategory[allocation.categoryID] = len(merged)
		merged = append(merged, allocation)
	}
	return merged
}

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

func insertMonthAllocations(ctx context.Context, tx *sql.Tx, month string, allocations []resolvedAllocation) error {
	for _, allocation := range allocations {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO budgets (category_id, month, amount_hundredths)
			VALUES (?, ?, ?)
		`, allocation.categoryID, month, allocation.amount); err != nil {
			return err
		}
	}
	return nil
}

func budgetMonthExists(ctx context.Context, tx *sql.Tx, month string) (bool, error) {
	var marker int64
	err := tx.QueryRowContext(ctx, `
		SELECT 1
		FROM budgets
		WHERE month = ?
		LIMIT 1
	`, month).Scan(&marker)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	case err != nil:
		return false, err
	default:
		return true, nil
	}
}

func lookupLatestEarlierMonth(ctx context.Context, tx *sql.Tx, month string) (string, bool, error) {
	var source string
	err := tx.QueryRowContext(ctx, `
		SELECT month
		FROM budgets
		WHERE month < ?
		ORDER BY month DESC
		LIMIT 1
	`, month).Scan(&source)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "", false, nil
	case err != nil:
		return "", false, err
	default:
		return source, true, nil
	}
}

func loadActiveSourceAllocations(ctx context.Context, tx *sql.Tx, month string) ([]resolvedAllocation, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT b.category_id, b.amount_hundredths, c.active
		FROM budgets AS b
		INNER JOIN categories AS c ON c.id = b.category_id
		WHERE b.month = ?
		ORDER BY c.name COLLATE NOCASE ASC, b.id ASC
	`, month)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	allocations := make([]resolvedAllocation, 0)
	for rows.Next() {
		var allocation resolvedAllocation
		var active bool
		if err := rows.Scan(&allocation.categoryID, &allocation.amount, &active); err != nil {
			return nil, err
		}
		if !active {
			continue
		}
		allocations = append(allocations, allocation)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	return allocations, nil
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

func listBudgetsForMonth(ctx context.Context, tx *sql.Tx, month string, expected int) ([]contract.Budget, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT
			b.id,
			b.month,
			b.category_id,
			c.name,
			b.amount_hundredths,
			b.created_at,
			b.updated_at
		FROM budgets AS b
		INNER JOIN categories AS c ON c.id = b.category_id
		WHERE b.month = ?
		ORDER BY c.name COLLATE NOCASE ASC, b.id ASC
	`, month)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	budgets := make([]contract.Budget, 0, expected)
	for rows.Next() {
		var budget contract.Budget
		var amount int64
		if err := rows.Scan(
			&budget.ID,
			&budget.Month,
			&budget.CategoryID,
			&budget.Category,
			&amount,
			&budget.CreatedAt,
			&budget.UpdatedAt,
		); err != nil {
			return nil, err
		}
		formatted, err := contract.FormatAmount(amount)
		if err != nil {
			return nil, err
		}
		budget.Amount = formatted
		budgets = append(budgets, budget)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if len(budgets) != expected {
		return nil, fmt.Errorf("budget snapshot returned %d rows, expected %d", len(budgets), expected)
	}
	return budgets, nil
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
