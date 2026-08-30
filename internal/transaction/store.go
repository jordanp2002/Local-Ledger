// Package transaction implements transaction domain rules.
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
	ErrCategoryNotFound                    = errors.New("category not found")
	ErrCategoryInactive                    = errors.New("category inactive")
	ErrMerchantCategoryRequired            = errors.New("merchant category required")
	ErrMerchantCategoryInactive            = errors.New("merchant category inactive")
	ErrTransactionNotFound                 = errors.New("transaction not found")
	ErrSplitTransactionRequiresAllocations = errors.New("split transaction requires allocations")
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
	Amount         string
	Merchant       string
	Category       *string
	Date           *string
	Note           *string
	IdempotencyKey *string
}

type AllocationInput struct {
	Category string
	Amount   string
}

type AddSplitInput struct {
	Merchant       string
	Date           *string
	Note           *string
	Allocations    []AllocationInput
	IdempotencyKey *string
}

// InTxAllocation is one already-resolved allocation for AddInTx.
type InTxAllocation struct {
	CategoryID       int64
	AmountHundredths int64
}

// InTxInput inserts a complete transaction inside a caller-owned transaction.
// CreatedAt and UpdatedAt may be set when the caller owns the operation clock.
type InTxInput struct {
	Merchant    string
	Date        string
	Note        *string
	Allocations []InTxAllocation
	CreatedAt   string
	UpdatedAt   string
}

