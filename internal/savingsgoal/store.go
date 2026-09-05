package savingsgoal

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/jordanp2002/Local-Ledger/internal/contract"
)

type Store struct {
	DB  *sql.DB
	Now func() time.Time
}

type CreateInput struct {
	Name         string
	AccountID    int64
	TargetAmount string
	TargetDate   *string
	Note         *string
}

type UpdateInput struct {
	ID                int64
	Name              *string
	NameNull          bool
	TargetAmount      *string
	TargetAmountNull  bool
	TargetDate        *string
	TargetDatePresent bool
	Note              *string
	NotePresent       bool
	AccountID         *int64
	AccountIDNull     bool
}

type UpdateResult struct {
	Goal    contract.SavingsGoal
	Changed bool
}

type ListInput struct {
	Name          *string
	AccountID     *int64
	Status        *string
	IncludeClosed bool
}

type NotFoundError struct {
	ID int64
}

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("savings goal %d not found", e.ID)
}

type AlreadyExistsError struct {
	Name string
}

func (e *AlreadyExistsError) Error() string {
	return fmt.Sprintf("savings goal %q already exists", e.Name)
}

type ClosedError struct {
	ID     int64
	Status string
}

func (e *ClosedError) Error() string {
	return fmt.Sprintf("savings goal %d is %s", e.ID, e.Status)
}

type HasAllocationsError struct {
	ID            int64
	CurrentAmount string
}

func (e *HasAllocationsError) Error() string {
	return fmt.Sprintf("savings goal %d has allocations", e.ID)
}

type AccountNotFoundError struct {
	ID int64
}

func (e *AccountNotFoundError) Error() string {
	return fmt.Sprintf("account %d not found", e.ID)
}

var ErrAccountInactive = errors.New("account is inactive")

