package transaction

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

const MaxBatchTransactions = 100

type BatchRow struct {
	Amount   string
	Merchant string
	Category *string
	Date     string
	Note     *string
}

type AddBatchInput struct {
	IdempotencyKey string
	Transactions   []BatchRow
}

type AddBatchResult struct {
	IdempotencyKey   string
	IdempotentReplay bool
	Transactions     []AddResult
	TotalHundredths  int64
}

type BatchRowError struct {
	Index int
	Err   error
}

func (e *BatchRowError) Error() string {
	if e == nil {
		return "transaction batch row error"
	}
	if e.Err == nil {
		return fmt.Sprintf("transaction batch row %d failed", e.Index)
	}
	return fmt.Sprintf("transaction batch row %d: %v", e.Index, e.Err)
}

func (e *BatchRowError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

type validatedBatch struct {
	idempotencyKey string
	fingerprint    string
	rows           []validatedAdd
	total          int64
}

type rowQueryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

type importRecord struct {
	id          int64
	fingerprint string
}

type importItem struct {
	index          int
	transactionID  sql.NullInt64
	categorySource string
	mappingAction  string
}

// AddBatch validates and records a confirmed expense batch atomically.
func (s *Store) AddBatch(ctx context.Context, in AddBatchInput) (AddBatchResult, []contract.FieldIssue, error) {
	now := time.Now()
	if s != nil && s.Now != nil {
		now = s.Now()
	}

	validated, fields := validateAddBatch(in, now)
	if len(fields) != 0 {
		return AddBatchResult{}, fields, nil
	}
	fingerprint, err := fingerprintAddBatch(validated.rows)
	if err != nil {
		return AddBatchResult{}, nil, err
	}
	validated.fingerprint = fingerprint
	if s == nil || s.DB == nil {
		return AddBatchResult{}, nil, errors.New("transaction store database is nil")
	}
	return s.addBatch(ctx, validated)
}

func (s *Store) addBatch(ctx context.Context, in validatedBatch) (AddBatchResult, []contract.FieldIssue, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return AddBatchResult{}, nil, err
	}
	defer func() { _ = tx.Rollback() }()

	result, err := addBatchInTx(ctx, tx, in)
	if isUniqueConstraintOn(err, "transaction_imports") {
		_ = tx.Rollback()
		replay, replayErr := replayImport(ctx, s.DB, in.idempotencyKey, in.fingerprint)
		if replayErr != nil {
			return AddBatchResult{}, nil, replayErr
		}
		return replay, nil, nil
	}
	if err != nil {
		return AddBatchResult{}, nil, err
	}
	if err := tx.Commit(); err != nil {
		return AddBatchResult{}, nil, err
	}
	return result, nil, nil
}

