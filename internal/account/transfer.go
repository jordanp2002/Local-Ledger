package account

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/jordanp2002/Local-Ledger/internal/contract"
)

var (
	ErrTransferNotFound           = errors.New("account transfer not found")
	ErrTransferAlreadyReversed    = errors.New("account transfer already reversed")
	ErrTransferDependencyConflict = errors.New("account transfer has a dependency")
)

type TransferNotFoundError struct{ ID int64 }

func (e *TransferNotFoundError) Error() string {
	if e == nil {
		return ErrTransferNotFound.Error()
	}
	return fmt.Sprintf("account transfer %d was not found", e.ID)
}

func (e *TransferNotFoundError) Is(target error) bool { return target == ErrTransferNotFound }

type TransferAlreadyReversedError struct{ ID int64 }

func (e *TransferAlreadyReversedError) Error() string {
	if e == nil {
		return ErrTransferAlreadyReversed.Error()
	}
	return fmt.Sprintf("account transfer %d has already been reversed", e.ID)
}

func (e *TransferAlreadyReversedError) Is(target error) bool {
	return target == ErrTransferAlreadyReversed
}

type TransferDependencyConflictError struct{ ID int64 }

func (e *TransferDependencyConflictError) Error() string {
	if e == nil {
		return ErrTransferDependencyConflict.Error()
	}
	return fmt.Sprintf("account transfer %d has an active dependency", e.ID)
}

func (e *TransferDependencyConflictError) Is(target error) bool {
	return target == ErrTransferDependencyConflict
}

type TransferInput struct {
	SourceAccountID      int64
	DestinationAccountID int64
	Amount               string
	Date                 string
	Note                 *string
	NotePresent          bool
	IdempotencyKey       string
}

type TransferResult struct {
	Transfer           contract.AccountTransfer
	SourceBalance      string
	DestinationBalance string
	Changed            bool
	IdempotentReplay   bool
}

type ListTransfersInput struct {
	AccountID            *int64
	SourceAccountID      *int64
	DestinationAccountID *int64
	StartDate            *string
	EndDate              *string
	Status               *string
	Limit                *int64
	Offset               *int64
}

type ListTransfersResult struct {
	Transfers []contract.AccountTransfer
	Page      contract.Page
}

type ReverseTransferInput struct {
	TransferID     int64
	Note           *string
	NotePresent    bool
	IdempotencyKey string
}

// TransferInTxInput is the canonical form used by a caller-owned transaction.
// AmountHundredths, Date, Note, and IdempotencyKey must already be normalized.
// Fingerprint may be omitted and is derived from those fields. Timestamp is
// supplied once so every transfer and entry row can share the caller's clock.
type TransferInTxInput struct {
	SourceAccountID      int64
	DestinationAccountID int64
	AmountHundredths     int64
	Date                 string
	Note                 *string
	IdempotencyKey       string
	Fingerprint          string
	Timestamp            string
}

// ReverseTransferInTxInput is the canonical form used by a caller-owned
// transaction for an inverse transfer. The caller may provide a custom
// fingerprint when a larger operation namespaces its child request.
type ReverseTransferInTxInput struct {
	TransferID     int64
	Note           *string
	Date           string
	IdempotencyKey string
	Fingerprint    string
	Timestamp      string
}

type transferRow struct {
	id                   int64
	sourceAccountID      int64
	sourceAccount        string
	destinationAccountID int64
	destinationAccount   string
	amount               int64
	date                 string
	note                 sql.NullString
	idempotencyKey       string
	fingerprint          string
	reversalOfTransferID sql.NullInt64
	status               string
	createdAt            string
	updatedAt            string
}

const transferColumns = `t.id, t.source_account_id, source.name, t.destination_account_id, destination.name, t.amount_hundredths, t.date, t.note, t.idempotency_key, t.fingerprint, t.reversal_of_transfer_id, t.status, t.created_at, t.updated_at`

func scanTransfer(row interface{ Scan(dest ...any) error }) (transferRow, error) {
	var transfer transferRow
	err := row.Scan(
		&transfer.id,
		&transfer.sourceAccountID,
		&transfer.sourceAccount,
		&transfer.destinationAccountID,
		&transfer.destinationAccount,
		&transfer.amount,
		&transfer.date,
		&transfer.note,
		&transfer.idempotencyKey,
		&transfer.fingerprint,
		&transfer.reversalOfTransferID,
		&transfer.status,
		&transfer.createdAt,
		&transfer.updatedAt,
	)
	return transfer, err
}

