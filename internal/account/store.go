// Package account owns local asset account identity and lifecycle.
package account

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
	ErrNotFound       = errors.New("account not found")
	ErrAlreadyExists  = errors.New("account already exists")
	ErrInactive       = errors.New("account inactive")
	ErrBalanceNotZero = errors.New("account balance must be zero")
)

type AlreadyExistsError struct {
	Account contract.Account
}

func (e *AlreadyExistsError) Error() string {
	if e == nil {
		return ErrAlreadyExists.Error()
	}
	return fmt.Sprintf("account %q already exists", e.Account.Name)
}

func (e *AlreadyExistsError) Is(target error) bool { return target == ErrAlreadyExists }

type NotFoundError struct {
	ID int64
}

func (e *NotFoundError) Error() string        { return fmt.Sprintf("account %d was not found", e.ID) }
func (e *NotFoundError) Is(target error) bool { return target == ErrNotFound }

type BalanceNotZeroError struct {
	Account contract.Account
}

func (e *BalanceNotZeroError) Error() string        { return ErrBalanceNotZero.Error() }
func (e *BalanceNotZeroError) Is(target error) bool { return target == ErrBalanceNotZero }

type Store struct {
	DB  *sql.DB
	Now func() time.Time
}

func (s *Store) now() time.Time {
	if s != nil && s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func timestamp(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }

func normalizeName(name string) string { return contract.TrimASCIIWhitespace(name) }

func normalizeType(t string) string { return strings.TrimSpace(t) }

func validType(t string) bool {
	switch t {
	case "checking", "savings", "cash", "other":
		return true
	default:
		return false
	}
}

type CreateInput struct {
	Name           string
	Type           string
	OpeningBalance string
	Note           *string
	NotePresent    bool
}

type CreateResult struct {
	Account     contract.Account
	Created     bool
	Reactivated bool
}

type UpdateInput struct {
	ID          int64
	Name        *string
	NameNull    bool
	Note        *string
	NotePresent bool
}

type UpdateResult struct {
	Account contract.Account
	Changed bool
}

type ListInput struct {
	Name            *string
	Type            *string
	IncludeInactive bool
}

type DisableResult struct {
	Account contract.Account
	Changed bool
}

func validateNameField(field, value string) (string, *contract.FieldIssue) {
	normalized := normalizeName(value)
	switch {
	case normalized == "":
		return "", &contract.FieldIssue{Field: field, Reason: "must not be empty"}
	case strings.ContainsRune(normalized, '\x00'):
		return "", &contract.FieldIssue{Field: field, Reason: "must not contain NUL characters"}
	default:
		return normalized, nil
	}
}

func validateNoteValue(value string) (sql.NullString, *contract.FieldIssue) {
	note := contract.TrimASCIIWhitespace(value)
	if strings.ContainsRune(note, '\x00') {
		return sql.NullString{}, &contract.FieldIssue{Field: "note", Reason: "must not contain NUL characters"}
	}
	if note == "" {
		return sql.NullString{}, nil
	}
	return sql.NullString{String: note, Valid: true}, nil
}

func (s *Store) Create(ctx context.Context, in CreateInput) (CreateResult, []contract.FieldIssue, error) {
	fields := make([]contract.FieldIssue, 0, 4)
	name, nameIssue := validateNameField("name", in.Name)
	if nameIssue != nil {
		fields = append(fields, *nameIssue)
	}
	accountType := normalizeType(in.Type)
	if accountType == "" {
		fields = append(fields, contract.FieldIssue{Field: "type", Reason: "must not be empty"})
	} else if !validType(accountType) {
		fields = append(fields, contract.FieldIssue{Field: "type", Reason: "must be one of checking, savings, cash, other"})
	}
	openingHundredths, openingIssue := validateOpeningBalance(in.OpeningBalance)
	if openingIssue != nil {
		fields = append(fields, *openingIssue)
	}
	var note sql.NullString
	if in.NotePresent {
		if in.Note == nil {
			note = sql.NullString{}
		} else {
			parsed, issue := validateNoteValue(*in.Note)
			if issue != nil {
				fields = append(fields, *issue)
			} else {
				note = parsed
			}
		}
	}
	if len(fields) > 0 {
		return CreateResult{}, fields, nil
	}
	if s == nil || s.DB == nil {
		return CreateResult{}, nil, errors.New("account store database is nil")
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return CreateResult{}, nil, err
	}
	defer func() { _ = tx.Rollback() }()

	existing, found, err := lookupByName(ctx, tx, name)
	if err != nil {
		return CreateResult{}, nil, err
	}
	if found {
		foundAccount, err := toContract(existing)
		if err != nil {
			return CreateResult{}, nil, err
		}
		if foundAccount.Active {
			return CreateResult{}, nil, &AlreadyExistsError{Account: foundAccount}
		}
		if existing.accountType != accountType {
			return CreateResult{}, []contract.FieldIssue{{Field: "type", Reason: "must match the existing account type"}}, nil
		}
		if existing.openingHundredths != openingHundredths {
			return CreateResult{}, []contract.FieldIssue{{Field: "opening_balance", Reason: "must match the existing opening balance"}}, nil
		}
		stamp := timestamp(s.now())
		updates := []string{"active = 1", "updated_at = ?"}
		args := []any{stamp}
		if in.NotePresent {
			updates = append(updates, "note = ?")
			args = append(args, note)
		}
		args = append(args, existing.id)
		if _, err := tx.ExecContext(ctx, `UPDATE accounts SET `+strings.Join(updates, ", ")+` WHERE id = ?`, args...); err != nil {
			return CreateResult{}, nil, err
		}
		reactivated, err := getByID(ctx, tx, existing.id)
		if err != nil {
			return CreateResult{}, nil, err
		}
		account, err := toContract(reactivated)
		if err != nil {
			return CreateResult{}, nil, err
		}
		if err := tx.Commit(); err != nil {
			return CreateResult{}, nil, err
		}
		return CreateResult{Account: account, Created: false, Reactivated: true}, nil, nil
	}

	stamp := timestamp(s.now())
	res, err := tx.ExecContext(ctx, `INSERT INTO accounts (name, type, opening_balance_hundredths, note, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		name, accountType, openingHundredths, note, stamp, stamp)
	if err != nil {
		return CreateResult{}, nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return CreateResult{}, nil, err
	}
	created, err := getByID(ctx, tx, id)
	if err != nil {
		return CreateResult{}, nil, err
	}
	account, err := toContract(created)
	if err != nil {
		return CreateResult{}, nil, err
	}
	if err := tx.Commit(); err != nil {
		return CreateResult{}, nil, err
	}
	return CreateResult{Account: account, Created: true}, nil, nil
}

func validateOpeningBalance(value string) (int64, *contract.FieldIssue) {
	if value == "" {
		return 0, &contract.FieldIssue{Field: "opening_balance", Reason: "must not be empty"}
	}
	parsed, err := contract.ParseSignedAmount(value)
	if err != nil {
		if strings.Contains(value, "\x00") {
			return 0, &contract.FieldIssue{Field: "opening_balance", Reason: "must not contain NUL characters"}
		}
		return 0, &contract.FieldIssue{Field: "opening_balance", Reason: "must be a decimal with at most two places"}
	}
	return parsed, nil
}

func (s *Store) Update(ctx context.Context, in UpdateInput) (UpdateResult, []contract.FieldIssue, error) {
	fields := make([]contract.FieldIssue, 0, 2)
	if in.ID < 1 {
		fields = append(fields, contract.FieldIssue{Field: "id", Reason: "must be a positive integer"})
	}
	if in.Name == nil && !in.NameNull && !in.NotePresent {
		fields = append(fields, contract.FieldIssue{Field: "id", Reason: "at least one of name or note must be supplied"})
	}
	var newName *string
	if in.NameNull {
		fields = append(fields, contract.FieldIssue{Field: "name", Reason: "must not be null"})
	} else if in.Name != nil {
		normalized, issue := validateNameField("name", *in.Name)
		if issue != nil {
			fields = append(fields, *issue)
		} else {
			newName = &normalized
		}
	}
	var noteSet bool
	var note sql.NullString
	if in.NotePresent {
		noteSet = true
		if in.Note == nil {
			note = sql.NullString{}
		} else {
			parsed, issue := validateNoteValue(*in.Note)
			if issue != nil {
				fields = append(fields, *issue)
			} else {
				note = parsed
			}
		}
	}
	if len(fields) > 0 {
		return UpdateResult{}, fields, nil
	}
	if s == nil || s.DB == nil {
		return UpdateResult{}, nil, errors.New("account store database is nil")
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return UpdateResult{}, nil, err
	}
	defer func() { _ = tx.Rollback() }()

	existing, err := getByID(ctx, tx, in.ID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return UpdateResult{}, nil, &NotFoundError{ID: in.ID}
		}
		return UpdateResult{}, nil, err
	}
	existingAccount, err := toContract(existing)
	if err != nil {
		return UpdateResult{}, nil, err
	}

	desiredName := existing.name
	if newName != nil {
		desiredName = *newName
	}
	desiredNote := existing.note
	if noteSet {
		desiredNote = note
	}

	if desiredName == existing.name && sqlNullEqual(desiredNote, existing.note) {
		if err := tx.Commit(); err != nil {
			return UpdateResult{}, nil, err
		}
		return UpdateResult{Account: existingAccount, Changed: false}, nil, nil
	}

	if newName != nil {
		conflict, found, err := lookupByName(ctx, tx, *newName)
		if err != nil {
			return UpdateResult{}, nil, err
		}
		if found && conflict.id != existing.id {
			conflictAccount, err := toContract(conflict)
			if err != nil {
				return UpdateResult{}, nil, err
			}
			return UpdateResult{}, nil, &AlreadyExistsError{Account: conflictAccount}
		}
	}

	if _, err := tx.ExecContext(ctx, `UPDATE accounts SET name = ?, note = ?, updated_at = ? WHERE id = ?`,
		desiredName, desiredNote, timestamp(s.now()), existing.id); err != nil {
		return UpdateResult{}, nil, err
	}
	updated, err := getByID(ctx, tx, existing.id)
	if err != nil {
		return UpdateResult{}, nil, err
	}
	account, err := toContract(updated)
	if err != nil {
		return UpdateResult{}, nil, err
	}
	if err := tx.Commit(); err != nil {
		return UpdateResult{}, nil, err
	}
	return UpdateResult{Account: account, Changed: true}, nil, nil
}

func (s *Store) List(ctx context.Context, in ListInput) ([]contract.Account, []contract.FieldIssue, error) {
	fields := make([]contract.FieldIssue, 0, 2)
	var nameFilter *string
	if in.Name != nil {
		normalized, issue := validateNameField("name", *in.Name)
		if issue != nil {
			fields = append(fields, *issue)
		} else {
			nameFilter = &normalized
		}
	}
	var typeFilter *string
	if in.Type != nil {
		t := normalizeType(*in.Type)
		if t == "" {
			fields = append(fields, contract.FieldIssue{Field: "type", Reason: "must not be empty"})
		} else if !validType(t) {
			fields = append(fields, contract.FieldIssue{Field: "type", Reason: "must be one of checking, savings, cash, other"})
		} else {
			typeFilter = &t
		}
	}
	if len(fields) > 0 {
		return nil, fields, nil
	}
	if s == nil || s.DB == nil {
		return nil, nil, errors.New("account store database is nil")
	}
	query := `SELECT id, name, type, opening_balance_hundredths, active, note, created_at, updated_at FROM accounts`
	conds := []string{}
	args := []any{}
	if nameFilter != nil {
		conds = append(conds, "name = ? COLLATE NOCASE")
		args = append(args, *nameFilter)
	}
	if typeFilter != nil {
		conds = append(conds, "type = ?")
		args = append(args, *typeFilter)
	}
	if !in.IncludeInactive {
		conds = append(conds, "active = 1")
	}
	if len(conds) > 0 {
		query += " WHERE " + strings.Join(conds, " AND ")
	}
	query += " ORDER BY active DESC, name COLLATE NOCASE ASC, id ASC"
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	accounts := make([]contract.Account, 0)
	for rows.Next() {
		row, err := scanRow(rows)
		if err != nil {
			return nil, nil, err
		}
		account, err := toContract(row)
		if err != nil {
			return nil, nil, err
		}
		accounts = append(accounts, account)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return accounts, nil, nil
}

func (s *Store) Disable(ctx context.Context, id int64) (DisableResult, []contract.FieldIssue, error) {
	if id < 1 {
		return DisableResult{}, []contract.FieldIssue{{Field: "id", Reason: "must be a positive integer"}}, nil
	}
	if s == nil || s.DB == nil {
		return DisableResult{}, nil, errors.New("account store database is nil")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return DisableResult{}, nil, err
	}
	defer func() { _ = tx.Rollback() }()

	existing, err := getByID(ctx, tx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return DisableResult{}, nil, &NotFoundError{ID: id}
		}
		return DisableResult{}, nil, err
	}
	account, err := toContract(existing)
	if err != nil {
		return DisableResult{}, nil, err
	}
	if !account.Active {
		if err := tx.Commit(); err != nil {
			return DisableResult{}, nil, err
		}
		return DisableResult{Account: account, Changed: false}, nil, nil
	}
	if existing.openingHundredths != 0 {
		return DisableResult{}, nil, &BalanceNotZeroError{Account: account}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE accounts SET active = 0, updated_at = ? WHERE id = ?`, timestamp(s.now()), id); err != nil {
		return DisableResult{}, nil, err
	}
	disabled, err := getByID(ctx, tx, id)
	if err != nil {
		return DisableResult{}, nil, err
	}
	disabledAccount, err := toContract(disabled)
	if err != nil {
		return DisableResult{}, nil, err
	}
	if err := tx.Commit(); err != nil {
		return DisableResult{}, nil, err
	}
	return DisableResult{Account: disabledAccount, Changed: true}, nil, nil
}

type accountRow struct {
	id                int64
	name              string
	accountType       string
	openingHundredths int64
	active            int
	note              sql.NullString
	createdAt         string
	updatedAt         string
}

func scanRow(scanner interface{ Scan(dest ...any) error }) (accountRow, error) {
	var row accountRow
	err := scanner.Scan(&row.id, &row.name, &row.accountType, &row.openingHundredths, &row.active, &row.note, &row.createdAt, &row.updatedAt)
	return row, err
}

func toContract(row accountRow) (contract.Account, error) {
	opening, err := contract.FormatSignedAmount(row.openingHundredths)
	if err != nil {
		return contract.Account{}, err
	}
	current := opening
	var note *string
	if row.note.Valid {
		value := row.note.String
		note = &value
	}
	return contract.Account{
		ID:             row.id,
		Name:           row.name,
		Type:           row.accountType,
		OpeningBalance: opening,
		CurrentBalance: current,
		Active:         row.active == 1,
		Note:           note,
		CreatedAt:      row.createdAt,
		UpdatedAt:      row.updatedAt,
	}, nil
}

type queryer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func lookupByName(ctx context.Context, q queryer, name string) (accountRow, bool, error) {
	row, err := scanRow(q.QueryRowContext(ctx, `SELECT id, name, type, opening_balance_hundredths, active, note, created_at, updated_at FROM accounts WHERE name = ? COLLATE NOCASE`, name))
	if errors.Is(err, sql.ErrNoRows) {
		return accountRow{}, false, nil
	}
	if err != nil {
		return accountRow{}, false, err
	}
	return row, true, nil
}

func getByID(ctx context.Context, q queryer, id int64) (accountRow, error) {
	return scanRow(q.QueryRowContext(ctx, `SELECT id, name, type, opening_balance_hundredths, active, note, created_at, updated_at FROM accounts WHERE id = ?`, id))
}

func sqlNullEqual(a, b sql.NullString) bool {
	if !a.Valid && !b.Valid {
		return true
	}
	return a.Valid && b.Valid && a.String == b.String
}