// AddInTx writes one parent and its complete allocation set atomically. It is
// intended for domain operations that already own a SQLite transaction.
func AddInTx(ctx context.Context, tx *sql.Tx, in InTxInput) (contract.Transaction, error) {
	if tx == nil {
		return contract.Transaction{}, errors.New("transaction SQL transaction is nil")
	}
	if len(in.Allocations) == 0 {
		return contract.Transaction{}, errors.New("transaction must have at least one allocation")
	}
	seen := make(map[int64]struct{}, len(in.Allocations))
	var total int64
	for _, allocation := range in.Allocations {
		if allocation.CategoryID < 1 || allocation.AmountHundredths < 1 {
			return contract.Transaction{}, errors.New("transaction allocations must be positive")
		}
		if _, exists := seen[allocation.CategoryID]; exists {
			return contract.Transaction{}, errors.New("transaction allocations must use distinct categories")
		}
		next, ok := checkedAllocationAdd(total, allocation.AmountHundredths)
		if !ok {
			return contract.Transaction{}, errors.New("transaction allocation total overflow")
		}
		total = next
		seen[allocation.CategoryID] = struct{}{}
	}
	query := `INSERT INTO transactions (merchant, date, note) VALUES (?, ?, ?)`
	args := []any{in.Merchant, in.Date, in.Note}
	if in.CreatedAt != "" || in.UpdatedAt != "" {
		if in.CreatedAt == "" || in.UpdatedAt == "" {
			return contract.Transaction{}, errors.New("transaction timestamps must be supplied together")
		}
		query = `INSERT INTO transactions (merchant, date, note, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`
		args = append(args, in.CreatedAt, in.UpdatedAt)
	}
	result, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return contract.Transaction{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return contract.Transaction{}, err
	}
	for _, allocation := range in.Allocations {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO transaction_allocations (transaction_id, category_id, amount_hundredths)
			VALUES (?, ?, ?)
		`, id, allocation.CategoryID, allocation.AmountHundredths); err != nil {
			return contract.Transaction{}, err
		}
	}
	return loadTransaction(ctx, tx, id)
}

// AddResult is the canonical result of recording one transaction.
type AddResult struct {
	Transaction           contract.Transaction
	CategorySource        string
	MerchantMappingAction string
	IdempotentReplay      bool
}

type NotePatch struct {
	Present bool
	Value   *string // Present+nil = clear note
}

type UpdateInput struct {
	ID              int64
	Amount          *string
	AmountNull      bool
	Merchant        *string
	MerchantNull    bool
	Category        *string
	CategoryNull    bool
	Date            *string
	DateNull        bool
	Note            NotePatch
	Allocations     *[]AllocationInput
	AllocationsNull bool
}

// UpdateResult is the canonical joined transaction after a patch.
type UpdateResult struct {
	Transaction contract.Transaction
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

// TransactionNotFoundError identifies a missing transaction ID.
type TransactionNotFoundError struct {
	ID int64
}

type SplitTransactionRequiresAllocationsError struct {
	ID int64
}

func (e *SplitTransactionRequiresAllocationsError) Error() string {
	if e == nil {
		return ErrSplitTransactionRequiresAllocations.Error()
	}
	return fmt.Sprintf("transaction %d is split and requires allocations", e.ID)
}

func (e *SplitTransactionRequiresAllocationsError) Is(target error) bool {
	return target == ErrSplitTransactionRequiresAllocations
}

func (e *TransactionNotFoundError) Error() string {
	if e == nil {
		return ErrTransactionNotFound.Error()
	}
	return fmt.Sprintf("transaction %d was not found", e.ID)
}

func (e *TransactionNotFoundError) Is(target error) bool {
	return target == ErrTransactionNotFound
}

type validatedAdd struct {
	amountHundredths int64
	merchant         string
	category         *string
	date             string
	dateOmitted      bool
	note             sql.NullString
	idempotencyKey   string
	fingerprint      string
}

type validatedAllocation struct {
	categoryID   int64
	categoryName string
	amount       int64
}

type validatedSplit struct {
	merchant       string
	date           string
	dateOmitted    bool
	note           sql.NullString
	allocations    []AllocationInput
	idempotencyKey string
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
	if validated.idempotencyKey != "" {
		fingerprint, err := fingerprintAdd(validated)
		if err != nil {
			return AddResult{}, nil, err
		}
		validated.fingerprint = fingerprint
	}
	if s == nil || s.DB == nil {
		return AddResult{}, nil, errors.New("transaction store database is nil")
	}
	return s.add(ctx, validated)
}

func (s *Store) AddSplit(ctx context.Context, in AddSplitInput) (AddResult, []contract.FieldIssue, error) {
	now := time.Now()
	if s != nil && s.Now != nil {
		now = s.Now()
	}
	validated, fields := validateAddSplit(in, now)
	if len(fields) != 0 {
		return AddResult{}, fields, nil
	}
	if s == nil || s.DB == nil {
		return AddResult{}, nil, errors.New("transaction store database is nil")
	}
	return s.addSplit(ctx, validated)
}

func validateAddSplit(in AddSplitInput, now time.Time) (validatedSplit, []contract.FieldIssue) {
	fields := make([]contract.FieldIssue, 0)
	validated := validatedSplit{}
	if merchant, issue := validateMerchant(in.Merchant); issue != nil {
		fields = append(fields, *issue)
	} else {
		validated.merchant = merchant
	}
	today := LocalDate(now)
	if in.Date != nil {
		if date, issue := validateDate(*in.Date, today); issue != nil {
			fields = append(fields, *issue)
		} else {
			validated.date = date
		}
	} else {
		validated.date = today
		validated.dateOmitted = true
	}
	if in.Note != nil {
		if note, issue := validateNote(*in.Note); issue != nil {
			fields = append(fields, *issue)
		} else {
			validated.note = note
		}
	}
	if in.IdempotencyKey != nil {
		if key, issue := validateIdempotencyKey(*in.IdempotencyKey); issue != nil {
			fields = append(fields, *issue)
		} else {
			validated.idempotencyKey = key
		}
	}
	if len(in.Allocations) < 2 {
		fields = append(fields, contract.FieldIssue{Field: "allocations", Reason: "must contain at least two items"})
	} else {
		validated.allocations = make([]AllocationInput, len(in.Allocations))
		seen := make(map[string]struct{}, len(in.Allocations))
		var total int64
		for i, allocation := range in.Allocations {
			category, issue := validateCategoryName(allocation.Category)
			if issue != nil {
				issue.Field = fmt.Sprintf("allocations[%d].category", i)
				fields = append(fields, *issue)
			} else {
				key := strings.ToLower(category)
				if _, exists := seen[key]; exists {
					fields = append(fields, contract.FieldIssue{Field: fmt.Sprintf("allocations[%d].category", i), Reason: "must not repeat a category"})
				}
				seen[key] = struct{}{}
				validated.allocations[i].Category = category
			}
			amount, amountIssue := validateAmount(allocation.Amount)
			if amountIssue != nil {
				amountIssue.Field = fmt.Sprintf("allocations[%d].amount", i)
				fields = append(fields, *amountIssue)
			} else {
				next, ok := checkedAdd(total, amount)
				if !ok {
					fields = append(fields, contract.FieldIssue{Field: "allocations", Reason: "total must fit the supported amount range"})
				} else {
					total = next
				}
				validated.allocations[i].Amount = allocation.Amount
			}
		}
	}
	return validated, fields
}

func validateAdd(in AddInput, now time.Time) (validatedAdd, []contract.FieldIssue) {
	fields := make([]contract.FieldIssue, 0)
	validated := validatedAdd{}

	if amount, issue := validateAmount(in.Amount); issue != nil {
		fields = append(fields, *issue)
	} else {
		validated.amountHundredths = amount
	}

	if merchant, issue := validateMerchant(in.Merchant); issue != nil {
		fields = append(fields, *issue)
	} else {
		validated.merchant = merchant
	}

	if in.Category != nil {
		if category, issue := validateCategoryName(*in.Category); issue != nil {
			fields = append(fields, *issue)
		} else {
			validated.category = &category
		}
	}

	today := LocalDate(now)
	if in.Date != nil {
		if date, issue := validateDate(*in.Date, today); issue != nil {
			fields = append(fields, *issue)
		} else {
			validated.date = date
		}
	} else {
		validated.date = today
		validated.dateOmitted = true
	}

	if in.Note != nil {
		if note, issue := validateNote(*in.Note); issue != nil {
			fields = append(fields, *issue)
		} else {
			validated.note = note
		}
	}

	if in.IdempotencyKey != nil {
		if key, issue := validateIdempotencyKey(*in.IdempotencyKey); issue != nil {
			fields = append(fields, *issue)
		} else {
			validated.idempotencyKey = key
		}
	}

	return validated, fields
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

func validateDate(value, today string) (string, *contract.FieldIssue) {
	parsed, err := contract.ParseDate(value)
	if err != nil {
		return "", &contract.FieldIssue{
			Field:  "date",
			Reason: "must be a valid YYYY-MM-DD date",
		}
	}
	if parsed > today {
		return "", &contract.FieldIssue{
			Field:  "date",
			Reason: "must not be in the future",
		}
	}
	return parsed, nil
}

func validateNote(value string) (sql.NullString, *contract.FieldIssue) {
	note := contract.TrimASCIIWhitespace(value)
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

func (s *Store) add(ctx context.Context, in validatedAdd) (AddResult, []contract.FieldIssue, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return AddResult{}, nil, err
	}
	defer func() { _ = tx.Rollback() }()

	result, err := addInTx(ctx, tx, in)
	if isUniqueConstraintOn(err, "transaction_idempotency") {
		_ = tx.Rollback()
		replay, replayErr := replayIdempotency(ctx, s.DB, in.idempotencyKey, in.fingerprint)
		if replayErr != nil {
			return AddResult{}, nil, replayErr
		}
		return replay, nil, nil
	}
	if err != nil {
		return AddResult{}, nil, err
	}
	if err := tx.Commit(); err != nil {
		return AddResult{}, nil, err
	}
	return result, nil, nil
}

func addValidatedInTx(ctx context.Context, tx *sql.Tx, in validatedAdd) (AddResult, error) {
	var supplied *contract.Category
	if in.category != nil {
		category, err := resolveActiveCategory(ctx, tx, *in.category)
		if err != nil {
			return AddResult{}, err
		}
		supplied = &category
	}

	mapping, found, err := lookupKnownMerchant(ctx, tx, in.merchant)
	if err != nil {
		return AddResult{}, err
	}

	var (
		categorySource string
		mappingAction  string
		categoryID     int64
	)
	if supplied == nil {
		categorySource = CategorySourceKnownMerchant
		if !found {
			return AddResult{}, &MerchantCategoryRequiredError{Merchant: in.merchant}
		}
		if !mapping.CategoryActive {
			activeCategories, err := listActiveCategories(ctx, tx)
			if err != nil {
				return AddResult{}, err
			}
			return AddResult{}, &MerchantCategoryInactiveError{
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
		INSERT INTO transactions (merchant, date, note)
		VALUES (?, ?, ?)
	`, in.merchant, in.date, in.note)
	if err != nil {
		return AddResult{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return AddResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO transaction_allocations (transaction_id, category_id, amount_hundredths)
		VALUES (?, ?, ?)
	`, id, categoryID, in.amountHundredths); err != nil {
		return AddResult{}, err
	}

	switch mappingAction {
	case MappingActionCreated:
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO known_merchants (merchant, category_id)
			VALUES (?, ?)
		`, in.merchant, categoryID); err != nil {
			return AddResult{}, err
		}
	case MappingActionReplacedInactive:
		if _, err := tx.ExecContext(ctx, `
			UPDATE known_merchants
			SET category_id = ?,
			    updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
			WHERE id = ?
		`, categoryID, mapping.ID); err != nil {
			return AddResult{}, err
		}
	}

	recorded, err := getTransactionByID(ctx, tx, id)
	if err != nil {
		return AddResult{}, err
	}
	return AddResult{
		Transaction:           recorded,
		CategorySource:        categorySource,
		MerchantMappingAction: mappingAction,
	}, nil
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
	return loadTransaction(ctx, tx, id)
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