func getTransferByID(ctx context.Context, q queryer, id int64) (transferRow, error) {
	return scanTransfer(q.QueryRowContext(ctx, `SELECT `+transferColumns+` FROM account_transfers AS t INNER JOIN accounts AS source ON source.id = t.source_account_id INNER JOIN accounts AS destination ON destination.id = t.destination_account_id WHERE t.id = ?`, id))
}

func getTransferByKey(ctx context.Context, q queryer, key string) (transferRow, bool, error) {
	row, err := scanTransfer(q.QueryRowContext(ctx, `SELECT `+transferColumns+` FROM account_transfers AS t INNER JOIN accounts AS source ON source.id = t.source_account_id INNER JOIN accounts AS destination ON destination.id = t.destination_account_id WHERE t.idempotency_key = ?`, key))
	if errors.Is(err, sql.ErrNoRows) {
		return transferRow{}, false, nil
	}
	if err != nil {
		return transferRow{}, false, err
	}
	return row, true, nil
}

func transferToContract(row transferRow) (contract.AccountTransfer, error) {
	amount, err := contract.FormatAmount(row.amount)
	if err != nil {
		return contract.AccountTransfer{}, err
	}
	var note *string
	if row.note.Valid {
		value := row.note.String
		note = &value
	}
	var reversal *int64
	if row.reversalOfTransferID.Valid {
		value := row.reversalOfTransferID.Int64
		reversal = &value
	}
	return contract.AccountTransfer{
		ID:                   row.id,
		SourceAccountID:      row.sourceAccountID,
		SourceAccount:        row.sourceAccount,
		DestinationAccountID: row.destinationAccountID,
		DestinationAccount:   row.destinationAccount,
		Amount:               amount,
		Date:                 row.date,
		Note:                 note,
		ReversalOfTransferID: reversal,
		Status:               row.status,
		CreatedAt:            row.createdAt,
		UpdatedAt:            row.updatedAt,
	}, nil
}

func transferFingerprint(sourceID, destinationID, amount int64, date string, note *string) (string, error) {
	return fingerprintPayload(struct {
		Op          string  `json:"op"`
		Source      int64   `json:"source_account_id"`
		Destination int64   `json:"destination_account_id"`
		Amount      int64   `json:"amount_hundredths"`
		Date        string  `json:"date"`
		Note        *string `json:"note"`
	}{Op: "transfer", Source: sourceID, Destination: destinationID, Amount: amount, Date: date, Note: note})
}

func reverseTransferFingerprint(transferID int64, note *string) (string, error) {
	return fingerprintPayload(struct {
		Op       string  `json:"op"`
		Original int64   `json:"original_transfer_id"`
		Note     *string `json:"note"`
	}{Op: "reverse_transfer", Original: transferID, Note: note})
}

func transferEntryFingerprint(transferID int64, side string) (string, error) {
	return fingerprintPayload(struct {
		Op         string `json:"op"`
		TransferID int64  `json:"transfer_id"`
		Side       string `json:"side"`
	}{Op: "transfer_entry", TransferID: transferID, Side: side})
}

func formatTransferBalances(ctx context.Context, tx *sql.Tx, transfer transferRow, entries []entryRow) (string, string, error) {
	if len(entries) != 2 {
		return "", "", errors.New("account transfer must have exactly two entries")
	}
	var sourceEntry, destinationEntry entryRow
	for _, entry := range entries {
		switch {
		case entry.kind == "transfer_out" && entry.accountID == transfer.sourceAccountID && entry.delta == -transfer.amount:
			sourceEntry = entry
		case entry.kind == "transfer_in" && entry.accountID == transfer.destinationAccountID && entry.delta == transfer.amount:
			destinationEntry = entry
		default:
			return "", "", errors.New("account transfer entries do not match transfer")
		}
	}
	if sourceEntry.id == 0 || destinationEntry.id == 0 || sourceEntry.id == destinationEntry.id {
		return "", "", errors.New("account transfer must have one outgoing and one incoming entry")
	}
	source, err := getByID(ctx, tx, transfer.sourceAccountID)
	if err != nil {
		return "", "", err
	}
	destination, err := getByID(ctx, tx, transfer.destinationAccountID)
	if err != nil {
		return "", "", err
	}
	sourceAfter, err := balanceAfterEntry(ctx, tx, source, sourceEntry)
	if err != nil {
		return "", "", err
	}
	destinationAfter, err := balanceAfterEntry(ctx, tx, destination, destinationEntry)
	if err != nil {
		return "", "", err
	}
	sourceBalance, err := contract.FormatSignedAmount(sourceAfter)
	if err != nil {
		return "", "", err
	}
	destinationBalance, err := contract.FormatSignedAmount(destinationAfter)
	if err != nil {
		return "", "", err
	}
	return sourceBalance, destinationBalance, nil
}

