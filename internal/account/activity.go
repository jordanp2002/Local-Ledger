package account

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jordanp2002/local-finance-mcp/internal/contract"
)

var (
	ErrEntryNotFound       = errors.New("account entry not found")
	ErrEntryNotReversible  = errors.New("account entry cannot be reversed")
	ErrIdempotencyConflict = errors.New("idempotency conflict")
)

const IdempotencyReasonPayloadMismatch = "payload_mismatch"

type EntryNotFoundError struct{ ID int64 }

func (e *EntryNotFoundError) Error() string {
	return fmt.Sprintf("account entry %d was not found", e.ID)
}
func (e *EntryNotFoundError) Is(target error) bool { return target == ErrEntryNotFound }

type EntryNotReversibleError struct {
	ID   int64
	Kind string
}

func (e *EntryNotReversibleError) Error() string {
	return fmt.Sprintf("account entry %d cannot be reversed", e.ID)
}
func (e *EntryNotReversibleError) Is(target error) bool { return target == ErrEntryNotReversible }

type IdempotencyConflictError struct {
	IdempotencyKey string
	Reason         string
}

func (e *IdempotencyConflictError) Error() string {
	if e == nil {
		return ErrIdempotencyConflict.Error()
	}
	return fmt.Sprintf("idempotency key %q conflicts (%s)", e.IdempotencyKey, e.Reason)
}
func (e *IdempotencyConflictError) Is(target error) bool { return target == ErrIdempotencyConflict }

const (
	ActivityDefaultLimit int64 = 50
	ActivityMaxLimit     int64 = 200
)

type RecordInput struct {
	AccountID      int64
	Type           string
	Amount         string
	Date           string
	Note           *string
	NotePresent    bool
	IdempotencyKey string
}

type RecordResult struct {
	Entry            contract.AccountEntry
	Balance          string
	IdempotentReplay bool
}

type ReconcileInput struct {
	AccountID      int64
	Balance        string
	Note           *string
	NotePresent    bool
	IdempotencyKey string
}

type ReconcileResult struct {
	Entry            *contract.AccountEntry
	PreviousBalance  string
	Adjustment       string
	Balance          string
	Changed          bool
	IdempotentReplay bool
}

type ListActivityInput struct {
	AccountID int64
	StartDate *string
	EndDate   *string
	Kind      *string
	Limit     *int64
	Offset    *int64
}

type ListActivityResult struct {
	Entries []contract.AccountEntry
	Page    contract.Page
}

type ReverseInput struct {
	EntryID        int64
	Note           *string
	NotePresent    bool
	IdempotencyKey string
}

type ReverseResult struct {
	Entry            contract.AccountEntry
	Balance          string
	Changed          bool
	IdempotentReplay bool
}

type entryRow struct {
	id             int64
	accountID      int64
	accountName    string
	kind           string
	delta          int64
	date           string
	note           sql.NullString
	idempotencyKey string
	fingerprint    string
	reversalOf     sql.NullInt64
	createdAt      string
}

func localDate(t time.Time) string { return t.Format("2006-01-02") }

func validateActivityKey(value string) (string, *contract.FieldIssue) {
	key := contract.TrimASCIIWhitespace(value)
	switch {
	case key == "":
		return "", &contract.FieldIssue{Field: "idempotency_key", Reason: "must not be empty"}
	case strings.ContainsRune(key, '\x00'):
		return "", &contract.FieldIssue{Field: "idempotency_key", Reason: "must not contain NUL characters"}
	default:
		return key, nil
	}
}

func validatePositiveAmount(field, value string) (int64, *contract.FieldIssue) {
	if strings.ContainsRune(value, '\x00') {
		return 0, &contract.FieldIssue{Field: field, Reason: "must not contain NUL characters"}
	}
	parsed, err := contract.ParseAmount(value)
	if err != nil {
		return 0, &contract.FieldIssue{Field: field, Reason: "must be a positive amount with at most two decimal places"}
	}
	if parsed == 0 {
		return 0, &contract.FieldIssue{Field: field, Reason: "must be greater than zero"}
	}
	return parsed, nil
}

func validateSignedBalance(value string) (int64, *contract.FieldIssue) {
	if strings.ContainsRune(value, '\x00') {
		return 0, &contract.FieldIssue{Field: "balance", Reason: "must not contain NUL characters"}
	}
	parsed, err := contract.ParseSignedAmount(value)
	if err != nil {
		return 0, &contract.FieldIssue{Field: "balance", Reason: "must be a decimal with at most two places"}
	}
	return parsed, nil
}

func validateActivityDate(value, today string) (string, *contract.FieldIssue) {
	if strings.ContainsRune(value, '\x00') {
		return "", &contract.FieldIssue{Field: "date", Reason: "must not contain NUL characters"}
	}
	parsed, err := contract.ParseDate(value)
	if err != nil {
		return "", &contract.FieldIssue{Field: "date", Reason: "must be a valid YYYY-MM-DD date"}
	}
	if parsed > today {
		return "", &contract.FieldIssue{Field: "date", Reason: "must not be in the future"}
	}
	return parsed, nil
}