func addBatchInTx(ctx context.Context, tx *sql.Tx, in validatedBatch) (AddBatchResult, error) {
	existing, found, err := lookupImport(ctx, tx, in.idempotencyKey)
	if err != nil {
		return AddBatchResult{}, err
	}
	if found {
		return replayImportRecord(ctx, tx, in.idempotencyKey, in.fingerprint, existing)
	}

	results := make([]AddResult, 0, len(in.rows))
	for index, row := range in.rows {
		result, err := addValidatedInTx(ctx, tx, row)
		if err != nil {
			if isBatchDomainError(err) {
				return AddBatchResult{}, &BatchRowError{Index: index, Err: err}
			}
			return AddBatchResult{}, err
		}
		results = append(results, result)
	}

	insert, err := tx.ExecContext(ctx, `
		INSERT INTO transaction_imports (idempotency_key, request_fingerprint)
		VALUES (?, ?)
	`, in.idempotencyKey, in.fingerprint)
	if err != nil {
		return AddBatchResult{}, err
	}
	importID, err := insert.LastInsertId()
	if err != nil {
		return AddBatchResult{}, err
	}
	for index, result := range results {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO transaction_import_items (
				import_id,
				item_index,
				transaction_id,
				category_source,
				merchant_mapping_action
			)
			VALUES (?, ?, ?, ?, ?)
		`, importID, index, result.Transaction.ID, result.CategorySource, result.MerchantMappingAction); err != nil {
			return AddBatchResult{}, err
		}
	}

	return AddBatchResult{
		IdempotencyKey:   in.idempotencyKey,
		IdempotentReplay: false,
		Transactions:     results,
		TotalHundredths:  in.total,
	}, nil
}

func replayImport(ctx context.Context, q rowQueryer, key, fingerprint string) (AddBatchResult, error) {
	existing, found, err := lookupImport(ctx, q, key)
	if err != nil {
		return AddBatchResult{}, err
	}
	if !found {
		return AddBatchResult{}, fmt.Errorf("idempotency key %q was not found after unique conflict", key)
	}
	return replayImportRecord(ctx, q, key, fingerprint, existing)
}

func replayImportRecord(ctx context.Context, q rowQueryer, key, fingerprint string, existing importRecord) (AddBatchResult, error) {
	if existing.fingerprint != fingerprint {
		return AddBatchResult{}, &IdempotencyConflictError{
			IdempotencyKey: key,
			Reason:         IdempotencyReasonPayloadMismatch,
		}
	}

	items, err := listImportItems(ctx, q, existing.id)
	if err != nil {
		return AddBatchResult{}, err
	}

	removed := make([]int, 0)
	for _, item := range items {
		if !item.transactionID.Valid {
			removed = append(removed, item.index)
		}
	}
	if len(removed) > 0 {
		return AddBatchResult{}, &IdempotencyConflictError{
			IdempotencyKey: key,
			Reason:         IdempotencyReasonTransactionRemoved,
			RemovedIndexes: removed,
		}
	}

	results := make([]AddResult, 0, len(items))
	var total int64
	for _, item := range items {
		recorded, err := scanTransaction(q.QueryRowContext(ctx, `
			SELECT `+transactionColumns+`
			FROM transactions AS t
			INNER JOIN categories AS c ON c.id = t.category_id
			WHERE t.id = ?
		`, item.transactionID.Int64))
		if err != nil {
			return AddBatchResult{}, err
		}
		amount, err := contract.ParseAmount(recorded.Amount)
		if err != nil {
			return AddBatchResult{}, err
		}
		next, ok := checkedAdd(total, amount)
		if !ok {
			return AddBatchResult{}, errors.New("imported transaction total overflow")
		}
		total = next
		results = append(results, AddResult{
			Transaction:           recorded,
			CategorySource:        item.categorySource,
			MerchantMappingAction: item.mappingAction,
		})
	}

	return AddBatchResult{
		IdempotencyKey:   key,
		IdempotentReplay: true,
		Transactions:     results,
		TotalHundredths:  total,
	}, nil
}

func lookupImport(ctx context.Context, q rowQueryer, key string) (importRecord, bool, error) {
	var rec importRecord
	err := q.QueryRowContext(ctx, `
		SELECT id, request_fingerprint
		FROM transaction_imports
		WHERE idempotency_key = ?
	`, key).Scan(&rec.id, &rec.fingerprint)
	if errors.Is(err, sql.ErrNoRows) {
		return importRecord{}, false, nil
	}
	if err != nil {
		return importRecord{}, false, err
	}
	return rec, true, nil
}

func listImportItems(ctx context.Context, q rowQueryer, importID int64) ([]importItem, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT item_index, transaction_id, category_source, merchant_mapping_action
		FROM transaction_import_items
		WHERE import_id = ?
		ORDER BY item_index
	`, importID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	items := make([]importItem, 0)
	for rows.Next() {
		var item importItem
		if err := rows.Scan(&item.index, &item.transactionID, &item.categorySource, &item.mappingAction); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	return items, nil
}

func validateAddBatch(in AddBatchInput, now time.Time) (validatedBatch, []contract.FieldIssue) {
	fields := make([]contract.FieldIssue, 0)
	validated := validatedBatch{}

	if key, issue := validateIdempotencyKey(in.IdempotencyKey); issue != nil {
		fields = append(fields, *issue)
	} else {
		validated.idempotencyKey = key
	}

	if len(in.Transactions) < 1 || len(in.Transactions) > MaxBatchTransactions {
		fields = append(fields, contract.FieldIssue{
			Field:  "transactions",
			Reason: "must contain between 1 and 100 items",
		})
		return validated, fields
	}

	today := LocalDate(now)
	validated.rows = make([]validatedAdd, len(in.Transactions))
	parsedAmounts := make([]int64, len(in.Transactions))
	amountsOK := true
	for index, row := range in.Transactions {
		item, itemFields, amount, amountOK := validateAddBatchRow(row, index, today)
		fields = append(fields, itemFields...)
		validated.rows[index] = item
		if !amountOK {
			amountsOK = false
			continue
		}
		parsedAmounts[index] = amount
	}

	if amountsOK {
		var total int64
		overflow := false
		for _, amount := range parsedAmounts {
			next, ok := checkedAdd(total, amount)
			if !ok {
				overflow = true
				break
			}
			total = next
		}
		if overflow {
			fields = append(fields, contract.FieldIssue{
				Field:  "transactions",
				Reason: "total must fit the supported amount range",
			})
		} else {
			validated.total = total
		}
	}

	if len(fields) != 0 {
		return validatedBatch{}, fields
	}
	return validated, nil
}

func validateAddBatchRow(row BatchRow, index int, today string) (validatedAdd, []contract.FieldIssue, int64, bool) {
	fields := make([]contract.FieldIssue, 0)
	validated := validatedAdd{}
	amountOK := false
	var amount int64

	if parsed, issue := validateAmount(row.Amount); issue != nil {
		fields = append(fields, prefixFieldIssue(index, *issue))
	} else {
		validated.amountHundredths = parsed
		amount = parsed
		amountOK = true
	}

	if merchant, issue := validateMerchant(row.Merchant); issue != nil {
		fields = append(fields, prefixFieldIssue(index, *issue))
	} else {
		validated.merchant = merchant
	}

	if row.Category != nil {
		if category, issue := validateCategoryName(*row.Category); issue != nil {
			fields = append(fields, prefixFieldIssue(index, *issue))
		} else {
			validated.category = &category
		}
	}

	if date, issue := validateDate(row.Date, today); issue != nil {
		fields = append(fields, prefixFieldIssue(index, *issue))
	} else {
		validated.date = date
	}

	if row.Note != nil {
		if note, issue := validateNote(*row.Note); issue != nil {
			fields = append(fields, prefixFieldIssue(index, *issue))
		} else {
			validated.note = note
		}
	}

	return validated, fields, amount, amountOK
}

func validateIdempotencyKey(value string) (string, *contract.FieldIssue) {
	key := contract.TrimASCIIWhitespace(value)
	switch {
	case key == "":
		return "", &contract.FieldIssue{
			Field:  "idempotency_key",
			Reason: "must not be empty",
		}
	case strings.ContainsRune(key, '\x00'):
		return "", &contract.FieldIssue{
			Field:  "idempotency_key",
			Reason: "must not contain NUL characters",
		}
	default:
		return key, nil
	}
}

func prefixFieldIssue(index int, issue contract.FieldIssue) contract.FieldIssue {
	return contract.FieldIssue{
		Field:  fmt.Sprintf("transactions[%d].%s", index, issue.Field),
		Reason: issue.Reason,
	}
}

func isBatchDomainError(err error) bool {
	return errors.Is(err, ErrCategoryNotFound) ||
		errors.Is(err, ErrCategoryInactive) ||
		errors.Is(err, ErrMerchantCategoryRequired) ||
		errors.Is(err, ErrMerchantCategoryInactive)
}

func checkedAdd(left, right int64) (int64, bool) {
	if left < 0 || right < 0 || right > math.MaxInt64-left {
		return 0, false
	}
	return left + right, true
}