func loadTransferEntries(ctx context.Context, tx *sql.Tx, transferID int64) ([]entryRow, error) {
	rows, err := tx.QueryContext(ctx, `SELECT `+entryColumns+` FROM account_entries AS e INNER JOIN accounts AS a ON a.id = e.account_id WHERE e.transfer_id = ? ORDER BY e.id ASC`, transferID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	entries := make([]entryRow, 0, 2)
	for rows.Next() {
		entry, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}

func transferResultFromRow(ctx context.Context, tx *sql.Tx, row transferRow, replay bool, changed bool) (TransferResult, error) {
	entries, err := loadTransferEntries(ctx, tx, row.id)
	if err != nil {
		return TransferResult{}, err
	}
	sourceBalance, destinationBalance, err := formatTransferBalances(ctx, tx, row, entries)
	if err != nil {
		return TransferResult{}, err
	}
	transfer, err := transferToContract(row)
	if err != nil {
		return TransferResult{}, err
	}
	return TransferResult{
		Transfer:           transfer,
		SourceBalance:      sourceBalance,
		DestinationBalance: destinationBalance,
		Changed:            changed,
		IdempotentReplay:   replay,
	}, nil
}

func (s *Store) validateTransferInput(in TransferInput) (TransferInTxInput, []contract.FieldIssue) {
	fields := make([]contract.FieldIssue, 0, 6)
	if in.SourceAccountID < 1 {
		fields = append(fields, contract.FieldIssue{Field: "source_account_id", Reason: "must be a positive integer"})
	}
	if in.DestinationAccountID < 1 {
		fields = append(fields, contract.FieldIssue{Field: "destination_account_id", Reason: "must be a positive integer"})
	}
	if in.SourceAccountID > 0 && in.SourceAccountID == in.DestinationAccountID {
		fields = append(fields, contract.FieldIssue{Field: "destination_account_id", Reason: "must differ from source_account_id"})
	}
	amount, amountIssue := validatePositiveAmount("amount", in.Amount)
	if amountIssue != nil {
		fields = append(fields, *amountIssue)
	}
	now := s.now()
	date := ""
	if in.Date == "" {
		fields = append(fields, contract.FieldIssue{Field: "date", Reason: "must not be empty"})
	} else if parsed, issue := validateActivityDate(in.Date, localDate(now)); issue != nil {
		fields = append(fields, *issue)
	} else {
		date = parsed
	}
	var note *string
	if in.NotePresent {
		if in.Note == nil {
			// An explicit null note is the same canonical payload as an omitted
			// note, while still allowing the adapter to distinguish presence.
		} else if _, parsed, issue := validateActivityNote(*in.Note); issue != nil {
			fields = append(fields, *issue)
		} else {
			note = parsed
		}
	}
	key, keyIssue := validateActivityKey(in.IdempotencyKey)
	if keyIssue != nil {
		fields = append(fields, *keyIssue)
	}
	validated := TransferInTxInput{
		SourceAccountID:      in.SourceAccountID,
		DestinationAccountID: in.DestinationAccountID,
		AmountHundredths:     amount,
		Date:                 date,
		Note:                 note,
		IdempotencyKey:       key,
		Timestamp:            timestamp(now),
	}
	if len(fields) == 0 {
		validated.Fingerprint, _ = transferFingerprint(validated.SourceAccountID, validated.DestinationAccountID, validated.AmountHundredths, validated.Date, validated.Note)
	}
	return validated, fields
}

func (s *Store) validateReverseTransferInput(in ReverseTransferInput) (ReverseTransferInTxInput, []contract.FieldIssue) {
	fields := make([]contract.FieldIssue, 0, 3)
	if in.TransferID < 1 {
		fields = append(fields, contract.FieldIssue{Field: "id", Reason: "must be a positive integer"})
	}
	var note *string
	if in.NotePresent {
		if in.Note == nil {
			// Explicit null clears the optional note.
		} else if _, parsed, issue := validateActivityNote(*in.Note); issue != nil {
			fields = append(fields, *issue)
		} else {
			note = parsed
		}
	}
	key, keyIssue := validateActivityKey(in.IdempotencyKey)
	if keyIssue != nil {
		fields = append(fields, *keyIssue)
	}
	now := s.now()
	validated := ReverseTransferInTxInput{
		TransferID:     in.TransferID,
		Note:           note,
		Date:           localDate(now),
		IdempotencyKey: key,
		Timestamp:      timestamp(now),
	}
	if len(fields) == 0 {
		validated.Fingerprint, _ = reverseTransferFingerprint(validated.TransferID, validated.Note)
	}
	return validated, fields
}

func validateTransferInTxInput(in TransferInTxInput) error {
	if in.SourceAccountID < 1 || in.DestinationAccountID < 1 || in.SourceAccountID == in.DestinationAccountID {
		return errors.New("transfer accounts must be positive and different")
	}
	if in.AmountHundredths < 1 {
		return errors.New("transfer amount must be positive")
	}
	if _, err := contract.ParseDate(in.Date); err != nil {
		return errors.New("transfer date must be canonical")
	}
	if strings.TrimSpace(in.IdempotencyKey) == "" || in.IdempotencyKey != strings.TrimSpace(in.IdempotencyKey) {
		return errors.New("transfer idempotency key must be non-empty and trimmed")
	}
	if in.Timestamp == "" {
		return errors.New("transfer timestamp must be supplied")
	}
	return nil
}

func insertTransfer(ctx context.Context, tx *sql.Tx, in TransferInTxInput, reversalOf *int64) (int64, error) {
	result, err := tx.ExecContext(ctx, `
		INSERT INTO account_transfers (
			source_account_id, destination_account_id, amount_hundredths,
			date, note, idempotency_key, fingerprint, reversal_of_transfer_id,
			status, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'recorded', ?, ?)
	`, in.SourceAccountID, in.DestinationAccountID, in.AmountHundredths, in.Date, in.Note, in.IdempotencyKey, in.Fingerprint, reversalOf, in.Timestamp, in.Timestamp)
	if err != nil {
		return 0, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	if id < 1 {
		return 0, errors.New("account transfer insert returned an invalid id")
	}
	return id, nil
}

func insertTransferEntries(ctx context.Context, tx *sql.Tx, transferID int64, sourceID, destinationID, amount int64, date string, note *string, createdAt string) error {
	outFingerprint, err := transferEntryFingerprint(transferID, "out")
	if err != nil {
		return err
	}
	inFingerprint, err := transferEntryFingerprint(transferID, "in")
	if err != nil {
		return err
	}
	outKey := fmt.Sprintf("account-transfer:%d:out", transferID)
	inKey := fmt.Sprintf("account-transfer:%d:in", transferID)
	query := `INSERT INTO account_entries (account_id, kind, delta_hundredths, date, note, idempotency_key, fingerprint, reversal_of_entry_id, transfer_id, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, NULL, ?, ?)`
	if _, err := tx.ExecContext(ctx, query, sourceID, "transfer_out", -amount, date, note, outKey, outFingerprint, transferID, createdAt); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, query, destinationID, "transfer_in", amount, date, note, inKey, inFingerprint, transferID, createdAt)
	return err
}

// TransferInTx records one transfer using an existing caller-owned SQL
// transaction. It never begins, commits, or rolls back that transaction.
func TransferInTx(ctx context.Context, tx *sql.Tx, in TransferInTxInput) (TransferResult, error) {
	if tx == nil {
		return TransferResult{}, errors.New("account transfer SQL transaction is nil")
	}
	if err := validateTransferInTxInput(in); err != nil {
		return TransferResult{}, err
	}
	if in.Fingerprint == "" {
		fingerprint, err := transferFingerprint(in.SourceAccountID, in.DestinationAccountID, in.AmountHundredths, in.Date, in.Note)
		if err != nil {
			return TransferResult{}, err
		}
		in.Fingerprint = fingerprint
	}
	if existing, found, err := getTransferByKey(ctx, tx, in.IdempotencyKey); err != nil {
		return TransferResult{}, err
	} else if found {
		if existing.fingerprint != in.Fingerprint {
			return TransferResult{}, &IdempotencyConflictError{IdempotencyKey: in.IdempotencyKey, Reason: IdempotencyReasonPayloadMismatch}
		}
		return transferResultFromRow(ctx, tx, existing, true, false)
	}
	if _, found, err := getEntryByKey(ctx, tx, in.IdempotencyKey); err != nil {
		return TransferResult{}, err
	} else if found {
		return TransferResult{}, &IdempotencyConflictError{IdempotencyKey: in.IdempotencyKey, Reason: IdempotencyReasonPayloadMismatch}
	}
	if _, found, err := getNoopByKey(ctx, tx, in.IdempotencyKey); err != nil {
		return TransferResult{}, err
	} else if found {
		return TransferResult{}, &IdempotencyConflictError{IdempotencyKey: in.IdempotencyKey, Reason: IdempotencyReasonPayloadMismatch}
	}
	source, err := getByID(ctx, tx, in.SourceAccountID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return TransferResult{}, &NotFoundError{ID: in.SourceAccountID}
		}
		return TransferResult{}, err
	}
	destination, err := getByID(ctx, tx, in.DestinationAccountID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return TransferResult{}, &NotFoundError{ID: in.DestinationAccountID}
		}
		return TransferResult{}, err
	}
	if source.active != 1 {
		return TransferResult{}, ErrInactive
	}
	if destination.active != 1 {
		return TransferResult{}, ErrInactive
	}
	sourceCurrent, err := balanceInTx(ctx, tx, source.id, source.openingHundredths)
	if err != nil {
		return TransferResult{}, err
	}
	destinationCurrent, err := balanceInTx(ctx, tx, destination.id, destination.openingHundredths)
	if err != nil {
		return TransferResult{}, err
	}
	if _, ok := checkedSignedSub(sourceCurrent, in.AmountHundredths); !ok {
		return TransferResult{}, errors.New("account balance overflow")
	}
	if _, ok := checkedSignedAdd(destinationCurrent, in.AmountHundredths); !ok {
		return TransferResult{}, errors.New("account balance overflow")
	}
	transferID, err := insertTransfer(ctx, tx, in, nil)
	if err != nil {
		return TransferResult{}, err
	}
	if err := insertTransferEntries(ctx, tx, transferID, source.id, destination.id, in.AmountHundredths, in.Date, in.Note, in.Timestamp); err != nil {
		return TransferResult{}, err
	}
	transfer, err := getTransferByID(ctx, tx, transferID)
	if err != nil {
		return TransferResult{}, err
	}
	return transferResultFromRow(ctx, tx, transfer, false, true)
}