func validateActivityNote(value string) (sql.NullString, *string, *contract.FieldIssue) {
	if strings.ContainsRune(value, '\x00') {
		return sql.NullString{}, nil, &contract.FieldIssue{Field: "note", Reason: "must not contain NUL characters"}
	}
	note := contract.TrimASCIIWhitespace(value)
	if note == "" {
		return sql.NullString{}, nil, nil
	}
	return sql.NullString{String: note, Valid: true}, &note, nil
}

func fingerprintPayload(v any) (string, error) {
	payload, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

func toEntryContract(row entryRow, balanceAfter int64) (contract.AccountEntry, error) {
	abs := row.delta
	if abs < 0 {
		if abs == -1<<63 {
			return contract.AccountEntry{}, errors.New("account entry amount overflow")
		}
		abs = -abs
	}
	amount, err := contract.FormatAmount(abs)
	if err != nil {
		return contract.AccountEntry{}, err
	}
	delta, err := contract.FormatSignedAmount(row.delta)
	if err != nil {
		return contract.AccountEntry{}, err
	}
	balance, err := contract.FormatSignedAmount(balanceAfter)
	if err != nil {
		return contract.AccountEntry{}, err
	}
	var note *string
	if row.note.Valid {
		v := row.note.String
		note = &v
	}
	var reversal *int64
	if row.reversalOf.Valid {
		v := row.reversalOf.Int64
		reversal = &v
	}
	return contract.AccountEntry{
		ID:                row.id,
		AccountID:         row.accountID,
		Account:           row.accountName,
		Kind:              row.kind,
		Amount:            amount,
		Delta:             delta,
		Date:              row.date,
		Note:              note,
		ReversalOfEntryID: reversal,
		TransferID:        nil,
		CreatedAt:         row.createdAt,
		BalanceAfter:      balance,
	}, nil
}

func scanEntry(row interface{ Scan(dest ...any) error }) (entryRow, error) {
	var e entryRow
	err := row.Scan(&e.id, &e.accountID, &e.accountName, &e.kind, &e.delta, &e.date, &e.note, &e.idempotencyKey, &e.fingerprint, &e.reversalOf, &e.createdAt)
	return e, err
}

const entryColumns = `e.id, e.account_id, a.name, e.kind, e.delta_hundredths, e.date, e.note, e.idempotency_key, e.fingerprint, e.reversal_of_entry_id, e.created_at`

func getEntryByID(ctx context.Context, tx *sql.Tx, id int64) (entryRow, error) {
	return scanEntry(tx.QueryRowContext(ctx, `SELECT `+entryColumns+` FROM account_entries AS e INNER JOIN accounts AS a ON a.id = e.account_id WHERE e.id = ?`, id))
}

func getEntryByKey(ctx context.Context, tx *sql.Tx, key string) (entryRow, bool, error) {
	row, err := scanEntry(tx.QueryRowContext(ctx, `SELECT `+entryColumns+` FROM account_entries AS e INNER JOIN accounts AS a ON a.id = e.account_id WHERE e.idempotency_key = ?`, key))
	if errors.Is(err, sql.ErrNoRows) {
		return entryRow{}, false, nil
	}
	if err != nil {
		return entryRow{}, false, err
	}
	return row, true, nil
}

type noopRow struct {
	fingerprint string
	accountID   int64
	balance     int64
	previous    int64
	date        string
	createdAt   string
}

func getNoopByKey(ctx context.Context, tx *sql.Tx, key string) (noopRow, bool, error) {
	var n noopRow
	err := tx.QueryRowContext(ctx, `SELECT request_fingerprint, account_id, balance_hundredths, previous_balance_hundredths, date, created_at FROM account_reconcile_noops WHERE idempotency_key = ?`, key).Scan(&n.fingerprint, &n.accountID, &n.balance, &n.previous, &n.date, &n.createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return noopRow{}, false, nil
	}
	if err != nil {
		return noopRow{}, false, err
	}
	return n, true, nil
}

func balanceAfterEntry(ctx context.Context, tx *sql.Tx, account accountRow, target entryRow) (int64, error) {
	// IDs delimit the original write snapshot, including activity entered out of date order.
	rows, err := tx.QueryContext(ctx, `SELECT delta_hundredths, id FROM account_entries WHERE account_id = ? AND id <= ? ORDER BY id ASC`, account.id, target.id)
	if err != nil {
		return 0, err
	}
	defer func() { _ = rows.Close() }()
	total := account.openingHundredths
	for rows.Next() {
		var delta int64
		var id int64
		if err := rows.Scan(&delta, &id); err != nil {
			return 0, err
		}
		next, ok := checkedSignedAdd(total, delta)
		if !ok {
			return 0, errors.New("account balance overflow")
		}
		total = next
		if id == target.id {
			return total, nil
		}
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	return 0, sql.ErrNoRows
}

func isUniqueViolation(err error, table string) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE constraint failed") && strings.Contains(msg, table)
}

func (s *Store) RecordActivity(ctx context.Context, in RecordInput) (RecordResult, []contract.FieldIssue, error) {
	fields := make([]contract.FieldIssue, 0, 6)
	if in.AccountID < 1 {
		fields = append(fields, contract.FieldIssue{Field: "account_id", Reason: "must be a positive integer"})
	}
	activityType := strings.TrimSpace(in.Type)
	if activityType != "deposit" && activityType != "withdrawal" {
		fields = append(fields, contract.FieldIssue{Field: "type", Reason: "must be one of deposit, withdrawal"})
	}
	amount, amountIssue := validatePositiveAmount("amount", in.Amount)
	if amountIssue != nil {
		fields = append(fields, *amountIssue)
	}
	now := s.now()
	today := localDate(now)
	date := ""
	if in.Date == "" {
		fields = append(fields, contract.FieldIssue{Field: "date", Reason: "must not be empty"})
	} else if parsed, issue := validateActivityDate(in.Date, today); issue != nil {
		fields = append(fields, *issue)
	} else {
		date = parsed
	}
	var note sql.NullString
	var notePtr *string
	if in.NotePresent {
		if in.Note == nil {
			note = sql.NullString{}
		} else if parsed, ptr, issue := validateActivityNote(*in.Note); issue != nil {
			fields = append(fields, *issue)
		} else {
			note = parsed
			notePtr = ptr
		}
	}
	key, keyIssue := validateActivityKey(in.IdempotencyKey)
	if keyIssue != nil {
		fields = append(fields, *keyIssue)
	}
	if len(fields) > 0 {
		return RecordResult{}, fields, nil
	}
	fingerprint, err := fingerprintPayload(struct {
		Op      string  `json:"op"`
		Account int64   `json:"account_id"`
		Type    string  `json:"type"`
		Amount  int64   `json:"amount_hundredths"`
		Date    string  `json:"date"`
		Note    *string `json:"note"`
	}{Op: "record", Account: in.AccountID, Type: activityType, Amount: amount, Date: date, Note: notePtr})
	if err != nil {
		return RecordResult{}, nil, err
	}
	if s == nil || s.DB == nil {
		return RecordResult{}, nil, errors.New("account store database is nil")
	}
	delta := amount
	if activityType == "withdrawal" {
		delta = -amount
	}
	stamp := timestamp(now)
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return RecordResult{}, nil, err
	}
	defer func() { _ = tx.Rollback() }()
	account, err := getByID(ctx, tx, in.AccountID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return RecordResult{}, nil, &NotFoundError{ID: in.AccountID}
		}
		return RecordResult{}, nil, err
	}
	if existing, found, err := getEntryByKey(ctx, tx, key); err != nil {
		return RecordResult{}, nil, err
	} else if found {
		if existing.fingerprint != fingerprint {
			return RecordResult{}, nil, &IdempotencyConflictError{IdempotencyKey: key, Reason: IdempotencyReasonPayloadMismatch}
		}
		entryAfter, err := balanceAfterEntry(ctx, tx, account, existing)
		if err != nil {
			return RecordResult{}, nil, err
		}
		entry, err := toEntryContract(existing, entryAfter)
		if err != nil {
			return RecordResult{}, nil, err
		}
		balance, err := contract.FormatSignedAmount(entryAfter)
		if err != nil {
			return RecordResult{}, nil, err
		}
		if err := tx.Commit(); err != nil {
			return RecordResult{}, nil, err
		}
		return RecordResult{Entry: entry, Balance: balance, IdempotentReplay: true}, nil, nil
	}
	if _, found, err := getNoopByKey(ctx, tx, key); err != nil {
		return RecordResult{}, nil, err
	} else if found {
		return RecordResult{}, nil, &IdempotencyConflictError{IdempotencyKey: key, Reason: IdempotencyReasonPayloadMismatch}
	}
	if account.active != 1 {
		return RecordResult{}, nil, ErrInactive
	}
	current, err := balanceInTx(ctx, tx, account.id, account.openingHundredths)
	if err != nil {
		return RecordResult{}, nil, err
	}
	if _, ok := checkedSignedAdd(current, delta); !ok {
		return RecordResult{}, nil, errors.New("account balance overflow")
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO account_entries (account_id, kind, delta_hundredths, date, note, idempotency_key, fingerprint, reversal_of_entry_id, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, NULL, ?)`,
		account.id, activityType, delta, date, note, key, fingerprint, stamp)
	if err != nil {
		if isUniqueViolation(err, "account_entries") {
			_ = tx.Rollback()
			return s.replayRecord(ctx, in.AccountID, key, fingerprint)
		}
		return RecordResult{}, nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return RecordResult{}, nil, err
	}
	inserted, err := getEntryByID(ctx, tx, id)
	if err != nil {
		return RecordResult{}, nil, err
	}
	entryAfter, err := balanceAfterEntry(ctx, tx, account, inserted)
	if err != nil {
		return RecordResult{}, nil, err
	}
	entry, err := toEntryContract(inserted, entryAfter)
	if err != nil {
		return RecordResult{}, nil, err
	}
	balance, err := contract.FormatSignedAmount(entryAfter)
	if err != nil {
		return RecordResult{}, nil, err
	}
	if err := tx.Commit(); err != nil {
		if isUniqueViolation(err, "account_entries") {
			return s.replayRecord(ctx, in.AccountID, key, fingerprint)
		}
		return RecordResult{}, nil, err
	}
	return RecordResult{Entry: entry, Balance: balance}, nil, nil
}

func (s *Store) replayRecord(ctx context.Context, accountID int64, key, fingerprint string) (RecordResult, []contract.FieldIssue, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return RecordResult{}, nil, err
	}
	defer func() { _ = tx.Rollback() }()
	account, err := getByID(ctx, tx, accountID)
	if err != nil {
		return RecordResult{}, nil, err
	}
	existing, found, err := getEntryByKey(ctx, tx, key)
	if err != nil {
		return RecordResult{}, nil, err
	}
	if !found {
		return RecordResult{}, nil, fmt.Errorf("idempotency key %q was not found after unique conflict", key)
	}
	if existing.fingerprint != fingerprint {
		return RecordResult{}, nil, &IdempotencyConflictError{IdempotencyKey: key, Reason: IdempotencyReasonPayloadMismatch}
	}
	entryAfter, err := balanceAfterEntry(ctx, tx, account, existing)
	if err != nil {
		return RecordResult{}, nil, err
	}
	entry, err := toEntryContract(existing, entryAfter)
	if err != nil {
		return RecordResult{}, nil, err
	}
	balance, err := contract.FormatSignedAmount(entryAfter)
	if err != nil {
		return RecordResult{}, nil, err
	}
	if err := tx.Commit(); err != nil {
		return RecordResult{}, nil, err
	}
	return RecordResult{Entry: entry, Balance: balance, IdempotentReplay: true}, nil, nil
}

func (s *Store) ReconcileBalance(ctx context.Context, in ReconcileInput) (ReconcileResult, []contract.FieldIssue, error) {
	fields := make([]contract.FieldIssue, 0, 4)
	if in.AccountID < 1 {
		fields = append(fields, contract.FieldIssue{Field: "account_id", Reason: "must be a positive integer"})
	}
	reported, balanceIssue := validateSignedBalance(in.Balance)
	if balanceIssue != nil {
		fields = append(fields, *balanceIssue)
	}
	var note sql.NullString
	var notePtr *string
	if in.NotePresent {
		if in.Note == nil {
			note = sql.NullString{}
		} else if parsed, ptr, issue := validateActivityNote(*in.Note); issue != nil {
			fields = append(fields, *issue)
		} else {
			note = parsed
			notePtr = ptr
		}
	}
	key, keyIssue := validateActivityKey(in.IdempotencyKey)
	if keyIssue != nil {
		fields = append(fields, *keyIssue)
	}
	if len(fields) > 0 {
		return ReconcileResult{}, fields, nil
	}
	now := s.now()
	today := localDate(now)
	stamp := timestamp(now)
	fingerprint, err := fingerprintPayload(struct {
		Op      string  `json:"op"`
		Account int64   `json:"account_id"`
		Balance int64   `json:"balance_hundredths"`
		Note    *string `json:"note"`
	}{Op: "reconcile", Account: in.AccountID, Balance: reported, Note: notePtr})
	if err != nil {
		return ReconcileResult{}, nil, err
	}
	if s == nil || s.DB == nil {
		return ReconcileResult{}, nil, errors.New("account store database is nil")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return ReconcileResult{}, nil, err
	}
	defer func() { _ = tx.Rollback() }()
	account, err := getByID(ctx, tx, in.AccountID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ReconcileResult{}, nil, &NotFoundError{ID: in.AccountID}
		}
		return ReconcileResult{}, nil, err
	}
	if existing, found, err := getEntryByKey(ctx, tx, key); err != nil {
		return ReconcileResult{}, nil, err
	} else if found {
		if existing.fingerprint != fingerprint {
			return ReconcileResult{}, nil, &IdempotencyConflictError{IdempotencyKey: key, Reason: IdempotencyReasonPayloadMismatch}
		}
		after, err := balanceAfterEntry(ctx, tx, account, existing)
		if err != nil {
			return ReconcileResult{}, nil, err
		}
		previous, ok := checkedSignedSub(after, existing.delta)
		if !ok {
			return ReconcileResult{}, nil, errors.New("account balance overflow")
		}
		return s.reconcileResultFromEntry(existing, previous, after, true)
	}
	if noop, found, err := getNoopByKey(ctx, tx, key); err != nil {
		return ReconcileResult{}, nil, err
	} else if found {
		if noop.fingerprint != fingerprint {
			return ReconcileResult{}, nil, &IdempotencyConflictError{IdempotencyKey: key, Reason: IdempotencyReasonPayloadMismatch}
		}
		previous, err := contract.FormatSignedAmount(noop.previous)
		if err != nil {
			return ReconcileResult{}, nil, err
		}
		balance, err := contract.FormatSignedAmount(noop.balance)
		if err != nil {
			return ReconcileResult{}, nil, err
		}
		if err := tx.Commit(); err != nil {
			return ReconcileResult{}, nil, err
		}
		return ReconcileResult{PreviousBalance: previous, Adjustment: "0.00", Balance: balance, IdempotentReplay: true}, nil, nil
	}
	if account.active != 1 {
		return ReconcileResult{}, nil, ErrInactive
	}
	current, err := balanceInTx(ctx, tx, account.id, account.openingHundredths)
	if err != nil {
		return ReconcileResult{}, nil, err
	}
	if reported == current {
		if _, err := tx.ExecContext(ctx, `INSERT INTO account_reconcile_noops (idempotency_key, request_fingerprint, account_id, balance_hundredths, previous_balance_hundredths, date, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			key, fingerprint, account.id, reported, current, today, stamp); err != nil {
			if isUniqueViolation(err, "account_reconcile_noops") || isUniqueViolation(err, "account_entries") {
				_ = tx.Rollback()
				return s.replayReconcile(ctx, key, fingerprint)
			}
			return ReconcileResult{}, nil, err
		}
		previous, err := contract.FormatSignedAmount(current)
		if err != nil {
			return ReconcileResult{}, nil, err
		}
		balance, err := contract.FormatSignedAmount(reported)
		if err != nil {
			return ReconcileResult{}, nil, err
		}
		if err := tx.Commit(); err != nil {
			if isUniqueViolation(err, "account_reconcile_noops") || isUniqueViolation(err, "account_entries") {
				return s.replayReconcile(ctx, key, fingerprint)
			}
			return ReconcileResult{}, nil, err
		}
		return ReconcileResult{PreviousBalance: previous, Adjustment: "0.00", Balance: balance}, nil, nil
	}
	delta, ok := checkedSignedSub(reported, current)
	if !ok {
		return ReconcileResult{}, nil, errors.New("account balance overflow")
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO account_entries (account_id, kind, delta_hundredths, date, note, idempotency_key, fingerprint, reversal_of_entry_id, created_at) VALUES (?, 'reconciliation', ?, ?, ?, ?, ?, NULL, ?)`,
		account.id, delta, today, note, key, fingerprint, stamp)
	if err != nil {
		if isUniqueViolation(err, "account_entries") || isUniqueViolation(err, "account_reconcile_noops") {
			_ = tx.Rollback()
			return s.replayReconcile(ctx, key, fingerprint)
		}
		return ReconcileResult{}, nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return ReconcileResult{}, nil, err
	}
	inserted, err := getEntryByID(ctx, tx, id)
	if err != nil {
		return ReconcileResult{}, nil, err
	}
	insertedAfter, err := balanceAfterEntry(ctx, tx, account, inserted)
	if err != nil {
		return ReconcileResult{}, nil, err
	}
	result, _, err := s.reconcileResultFromEntry(inserted, current, insertedAfter, false)
	if err != nil {
		return ReconcileResult{}, nil, err
	}
	if err := tx.Commit(); err != nil {
		if isUniqueViolation(err, "account_entries") || isUniqueViolation(err, "account_reconcile_noops") {
			return s.replayReconcile(ctx, key, fingerprint)
		}
		return ReconcileResult{}, nil, err
	}
	return result, nil, nil
}

func (s *Store) reconcileResultFromEntry(row entryRow, previous, after int64, replay bool) (ReconcileResult, []contract.FieldIssue, error) {
	entry, err := toEntryContract(row, after)
	if err != nil {
		return ReconcileResult{}, nil, err
	}
	previousStr, err := contract.FormatSignedAmount(previous)
	if err != nil {
		return ReconcileResult{}, nil, err
	}
	adjustment, err := contract.FormatSignedAmount(row.delta)
	if err != nil {
		return ReconcileResult{}, nil, err
	}
	balance, err := contract.FormatSignedAmount(after)
	if err != nil {
		return ReconcileResult{}, nil, err
	}
	return ReconcileResult{Entry: &entry, PreviousBalance: previousStr, Adjustment: adjustment, Balance: balance, Changed: true, IdempotentReplay: replay}, nil, nil
}

func (s *Store) replayReconcile(ctx context.Context, key, fingerprint string) (ReconcileResult, []contract.FieldIssue, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return ReconcileResult{}, nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if existing, found, err := getEntryByKey(ctx, tx, key); err != nil {
		return ReconcileResult{}, nil, err
	} else if found {
		if existing.fingerprint != fingerprint {
			return ReconcileResult{}, nil, &IdempotencyConflictError{IdempotencyKey: key, Reason: IdempotencyReasonPayloadMismatch}
		}
		account, err := getByID(ctx, tx, existing.accountID)
		if err != nil {
			return ReconcileResult{}, nil, err
		}
		after, err := balanceAfterEntry(ctx, tx, account, existing)
		if err != nil {
			return ReconcileResult{}, nil, err
		}
		previous, ok := checkedSignedSub(after, existing.delta)
		if !ok {
			return ReconcileResult{}, nil, errors.New("account balance overflow")
		}
		result, _, err := s.reconcileResultFromEntry(existing, previous, after, true)
		if err != nil {
			return ReconcileResult{}, nil, err
		}
		if err := tx.Commit(); err != nil {
			return ReconcileResult{}, nil, err
		}
		return result, nil, nil
	}
	noop, found, err := getNoopByKey(ctx, tx, key)
	if err != nil {
		return ReconcileResult{}, nil, err
	}
	if !found {
		return ReconcileResult{}, nil, fmt.Errorf("idempotency key %q was not found after unique conflict", key)
	}
	if noop.fingerprint != fingerprint {
		return ReconcileResult{}, nil, &IdempotencyConflictError{IdempotencyKey: key, Reason: IdempotencyReasonPayloadMismatch}
	}
	previous, err := contract.FormatSignedAmount(noop.previous)
	if err != nil {
		return ReconcileResult{}, nil, err
	}
	balance, err := contract.FormatSignedAmount(noop.balance)
	if err != nil {
		return ReconcileResult{}, nil, err
	}
	if err := tx.Commit(); err != nil {
		return ReconcileResult{}, nil, err
	}
	return ReconcileResult{PreviousBalance: previous, Adjustment: "0.00", Balance: balance, IdempotentReplay: true}, nil, nil
}

func (s *Store) ListActivity(ctx context.Context, in ListActivityInput) (ListActivityResult, []contract.FieldIssue, error) {
	fields := make([]contract.FieldIssue, 0, 5)
	if in.AccountID < 1 {
		fields = append(fields, contract.FieldIssue{Field: "account_id", Reason: "must be a positive integer"})
	}
	var start, end *string
	if in.StartDate != nil {
		parsed, err := contract.ParseDate(*in.StartDate)
		if err != nil {
			fields = append(fields, contract.FieldIssue{Field: "start_date", Reason: "must be a valid YYYY-MM-DD date"})
		} else {
			start = &parsed
		}
	}
	if in.EndDate != nil {
		parsed, err := contract.ParseDate(*in.EndDate)
		if err != nil {
			fields = append(fields, contract.FieldIssue{Field: "end_date", Reason: "must be a valid YYYY-MM-DD date"})
		} else {
			end = &parsed
		}
	}
	if start != nil && end != nil && *start > *end {
		fields = append(fields, contract.FieldIssue{Field: "end_date", Reason: "must be on or after start_date"})
	}
	var kind *string
	if in.Kind != nil {
		k := strings.TrimSpace(*in.Kind)
		switch k {
		case "deposit", "withdrawal", "reconciliation", "reversal":
			kind = &k
		default:
			fields = append(fields, contract.FieldIssue{Field: "kind", Reason: "must be one of deposit, withdrawal, reconciliation, reversal"})
		}
	}
	limit := ActivityDefaultLimit
	if in.Limit != nil {
		limit = *in.Limit
		if limit < 1 || limit > ActivityMaxLimit {
			fields = append(fields, contract.FieldIssue{Field: "limit", Reason: "must be between 1 and 200"})
		}
	}
	offset := int64(0)
	if in.Offset != nil {
		offset = *in.Offset
		if offset < 0 {
			fields = append(fields, contract.FieldIssue{Field: "offset", Reason: "must be zero or greater"})
		}
	}
	if len(fields) > 0 {
		return ListActivityResult{}, fields, nil
	}
	if s == nil || s.DB == nil {
		return ListActivityResult{}, nil, errors.New("account store database is nil")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return ListActivityResult{}, nil, err
	}
	defer func() { _ = tx.Rollback() }()
	account, err := getByID(ctx, tx, in.AccountID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ListActivityResult{}, nil, &NotFoundError{ID: in.AccountID}
		}
		return ListActivityResult{}, nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+entryColumns+` FROM account_entries AS e INNER JOIN accounts AS a ON a.id = e.account_id WHERE e.account_id = ? ORDER BY e.date ASC, e.created_at ASC, e.id ASC`, account.id)
	if err != nil {
		return ListActivityResult{}, nil, err
	}
	defer func() { _ = rows.Close() }()
	entries := make([]contract.AccountEntry, 0, limit)
	var totalCount int64
	total := account.openingHundredths
	for rows.Next() {
		row, err := scanEntry(rows)
		if err != nil {
			return ListActivityResult{}, nil, err
		}
		next, ok := checkedSignedAdd(total, row.delta)
		if !ok {
			return ListActivityResult{}, nil, errors.New("account balance overflow")
		}
		total = next
		if start != nil && row.date < *start {
			continue
		}
		if end != nil && row.date > *end {
			continue
		}
		if kind != nil && row.kind != *kind {
			continue
		}
		totalCount++
		if totalCount <= offset || int64(len(entries)) == limit {
			continue
		}
		entry, err := toEntryContract(row, total)
		if err != nil {
			return ListActivityResult{}, nil, err
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return ListActivityResult{}, nil, err
	}
	if err := tx.Commit(); err != nil {
		return ListActivityResult{}, nil, err
	}
	return ListActivityResult{Entries: entries, Page: contract.Page{Limit: limit, Offset: offset, Returned: int64(len(entries)), Total: totalCount, HasMore: offset < totalCount-int64(len(entries))}}, nil, nil
}

func (s *Store) ReverseActivity(ctx context.Context, in ReverseInput) (ReverseResult, []contract.FieldIssue, error) {
	fields := make([]contract.FieldIssue, 0, 3)
	if in.EntryID < 1 {
		fields = append(fields, contract.FieldIssue{Field: "id", Reason: "must be a positive integer"})
	}
	var note sql.NullString
	var notePtr *string
	if in.NotePresent {
		if in.Note == nil {
			note = sql.NullString{}
		} else if parsed, ptr, issue := validateActivityNote(*in.Note); issue != nil {
			fields = append(fields, *issue)
		} else {
			note = parsed
			notePtr = ptr
		}
	}
	key, keyIssue := validateActivityKey(in.IdempotencyKey)
	if keyIssue != nil {
		fields = append(fields, *keyIssue)
	}
	if len(fields) > 0 {
		return ReverseResult{}, fields, nil
	}
	now := s.now()
	today := localDate(now)
	stamp := timestamp(now)
	fingerprint, err := fingerprintPayload(struct {
		Op       string  `json:"op"`
		Original int64   `json:"original_entry_id"`
		Note     *string `json:"note"`
	}{Op: "reverse", Original: in.EntryID, Note: notePtr})
	if err != nil {
		return ReverseResult{}, nil, err
	}
	if s == nil || s.DB == nil {
		return ReverseResult{}, nil, errors.New("account store database is nil")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return ReverseResult{}, nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if existing, found, err := getEntryByKey(ctx, tx, key); err != nil {
		return ReverseResult{}, nil, err
	} else if found {
		if existing.fingerprint != fingerprint {
			return ReverseResult{}, nil, &IdempotencyConflictError{IdempotencyKey: key, Reason: IdempotencyReasonPayloadMismatch}
		}
		account, err := getByID(ctx, tx, existing.accountID)
		if err != nil {
			return ReverseResult{}, nil, err
		}
		after, err := balanceAfterEntry(ctx, tx, account, existing)
		if err != nil {
			return ReverseResult{}, nil, err
		}
		entry, err := toEntryContract(existing, after)
		if err != nil {
			return ReverseResult{}, nil, err
		}
		balance, err := contract.FormatSignedAmount(after)
		if err != nil {
			return ReverseResult{}, nil, err
		}
		if err := tx.Commit(); err != nil {
			return ReverseResult{}, nil, err
		}
		return ReverseResult{Entry: entry, Balance: balance, IdempotentReplay: true}, nil, nil
	}
	if _, found, err := getNoopByKey(ctx, tx, key); err != nil {
		return ReverseResult{}, nil, err
	} else if found {
		return ReverseResult{}, nil, &IdempotencyConflictError{IdempotencyKey: key, Reason: IdempotencyReasonPayloadMismatch}
	}
	original, err := getEntryByID(ctx, tx, in.EntryID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ReverseResult{}, nil, &EntryNotFoundError{ID: in.EntryID}
		}
		return ReverseResult{}, nil, err
	}
	if original.kind != "deposit" && original.kind != "withdrawal" && original.kind != "reconciliation" {
		return ReverseResult{}, nil, &EntryNotReversibleError{ID: original.id, Kind: original.kind}
	}
	var existingReversal entryRow
	hasExisting := false
	err = func() error {
		row, err := scanEntry(tx.QueryRowContext(ctx, `SELECT `+entryColumns+` FROM account_entries AS e INNER JOIN accounts AS a ON a.id = e.account_id WHERE e.reversal_of_entry_id = ?`, original.id))
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		existingReversal = row
		hasExisting = true
		return nil
	}()
	if err != nil {
		return ReverseResult{}, nil, err
	}
	if hasExisting {
		if existingReversal.fingerprint == fingerprint {
			account, err := getByID(ctx, tx, existingReversal.accountID)
			if err != nil {
				return ReverseResult{}, nil, err
			}
			after, err := balanceAfterEntry(ctx, tx, account, existingReversal)
			if err != nil {
				return ReverseResult{}, nil, err
			}
			entry, err := toEntryContract(existingReversal, after)
			if err != nil {
				return ReverseResult{}, nil, err
			}
			balance, err := contract.FormatSignedAmount(after)
			if err != nil {
				return ReverseResult{}, nil, err
			}
			if err := tx.Commit(); err != nil {
				return ReverseResult{}, nil, err
			}
			return ReverseResult{Entry: entry, Balance: balance}, nil, nil
		}
		return ReverseResult{}, nil, &EntryNotReversibleError{ID: original.id, Kind: original.kind}
	}
	if original.delta == -1<<63 {
		return ReverseResult{}, nil, errors.New("account balance overflow")
	}
	reversalDelta := -original.delta
	account, err := getByID(ctx, tx, original.accountID)
	if err != nil {
		return ReverseResult{}, nil, err
	}
	current, err := balanceInTx(ctx, tx, account.id, account.openingHundredths)
	if err != nil {
		return ReverseResult{}, nil, err
	}
	after, ok := checkedSignedAdd(current, reversalDelta)
	if !ok {
		return ReverseResult{}, nil, errors.New("account balance overflow")
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO account_entries (account_id, kind, delta_hundredths, date, note, idempotency_key, fingerprint, reversal_of_entry_id, created_at) VALUES (?, 'reversal', ?, ?, ?, ?, ?, ?, ?)`,
		account.id, reversalDelta, today, note, key, fingerprint, original.id, stamp)
	if err != nil {
		if isUniqueViolation(err, "account_entries") {
			_ = tx.Rollback()
			return s.replayReverse(ctx, key, fingerprint, original.id)
		}
		return ReverseResult{}, nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return ReverseResult{}, nil, err
	}
	inserted, err := getEntryByID(ctx, tx, id)
	if err != nil {
		return ReverseResult{}, nil, err
	}
	entry, err := toEntryContract(inserted, after)
	if err != nil {
		return ReverseResult{}, nil, err
	}
	balance, err := contract.FormatSignedAmount(after)
	if err != nil {
		return ReverseResult{}, nil, err
	}
	if err := tx.Commit(); err != nil {
		if isUniqueViolation(err, "account_entries") {
			return s.replayReverse(ctx, key, fingerprint, original.id)
		}
		return ReverseResult{}, nil, err
	}
	return ReverseResult{Entry: entry, Balance: balance, Changed: true}, nil, nil
}