func (s *Store) now() time.Time {
	if s != nil && s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func timestamp(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000Z")
}

func AccountHasActiveGoalsOrAllocations(ctx context.Context, tx *sql.Tx, accountID int64) (bool, error) {
	query := `
		SELECT 1
		FROM savings_goals g
		WHERE g.account_id = ?
		  AND (
		      g.status = 'active'
		      OR (
		          g.status = 'completed'
		          AND (
		              SELECT COALESCE(SUM(e.delta_hundredths), 0)
		              FROM savings_goal_entries e
		              WHERE e.goal_id = g.id
		          ) > 0
		      )
		  )
		LIMIT 1
	`
	var found int
	err := tx.QueryRowContext(ctx, query, accountID).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) Create(ctx context.Context, in CreateInput) (contract.SavingsGoal, []contract.FieldIssue, error) {
	if s == nil || s.DB == nil {
		return contract.SavingsGoal{}, nil, errors.New("savings goal store database is nil")
	}

	currentTime := s.now()
	currentDate := currentTime.Format("2006-01-02")
	stamp := timestamp(currentTime)

	var issues []contract.FieldIssue
	name := contract.TrimASCIIWhitespace(in.Name)
	if name == "" {
		issues = append(issues, contract.FieldIssue{Field: "name", Reason: "must not be empty"})
	}

	if in.AccountID < 1 {
		issues = append(issues, contract.FieldIssue{Field: "account_id", Reason: "must be a positive integer"})
	}

	amountHundredths, err := contract.ParseAmount(in.TargetAmount)
	if err != nil {
		issues = append(issues, contract.FieldIssue{Field: "target_amount", Reason: "must be a positive amount with at most two decimal places"})
	} else if amountHundredths <= 0 {
		issues = append(issues, contract.FieldIssue{Field: "target_amount", Reason: "must be greater than zero"})
	}

	var targetDate *string
	if in.TargetDate != nil {
		d := strings.TrimSpace(*in.TargetDate)
		if d == "" {
			issues = append(issues, contract.FieldIssue{Field: "target_date", Reason: "must be a valid YYYY-MM-DD date"})
		} else {
			parsedDate, err := contract.ParseDate(d)
			if err != nil {
				issues = append(issues, contract.FieldIssue{Field: "target_date", Reason: "must be a valid YYYY-MM-DD date"})
			} else if parsedDate < currentDate {
				issues = append(issues, contract.FieldIssue{Field: "target_date", Reason: "cannot precede current date"})
			} else {
				targetDate = &parsedDate
			}
		}
	}

	var note *string
	if in.Note != nil {
		n := contract.TrimASCIIWhitespace(*in.Note)
		if n != "" {
			note = &n
		}
	}

	if len(issues) > 0 {
		return contract.SavingsGoal{}, issues, nil
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return contract.SavingsGoal{}, nil, err
	}
	defer func() { _ = tx.Rollback() }()

	var accountName string
	var accountActive int
	err = tx.QueryRowContext(ctx, "SELECT name, active FROM accounts WHERE id = ?", in.AccountID).Scan(&accountName, &accountActive)
	if errors.Is(err, sql.ErrNoRows) {
		return contract.SavingsGoal{}, nil, &AccountNotFoundError{ID: in.AccountID}
	}
	if err != nil {
		return contract.SavingsGoal{}, nil, err
	}
	if accountActive != 1 {
		return contract.SavingsGoal{}, nil, ErrAccountInactive
	}

	var existingID int64
	err = tx.QueryRowContext(ctx, "SELECT id FROM savings_goals WHERE name = ? COLLATE NOCASE LIMIT 1", name).Scan(&existingID)
	if err == nil {
		return contract.SavingsGoal{}, nil, &AlreadyExistsError{Name: name}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return contract.SavingsGoal{}, nil, err
	}

	res, err := tx.ExecContext(ctx, `
		INSERT INTO savings_goals (name, account_id, target_amount_hundredths, target_date, note, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, 'active', ?, ?)
	`, name, in.AccountID, amountHundredths, targetDate, note, stamp, stamp)
	if err != nil {
		return contract.SavingsGoal{}, nil, err
	}

	id, err := res.LastInsertId()
	if err != nil {
		return contract.SavingsGoal{}, nil, err
	}

	if err := tx.Commit(); err != nil {
		return contract.SavingsGoal{}, nil, err
	}

	goal, err := buildGoal(goalValues{
		id: id, name: name, accountID: in.AccountID, account: accountName,
		targetAmount: amountHundredths, targetDate: targetDate, status: "active",
		note: note, createdAt: stamp, updatedAt: stamp,
	}, 0)
	return goal, nil, err
}

func (s *Store) Update(ctx context.Context, in UpdateInput) (UpdateResult, []contract.FieldIssue, error) {
	if in.ID < 1 {
		return UpdateResult{}, []contract.FieldIssue{{Field: "id", Reason: "must be a positive integer"}}, nil
	}

	currentTime := s.now()
	currentDate := currentTime.Format("2006-01-02")
	stamp := timestamp(currentTime)

	var issues []contract.FieldIssue
	if !in.NameNull && in.Name == nil && !in.TargetAmountNull && in.TargetAmount == nil &&
		!in.AccountIDNull && in.AccountID == nil && !in.TargetDatePresent && !in.NotePresent {
		return UpdateResult{}, []contract.FieldIssue{{Field: "id", Reason: "at least one mutable field must be supplied"}}, nil
	}

	var desiredName *string
	if in.NameNull {
		issues = append(issues, contract.FieldIssue{Field: "name", Reason: "must not be null"})
	} else if in.Name != nil {
		n := contract.TrimASCIIWhitespace(*in.Name)
		if n == "" {
			issues = append(issues, contract.FieldIssue{Field: "name", Reason: "must not be empty"})
		} else {
			desiredName = &n
		}
	}

	var desiredTargetAmount *int64
	if in.TargetAmountNull {
		issues = append(issues, contract.FieldIssue{Field: "target_amount", Reason: "must not be null"})
	} else if in.TargetAmount != nil {
		amount, err := contract.ParseAmount(*in.TargetAmount)
		if err != nil {
			issues = append(issues, contract.FieldIssue{Field: "target_amount", Reason: "must be a positive amount with at most two decimal places"})
		} else if amount <= 0 {
			issues = append(issues, contract.FieldIssue{Field: "target_amount", Reason: "must be greater than zero"})
		} else {
			desiredTargetAmount = &amount
		}
	}

	var desiredAccountID *int64
	if in.AccountIDNull {
		issues = append(issues, contract.FieldIssue{Field: "account_id", Reason: "must not be null"})
	} else if in.AccountID != nil {
		if *in.AccountID < 1 {
			issues = append(issues, contract.FieldIssue{Field: "account_id", Reason: "must be a positive integer"})
		} else {
			desiredAccountID = in.AccountID
		}
	}

	var desiredTargetDate *string
	if in.TargetDatePresent {
		if in.TargetDate != nil {
			d := strings.TrimSpace(*in.TargetDate)
			if d == "" {
				issues = append(issues, contract.FieldIssue{Field: "target_date", Reason: "must be a valid YYYY-MM-DD date"})
			} else {
				parsedDate, err := contract.ParseDate(d)
				if err != nil {
					issues = append(issues, contract.FieldIssue{Field: "target_date", Reason: "must be a valid YYYY-MM-DD date"})
				} else if parsedDate < currentDate {
					issues = append(issues, contract.FieldIssue{Field: "target_date", Reason: "cannot precede current date"})
				} else {
					desiredTargetDate = &parsedDate
				}
			}
		}
	}

	var desiredNote *string
	if in.NotePresent {
		if in.Note != nil {
			n := contract.TrimASCIIWhitespace(*in.Note)
			if n != "" {
				desiredNote = &n
			}
		}
	}

	if len(issues) > 0 {
		return UpdateResult{}, issues, nil
	}

	if s == nil || s.DB == nil {
		return UpdateResult{}, nil, errors.New("savings goal store database is nil")
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return UpdateResult{}, nil, err
	}
	defer func() { _ = tx.Rollback() }()

	var (
		id                     int64
		existingName           string
		existingAccountID      int64
		accountName            string
		targetAmountHundredths int64
		existingTargetDate     sql.NullString
		existingNote           sql.NullString
		status                 string
		completedAt            sql.NullString
		cancelledAt            sql.NullString
		createdAt              string
		updatedAt              string
	)
	err = tx.QueryRowContext(ctx, `
		SELECT g.id, g.name, g.account_id, a.name, g.target_amount_hundredths, g.target_date, g.note,
		       g.status, g.completed_at, g.cancelled_at, g.created_at, g.updated_at
		FROM savings_goals g
		JOIN accounts a ON a.id = g.account_id
		WHERE g.id = ?
	`, in.ID).Scan(
		&id, &existingName, &existingAccountID, &accountName, &targetAmountHundredths,
		&existingTargetDate, &existingNote, &status, &completedAt, &cancelledAt,
		&createdAt, &updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return UpdateResult{}, nil, &NotFoundError{ID: in.ID}
	}
	if err != nil {
		return UpdateResult{}, nil, err
	}

	if status != "active" {
		return UpdateResult{}, nil, &ClosedError{ID: id, Status: status}
	}

	var currentAmountHundredths int64
	err = tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(delta_hundredths), 0) FROM savings_goal_entries WHERE goal_id = ?`, id).Scan(&currentAmountHundredths)
	if err != nil {
		return UpdateResult{}, nil, err
	}

	finalAccountID := existingAccountID
	finalAccountName := accountName
	if desiredAccountID != nil && *desiredAccountID != existingAccountID {
		if currentAmountHundredths != 0 {
			formattedCurrent, _ := contract.FormatAmount(currentAmountHundredths)
			return UpdateResult{}, nil, &HasAllocationsError{ID: id, CurrentAmount: formattedCurrent}
		}
		var newAccountName string
		var newAccountActive int
		err = tx.QueryRowContext(ctx, `SELECT name, active FROM accounts WHERE id = ?`, *desiredAccountID).Scan(&newAccountName, &newAccountActive)
		if errors.Is(err, sql.ErrNoRows) {
			return UpdateResult{}, nil, &AccountNotFoundError{ID: *desiredAccountID}
		}
		if err != nil {
			return UpdateResult{}, nil, err
		}
		if newAccountActive != 1 {
			return UpdateResult{}, nil, ErrAccountInactive
		}
		finalAccountID = *desiredAccountID
		finalAccountName = newAccountName
	}

	finalName := existingName
	if desiredName != nil && !strings.EqualFold(*desiredName, existingName) {
		var duplicateID int64
		err = tx.QueryRowContext(ctx, `SELECT id FROM savings_goals WHERE name = ? COLLATE NOCASE AND id != ? LIMIT 1`, *desiredName, id).Scan(&duplicateID)
		if err == nil {
			return UpdateResult{}, nil, &AlreadyExistsError{Name: *desiredName}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return UpdateResult{}, nil, err
		}
		finalName = *desiredName
	} else if desiredName != nil {
		finalName = *desiredName
	}

	finalTargetAmount := targetAmountHundredths
	if desiredTargetAmount != nil {
		finalTargetAmount = *desiredTargetAmount
	}

	finalTargetDate := nullableStringValue(existingTargetDate)
	if in.TargetDatePresent {
		finalTargetDate = desiredTargetDate
	}

	finalNote := nullableStringValue(existingNote)
	if in.NotePresent {
		finalNote = desiredNote
	}

	changed := finalName != existingName ||
		finalAccountID != existingAccountID ||
		finalTargetAmount != targetAmountHundredths ||
		!equalStringPointers(finalTargetDate, nullableStringValue(existingTargetDate)) ||
		!equalStringPointers(finalNote, nullableStringValue(existingNote))

	finalUpdatedAt := updatedAt
	if changed {
		finalUpdatedAt = stamp
		_, err = tx.ExecContext(ctx, `
			UPDATE savings_goals
			SET name = ?, account_id = ?, target_amount_hundredths = ?, target_date = ?, note = ?, updated_at = ?
			WHERE id = ?
		`, finalName, finalAccountID, finalTargetAmount, finalTargetDate, finalNote, finalUpdatedAt, id)
		if err != nil {
			return UpdateResult{}, nil, err
		}
	}

	if err := tx.Commit(); err != nil {
		return UpdateResult{}, nil, err
	}

	goal, err := buildGoal(goalValues{
		id: id, name: finalName, accountID: finalAccountID, account: finalAccountName,
		targetAmount: finalTargetAmount, targetDate: finalTargetDate, status: status,
		note: finalNote, createdAt: createdAt, updatedAt: finalUpdatedAt,
		completedAt: nullableStringValue(completedAt), cancelledAt: nullableStringValue(cancelledAt),
	}, currentAmountHundredths)
	if err != nil {
		return UpdateResult{}, nil, err
	}

	return UpdateResult{Goal: goal, Changed: changed}, nil, nil
}

func (s *Store) List(ctx context.Context, in ListInput) ([]contract.SavingsGoal, []contract.FieldIssue, error) {
	var issues []contract.FieldIssue
	var nameFilter *string
	if in.Name != nil {
		n := contract.TrimASCIIWhitespace(*in.Name)
		if n == "" {
			issues = append(issues, contract.FieldIssue{Field: "name", Reason: "must not be empty"})
		} else {
			nameFilter = &n
		}
	}

	if in.AccountID != nil && *in.AccountID < 1 {
		issues = append(issues, contract.FieldIssue{Field: "account_id", Reason: "must be a positive integer"})
	}

	var statusFilter *string
	if in.Status != nil {
		st := strings.ToLower(strings.TrimSpace(*in.Status))
		if st != "active" && st != "completed" && st != "cancelled" {
			issues = append(issues, contract.FieldIssue{Field: "status", Reason: "must be one of active, completed, cancelled"})
		} else {
			statusFilter = &st
		}
	}

	if len(issues) > 0 {
		return nil, issues, nil
	}

	if s == nil || s.DB == nil {
		return nil, nil, errors.New("savings goal store database is nil")
	}

	var conds []string
	var args []any

	if nameFilter != nil {
		conds = append(conds, "g.name = ? COLLATE NOCASE")
		args = append(args, *nameFilter)
	}

	if in.AccountID != nil {
		conds = append(conds, "g.account_id = ?")
		args = append(args, *in.AccountID)
	}

	if statusFilter != nil {
		conds = append(conds, "g.status = ?")
		args = append(args, *statusFilter)
	} else if !in.IncludeClosed {
		conds = append(conds, "g.status = 'active'")
	}

	whereClause := ""
	if len(conds) > 0 {
		whereClause = "WHERE " + strings.Join(conds, " AND ")
	}

	query := goalSelect + whereClause + " GROUP BY g.id " + goalOrder

	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	goals := make([]contract.SavingsGoal, 0)
	for rows.Next() {
		row, err := scanGoal(rows)
		if err != nil {
			return nil, nil, err
		}
		goal, err := buildGoal(row.values, row.current)
		if err != nil {
			return nil, nil, err
		}

		goals = append(goals, goal)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	return goals, nil, nil
}

func calculateProgress(targetAmountHundredths, currentAmountHundredths int64) (string, string, bool, *string, error) {
	if currentAmountHundredths >= targetAmountHundredths {
		rem := "0.00"
		pct := "100.00"
		reached := true
		var amountAbove *string
		if currentAmountHundredths > targetAmountHundredths {
			diff := currentAmountHundredths - targetAmountHundredths
			formatted, err := contract.FormatAmount(diff)
			if err != nil {
				return "", "", false, nil, err
			}
			amountAbove = &formatted
		}
		return rem, pct, reached, amountAbove, nil
	}

	remainingHundredths, ok := checkedSubtract(targetAmountHundredths, currentAmountHundredths)
	if !ok {
		return "", "", false, nil, errors.New("progress calculation overflow")
	}
	remFormatted, err := contract.FormatAmount(remainingHundredths)
	if err != nil {
		return "", "", false, nil, err
	}

	if currentAmountHundredths <= 0 {
		return remFormatted, "0.00", false, nil, nil
	}

	product, ok := checkedMultiply(currentAmountHundredths, 10000)
	if !ok {
		return "", "", false, nil, errors.New("progress calculation overflow")
	}

	pctHundredths := product / targetAmountHundredths
	if pctHundredths > 10000 {
		pctHundredths = 10000
	}
	pctFormatted, err := contract.FormatAmount(pctHundredths)
	if err != nil {
		return "", "", false, nil, err
	}

	return remFormatted, pctFormatted, false, nil, nil
}

type goalValues struct {
	id, accountID, targetAmount int64
	name, account, status       string
	targetDate, note            *string
	createdAt, updatedAt        string
	completedAt, cancelledAt    *string
}

func buildGoal(values goalValues, currentAmount int64) (contract.SavingsGoal, error) {
	target, err := contract.FormatAmount(values.targetAmount)
	if err != nil {
		return contract.SavingsGoal{}, err
	}
	current, err := contract.FormatSignedAmount(currentAmount)
	if err != nil {
		return contract.SavingsGoal{}, err
	}
	remaining, percent, reached, above, err := calculateProgress(values.targetAmount, currentAmount)
	if err != nil {
		return contract.SavingsGoal{}, err
	}
	return contract.SavingsGoal{
		ID: values.id, Name: values.name, AccountID: values.accountID, Account: values.account,
		TargetAmount: target, TargetDate: values.targetDate, CurrentAmount: current,
		RemainingAmount: remaining, AmountAboveTarget: above, ProgressPercent: percent,
		TargetReached: reached, Status: values.status, Note: values.note,
		CreatedAt: values.createdAt, UpdatedAt: values.updatedAt,
		CompletedAt: values.completedAt, CancelledAt: values.cancelledAt,
	}, nil
}

func checkedSubtract(left, right int64) (int64, bool) {
	if right > 0 && left < math.MinInt64+right {
		return 0, false
	}
	if right < 0 && left > math.MaxInt64+right {
		return 0, false
	}
	return left - right, true
}

func checkedMultiply(left, right int64) (int64, bool) {
	if left < 0 || right < 0 {
		return 0, false
	}
	if left != 0 && right > math.MaxInt64/left {
		return 0, false
	}
	return left * right, true
}

func nullableStringValue(v sql.NullString) *string {
	if !v.Valid {
		return nil
	}
	return &v.String
}

func equalStringPointers(a, b *string) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}