func (s *Store) TransferBetweenAccounts(ctx context.Context, in TransferInput) (TransferResult, []contract.FieldIssue, error) {
	validated, fields := s.validateTransferInput(in)
	if len(fields) > 0 {
		return TransferResult{}, fields, nil
	}
	if s == nil || s.DB == nil {
		return TransferResult{}, nil, errors.New("account store database is nil")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return TransferResult{}, nil, err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := TransferInTx(ctx, tx, validated)
	if err != nil && isUniqueViolation(err, "account_transfers") {
		_ = tx.Rollback()
		return s.replayTransfer(ctx, validated.IdempotencyKey, validated.Fingerprint)
	}
	if err != nil {
		return TransferResult{}, nil, err
	}
	if err := tx.Commit(); err != nil {
		if isUniqueViolation(err, "account_transfers") {
			return s.replayTransfer(ctx, validated.IdempotencyKey, validated.Fingerprint)
		}
		return TransferResult{}, nil, err
	}
	return result, nil, nil
}

func (s *Store) replayTransfer(ctx context.Context, key, fingerprint string) (TransferResult, []contract.FieldIssue, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return TransferResult{}, nil, err
	}
	defer func() { _ = tx.Rollback() }()
	row, found, err := getTransferByKey(ctx, tx, key)
	if err != nil {
		return TransferResult{}, nil, err
	}
	if !found {
		return TransferResult{}, nil, fmt.Errorf("idempotency key %q was not found after unique conflict", key)
	}
	if row.fingerprint != fingerprint {
		return TransferResult{}, nil, &IdempotencyConflictError{IdempotencyKey: key, Reason: IdempotencyReasonPayloadMismatch}
	}
	result, err := transferResultFromRow(ctx, tx, row, true, false)
	if err != nil {
		return TransferResult{}, nil, err
	}
	if err := tx.Commit(); err != nil {
		return TransferResult{}, nil, err
	}
	return result, nil, nil
}