func (s *Store) replayReverse(ctx context.Context, key, fingerprint string, originalID int64) (ReverseResult, []contract.FieldIssue, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return ReverseResult{}, nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if existing, found, err := getEntryByKey(ctx, tx, key); err != nil {
		return ReverseResult{}, nil, err
	} else if found {
		if existing.fingerprint != fingerprint {
			return ReverseResult{}, nil, &IdempotencyConflictError{IdempotencyKey: key, Reason: IdempotencyReasonPayloadMismatch}
		}
		account, err := getByID(ctx, tx, existing.accountID)
		if err != nil {
			return ReverseResult{}, nil, err
		}
		after, err := balanceAfterEntry(ctx, tx, account, existing)
		if err != nil {
			return ReverseResult{}, nil, err
		}
		entry, err := toEntryContract(existing, after)
		if err != nil {
			return ReverseResult{}, nil, err
		}
		balance, err := contract.FormatSignedAmount(after)
		if err != nil {
			return ReverseResult{}, nil, err
		}
		if err := tx.Commit(); err != nil {
			return ReverseResult{}, nil, err
		}
		return ReverseResult{Entry: entry, Balance: balance, IdempotentReplay: true}, nil, nil
	}
	row, err := scanEntry(tx.QueryRowContext(ctx, `SELECT `+entryColumns+` FROM account_entries AS e INNER JOIN accounts AS a ON a.id = e.account_id WHERE e.reversal_of_entry_id = ?`, originalID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ReverseResult{}, nil, fmt.Errorf("idempotency key %q was not found after unique conflict", key)
		}
		return ReverseResult{}, nil, err
	}
	if row.fingerprint != fingerprint {
		return ReverseResult{}, nil, &EntryNotReversibleError{ID: originalID}
	}
	account, err := getByID(ctx, tx, row.accountID)
	if err != nil {
		return ReverseResult{}, nil, err
	}
	after, err := balanceAfterEntry(ctx, tx, account, row)
	if err != nil {
		return ReverseResult{}, nil, err
	}
	entry, err := toEntryContract(row, after)
	if err != nil {
		return ReverseResult{}, nil, err
	}
	balance, err := contract.FormatSignedAmount(after)
	if err != nil {
		return ReverseResult{}, nil, err
	}
	if err := tx.Commit(); err != nil {
		return ReverseResult{}, nil, err
	}
	return ReverseResult{Entry: entry, Balance: balance, IdempotentReplay: true}, nil, nil
}