func validateReverseTransferInTxInput(in ReverseTransferInTxInput) error {
	if in.TransferID < 1 {
		return errors.New("transfer id must be positive")
	}
	if _, err := contract.ParseDate(in.Date); err != nil {
		return errors.New("transfer reversal date must be canonical")
	}
	if strings.TrimSpace(in.IdempotencyKey) == "" || in.IdempotencyKey != strings.TrimSpace(in.IdempotencyKey) {
		return errors.New("transfer reversal idempotency key must be non-empty and trimmed")
	}
	if in.Timestamp == "" {
		return errors.New("transfer reversal timestamp must be supplied")
	}
	return nil
}

func getTransferReversal(ctx context.Context, tx *sql.Tx, transferID int64) (transferRow, bool, error) {
	row, err := scanTransfer(tx.QueryRowContext(ctx, `SELECT `+transferColumns+` FROM account_transfers AS t INNER JOIN accounts AS source ON source.id = t.source_account_id INNER JOIN accounts AS destination ON destination.id = t.destination_account_id WHERE t.reversal_of_transfer_id = ?`, transferID))
	if errors.Is(err, sql.ErrNoRows) {
		return transferRow{}, false, nil
	}
	if err != nil {
		return transferRow{}, false, err
	}
	return row, true, nil
}

// ReverseTransferInTx records an inverse transfer and marks its original
// transfer reversed inside a caller-owned transaction. It deliberately does
// not inspect domain-specific dependencies; a dependent module can release
// its own records and call this seam in the same transaction.
func ReverseTransferInTx(ctx context.Context, tx *sql.Tx, in ReverseTransferInTxInput) (TransferResult, error) {
	if tx == nil {
		return TransferResult{}, errors.New("account transfer SQL transaction is nil")
	}
	if err := validateReverseTransferInTxInput(in); err != nil {
		return TransferResult{}, err
	}
	if in.Fingerprint == "" {
		fingerprint, err := reverseTransferFingerprint(in.TransferID, in.Note)
		if err != nil {
			return TransferResult{}, err
		}
		in.Fingerprint = fingerprint
	}
	if existing, found, err := getTransferByKey(ctx, tx, in.IdempotencyKey); err != nil {
		return TransferResult{}, err
	} else if found {
		if existing.fingerprint != in.Fingerprint {
			return TransferResult{}, &IdempotencyConflictError{IdempotencyKey: in.IdempotencyKey, Reason: IdempotencyReasonPayloadMismatch}
		}
		return transferResultFromRow(ctx, tx, existing, true, false)
	}
	if _, found, err := getEntryByKey(ctx, tx, in.IdempotencyKey); err != nil {
		return TransferResult{}, err
	} else if found {
		return TransferResult{}, &IdempotencyConflictError{IdempotencyKey: in.IdempotencyKey, Reason: IdempotencyReasonPayloadMismatch}
	}
	if _, found, err := getNoopByKey(ctx, tx, in.IdempotencyKey); err != nil {
		return TransferResult{}, err
	} else if found {
		return TransferResult{}, &IdempotencyConflictError{IdempotencyKey: in.IdempotencyKey, Reason: IdempotencyReasonPayloadMismatch}
	}
	original, err := getTransferByID(ctx, tx, in.TransferID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return TransferResult{}, &TransferNotFoundError{ID: in.TransferID}
		}
		return TransferResult{}, err
	}
	if original.reversalOfTransferID.Valid || original.status == "reversed" {
		if original.status == "reversed" {
			reversal, found, err := getTransferReversal(ctx, tx, original.id)
			if err != nil {
				return TransferResult{}, err
			}
			if found && reversal.fingerprint == in.Fingerprint {
				return transferResultFromRow(ctx, tx, reversal, false, false)
			}
		}
		return TransferResult{}, &TransferAlreadyReversedError{ID: original.id}
	}
	if original.amount < 1 {
		return TransferResult{}, errors.New("account transfer amount is invalid")
	}
	source, err := getByID(ctx, tx, original.destinationAccountID)
	if err != nil {
		return TransferResult{}, err
	}
	destination, err := getByID(ctx, tx, original.sourceAccountID)
	if err != nil {
		return TransferResult{}, err
	}
	sourceCurrent, err := balanceInTx(ctx, tx, source.id, source.openingHundredths)
	if err != nil {
		return TransferResult{}, err
	}
	destinationCurrent, err := balanceInTx(ctx, tx, destination.id, destination.openingHundredths)
	if err != nil {
		return TransferResult{}, err
	}
	if _, ok := checkedSignedSub(sourceCurrent, original.amount); !ok {
		return TransferResult{}, errors.New("account balance overflow")
	}
	if _, ok := checkedSignedAdd(destinationCurrent, original.amount); !ok {
		return TransferResult{}, errors.New("account balance overflow")
	}
	input := TransferInTxInput{
		SourceAccountID:      original.destinationAccountID,
		DestinationAccountID: original.sourceAccountID,
		AmountHundredths:     original.amount,
		Date:                 in.Date,
		Note:                 in.Note,
		IdempotencyKey:       in.IdempotencyKey,
		Fingerprint:          in.Fingerprint,
		Timestamp:            in.Timestamp,
	}
	transferID, err := insertTransfer(ctx, tx, input, &original.id)
	if err != nil {
		return TransferResult{}, err
	}
	if err := insertTransferEntries(ctx, tx, transferID, input.SourceAccountID, input.DestinationAccountID, input.AmountHundredths, input.Date, input.Note, input.Timestamp); err != nil {
		return TransferResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE account_transfers SET status = 'reversed', updated_at = ? WHERE id = ?`, in.Timestamp, original.id); err != nil {
		return TransferResult{}, err
	}
	reversal, err := getTransferByID(ctx, tx, transferID)
	if err != nil {
		return TransferResult{}, err
	}
	return transferResultFromRow(ctx, tx, reversal, false, true)
}

func (s *Store) ReverseAccountTransfer(ctx context.Context, in ReverseTransferInput) (TransferResult, []contract.FieldIssue, error) {
	validated, fields := s.validateReverseTransferInput(in)
	if len(fields) > 0 {
		return TransferResult{}, fields, nil
	}
	if s == nil || s.DB == nil {
		return TransferResult{}, nil, errors.New("account store database is nil")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return TransferResult{}, nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var dependency int
	err = tx.QueryRowContext(ctx, `SELECT 1 FROM savings_goal_entries WHERE transfer_id = ? LIMIT 1`, validated.TransferID).Scan(&dependency)
	if err == nil {
		return TransferResult{}, nil, &TransferDependencyConflictError{ID: validated.TransferID}
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return TransferResult{}, nil, err
	}
	result, err := ReverseTransferInTx(ctx, tx, validated)
	if err != nil && isUniqueViolation(err, "account_transfers") {
		_ = tx.Rollback()
		return s.replayReverseTransfer(ctx, validated.IdempotencyKey, validated.Fingerprint)
	}
	if err != nil {
		return TransferResult{}, nil, err
	}
	if err := tx.Commit(); err != nil {
		if isUniqueViolation(err, "account_transfers") {
			return s.replayReverseTransfer(ctx, validated.IdempotencyKey, validated.Fingerprint)
		}
		return TransferResult{}, nil, err
	}
	return result, nil, nil
}

func (s *Store) replayReverseTransfer(ctx context.Context, key, fingerprint string) (TransferResult, []contract.FieldIssue, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return TransferResult{}, nil, err
	}
	defer func() { _ = tx.Rollback() }()
	row, found, err := getTransferByKey(ctx, tx, key)
	if err != nil {
		return TransferResult{}, nil, err
	}
	if !found {
		return TransferResult{}, nil, fmt.Errorf("idempotency key %q was not found after unique conflict", key)
	}
	if row.fingerprint != fingerprint {
		return TransferResult{}, nil, &IdempotencyConflictError{IdempotencyKey: key, Reason: IdempotencyReasonPayloadMismatch}
	}
	result, err := transferResultFromRow(ctx, tx, row, true, false)
	if err != nil {
		return TransferResult{}, nil, err
	}
	if err := tx.Commit(); err != nil {
		return TransferResult{}, nil, err
	}
	return result, nil, nil
}

const (
	TransferDefaultLimit int64 = 50
	TransferMaxLimit     int64 = 200
)

type normalizedTransferList struct {
	accountID            *int64
	sourceAccountID      *int64
	destinationAccountID *int64
	startDate            *string
	endDate              *string
	status               *string
	limit                int64
	offset               int64
}

func validateTransferListInput(in ListTransfersInput) (normalizedTransferList, []contract.FieldIssue) {
	fields := make([]contract.FieldIssue, 0, 8)
	validateID := func(field string, value *int64) *int64 {
		if value == nil {
			return nil
		}
		if *value < 1 {
			fields = append(fields, contract.FieldIssue{Field: field, Reason: "must be a positive integer"})
			return nil
		}
		return value
	}
	accountID := validateID("account_id", in.AccountID)
	sourceID := validateID("source_account_id", in.SourceAccountID)
	destinationID := validateID("destination_account_id", in.DestinationAccountID)
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
	var status *string
	if in.Status != nil {
		value := strings.TrimSpace(*in.Status)
		if value != "recorded" && value != "reversed" {
			fields = append(fields, contract.FieldIssue{Field: "status", Reason: "must be one of recorded, reversed"})
		} else {
			status = &value
		}
	}
	limit := TransferDefaultLimit
	if in.Limit != nil {
		limit = *in.Limit
		if limit < 1 || limit > TransferMaxLimit {
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
	return normalizedTransferList{
		accountID: accountID, sourceAccountID: sourceID, destinationAccountID: destinationID,
		startDate: start, endDate: end, status: status, limit: limit, offset: offset,
	}, fields
}

func (s *Store) ListTransfers(ctx context.Context, in ListTransfersInput) (ListTransfersResult, []contract.FieldIssue, error) {
	filters, fields := validateTransferListInput(in)
	if len(fields) > 0 {
		return ListTransfersResult{}, fields, nil
	}
	if s == nil || s.DB == nil {
		return ListTransfersResult{}, nil, errors.New("account store database is nil")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return ListTransfersResult{}, nil, err
	}
	defer func() { _ = tx.Rollback() }()
	query := `SELECT ` + transferColumns + ` FROM account_transfers AS t INNER JOIN accounts AS source ON source.id = t.source_account_id INNER JOIN accounts AS destination ON destination.id = t.destination_account_id`
	countQuery := `SELECT count(*) FROM account_transfers AS t`
	conditions := make([]string, 0, 6)
	args := make([]any, 0, 6)
	if filters.accountID != nil {
		conditions = append(conditions, "(t.source_account_id = ? OR t.destination_account_id = ?)")
		args = append(args, *filters.accountID, *filters.accountID)
	}
	if filters.sourceAccountID != nil {
		conditions = append(conditions, "t.source_account_id = ?")
		args = append(args, *filters.sourceAccountID)
	}
	if filters.destinationAccountID != nil {
		conditions = append(conditions, "t.destination_account_id = ?")
		args = append(args, *filters.destinationAccountID)
	}
	if filters.startDate != nil {
		conditions = append(conditions, "t.date >= ?")
		args = append(args, *filters.startDate)
	}
	if filters.endDate != nil {
		conditions = append(conditions, "t.date <= ?")
		args = append(args, *filters.endDate)
	}
	if filters.status != nil {
		conditions = append(conditions, "t.status = ?")
		args = append(args, *filters.status)
	}
	if len(conditions) > 0 {
		where := " WHERE " + strings.Join(conditions, " AND ")
		query += where
		countQuery += where
	}
	var total int64
	if err := tx.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return ListTransfersResult{}, nil, err
	}
	query += " ORDER BY t.date DESC, t.created_at DESC, t.id DESC LIMIT ? OFFSET ?"
	listArgs := append(append([]any{}, args...), filters.limit, filters.offset)
	rows, err := tx.QueryContext(ctx, query, listArgs...)
	if err != nil {
		return ListTransfersResult{}, nil, err
	}
	defer func() { _ = rows.Close() }()
	transfers := make([]contract.AccountTransfer, 0, filters.limit)
	for rows.Next() {
		row, err := scanTransfer(rows)
		if err != nil {
			return ListTransfersResult{}, nil, err
		}
		transfer, err := transferToContract(row)
		if err != nil {
			return ListTransfersResult{}, nil, err
		}
		transfers = append(transfers, transfer)
	}
	if err := rows.Err(); err != nil {
		return ListTransfersResult{}, nil, err
	}
	if err := tx.Commit(); err != nil {
		return ListTransfersResult{}, nil, err
	}
	return ListTransfersResult{
		Transfers: transfers,
		Page: contract.Page{
			Limit:    filters.limit,
			Offset:   filters.offset,
			Returned: int64(len(transfers)),
			Total:    total,
			HasMore:  filters.offset < total && int64(len(transfers)) < total-filters.offset,
		},
	}, nil, nil
}
