package savingsgoal

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"

	"github.com/jordanp2002/Local-Ledger/internal/account"
	"github.com/jordanp2002/Local-Ledger/internal/contract"
)

type AllocationInput struct {
	GoalID         int64
	Amount, Date   string
	Note           *string
	IdempotencyKey string
}

type FundingInput struct {
	GoalID, SourceAccountID int64
	Amount, Date            string
	Note                    *string
	IdempotencyKey          string
}

type ReverseFundingInput struct {
	EntryID        int64
	Note           *string
	IdempotencyKey string
}

type GoalMutationResult struct {
	Goal             contract.SavingsGoal              `json:"goal"`
	Account          contract.SavingsAccountAllocation `json:"account"`
	Changed          bool                              `json:"changed"`
	IdempotentReplay bool                              `json:"idempotent_replay"`
}

type FundingResult struct {
	GoalMutationResult
	Transfer           contract.AccountTransfer `json:"transfer"`
	SourceBalance      string                   `json:"source_balance"`
	DestinationBalance string                   `json:"destination_balance"`
	ExecutedExternally bool                     `json:"executed_externally"`
}

type LifecycleResult struct {
	Goal           contract.SavingsGoal              `json:"goal"`
	Account        contract.SavingsAccountAllocation `json:"account"`
	Changed        bool                              `json:"changed"`
	ReleasedAmount string                            `json:"released_amount"`
}

type InsufficientBalanceError struct{ Balance, Allocated, Unallocated, Requested string }

func (e *InsufficientBalanceError) Error() string { return "insufficient unallocated balance" }

type ExceedsCurrentError struct{ Current, Requested string }

func (e *ExceedsCurrentError) Error() string { return "release exceeds current allocation" }

type TargetNotReachedError struct{ Target, Current, Remaining string }

func (e *TargetNotReachedError) Error() string { return "savings goal target not reached" }

type FundingNotFoundError struct{ EntryID int64 }

func (e *FundingNotFoundError) Error() string {
	return fmt.Sprintf("savings goal funding entry %d not found", e.EntryID)
}

type FundingDependencyConflictError struct{ EntryID int64 }

func (e *FundingDependencyConflictError) Error() string {
	return fmt.Sprintf("savings goal funding entry %d cannot be reversed", e.EntryID)
}

type IdempotencyConflictError struct{ Key string }

func (e *IdempotencyConflictError) Error() string {
	return fmt.Sprintf("idempotency key %q conflicts with an existing operation", e.Key)
}

type goalRow struct {
	values  goalValues
	current int64
	active  int
}

func loadGoal(ctx context.Context, tx *sql.Tx, id int64) (goalRow, error) {
	var r goalRow
	var targetDate, note, completed, cancelled sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT g.id,g.name,g.account_id,a.name,g.target_amount_hundredths,g.target_date,g.note,g.status,g.completed_at,g.cancelled_at,g.created_at,g.updated_at,a.active,COALESCE(SUM(e.delta_hundredths),0) FROM savings_goals g JOIN accounts a ON a.id=g.account_id LEFT JOIN savings_goal_entries e ON e.goal_id=g.id WHERE g.id=? GROUP BY g.id`, id).Scan(&r.values.id, &r.values.name, &r.values.accountID, &r.values.account, &r.values.targetAmount, &targetDate, &note, &r.values.status, &completed, &cancelled, &r.values.createdAt, &r.values.updatedAt, &r.active, &r.current)
	if errors.Is(err, sql.ErrNoRows) {
		return r, &NotFoundError{ID: id}
	}
	if err != nil {
		return r, err
	}
	r.values.targetDate = nullableStringValue(targetDate)
	r.values.note = nullableStringValue(note)
	r.values.completedAt = nullableStringValue(completed)
	r.values.cancelledAt = nullableStringValue(cancelled)
	return r, nil
}

func validateMutation(goalID int64, amount, date, key string, note *string, today string) (int64, string, string, *string, []contract.FieldIssue) {
	var fields []contract.FieldIssue
	if goalID < 1 {
		fields = append(fields, contract.FieldIssue{Field: "goal_id", Reason: "must be a positive integer"})
	}
	v, err := contract.ParseAmount(amount)
	if err != nil {
		fields = append(fields, contract.FieldIssue{Field: "amount", Reason: "must be a positive amount with at most two decimal places"})
	} else if v < 1 {
		fields = append(fields, contract.FieldIssue{Field: "amount", Reason: "must be greater than zero"})
	}
	d, err := contract.ParseDate(date)
	if err != nil {
		fields = append(fields, contract.FieldIssue{Field: "date", Reason: "must be a valid YYYY-MM-DD date"})
	} else if d > today {
		fields = append(fields, contract.FieldIssue{Field: "date", Reason: "must not be in the future"})
	}
	k := contract.TrimASCIIWhitespace(key)
	if k == "" {
		fields = append(fields, contract.FieldIssue{Field: "idempotency_key", Reason: "must not be empty"})
	}
	var n *string
	if note != nil {
		x := contract.TrimASCIIWhitespace(*note)
		if x != "" {
			n = &x
		}
	}
	return v, d, k, n, fields
}

func hash(v any) string {
	b, _ := json.Marshal(v)
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func accountAllocation(ctx context.Context, tx *sql.Tx, accountID int64) (contract.SavingsAccountAllocation, int64, int64, error) {
	var name string
	var opening, entries, allocated int64
	err := tx.QueryRowContext(ctx, `SELECT a.name,a.opening_balance_hundredths,COALESCE((SELECT SUM(e.delta_hundredths) FROM account_entries e WHERE e.account_id=a.id),0),COALESCE((SELECT SUM(se.delta_hundredths) FROM savings_goal_entries se JOIN savings_goals g ON g.id=se.goal_id WHERE se.account_id=a.id AND g.status!='cancelled'),0) FROM accounts a WHERE a.id=?`, accountID).Scan(&name, &opening, &entries, &allocated)
	if err != nil {
		return contract.SavingsAccountAllocation{}, 0, 0, err
	}
	if (entries > 0 && opening > math.MaxInt64-entries) || (entries < 0 && opening < math.MinInt64-entries) {
		return contract.SavingsAccountAllocation{}, 0, 0, errors.New("account balance overflow")
	}
	balance := opening + entries
	if allocated > 0 && balance < math.MinInt64+allocated {
		return contract.SavingsAccountAllocation{}, 0, 0, errors.New("allocation overflow")
	}
	unallocated := balance - allocated
	shortfall := int64(0)
	if unallocated < 0 {
		shortfall = -unallocated
	}
	b, _ := contract.FormatSignedAmount(balance)
	a, _ := contract.FormatSignedAmount(allocated)
	u, _ := contract.FormatSignedAmount(unallocated)
	sh, _ := contract.FormatAmount(shortfall)
	return contract.SavingsAccountAllocation{AccountID: accountID, Account: name, Balance: b, Allocated: a, Unallocated: u, AllocationShortfall: sh}, balance, allocated, nil
}

func insertEntry(ctx context.Context, tx *sql.Tx, goalID, accountID, delta int64, kind, date string, note *string, transferID *int64, reversal *int64, key, fingerprint, stamp string) (int64, error) {
	r, err := tx.ExecContext(ctx, `INSERT INTO savings_goal_entries(goal_id,account_id,delta_hundredths,kind,date,note,transfer_id,reversal_of_entry_id,idempotency_key,fingerprint,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, goalID, accountID, delta, kind, date, note, transferID, reversal, key, fingerprint, stamp)
	if err != nil {
		return 0, err
	}
	return r.LastInsertId()
}

func (s *Store) mutateAllocation(ctx context.Context, in AllocationInput, release bool) (GoalMutationResult, []contract.FieldIssue, error) {
	now := s.now()
	amount, date, key, note, fields := validateMutation(in.GoalID, in.Amount, in.Date, in.IdempotencyKey, in.Note, now.Format("2006-01-02"))
	if len(fields) > 0 {
		return GoalMutationResult{}, fields, nil
	}
	fingerprint := hash(struct {
		Op             string
		GoalID, Amount int64
		Date           string
		Note           *string
	}{map[bool]string{false: "allocate", true: "release"}[release], in.GoalID, amount, date, note})
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return GoalMutationResult{}, nil, err
	}
	defer tx.Rollback()
	var existingGoal int64
	var existingFingerprint string
	err = tx.QueryRowContext(ctx, `SELECT goal_id,fingerprint FROM savings_goal_entries WHERE idempotency_key=?`, key).Scan(&existingGoal, &existingFingerprint)
	if err == nil {
		if existingFingerprint != fingerprint {
			return GoalMutationResult{}, nil, &IdempotencyConflictError{Key: key}
		}
		g, e := loadGoal(ctx, tx, existingGoal)
		if e != nil {
			return GoalMutationResult{}, nil, e
		}
		goal, e := buildGoal(g.values, g.current)
		if e != nil {
			return GoalMutationResult{}, nil, e
		}
		totals, _, _, e := accountAllocation(ctx, tx, g.values.accountID)
		return GoalMutationResult{Goal: goal, Account: totals, IdempotentReplay: true}, nil, e
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return GoalMutationResult{}, nil, err
	}
	g, err := loadGoal(ctx, tx, in.GoalID)
	if err != nil {
		return GoalMutationResult{}, nil, err
	}
	if g.active != 1 {
		return GoalMutationResult{}, nil, ErrAccountInactive
	}
	if g.values.status == "cancelled" || (!release && g.values.status != "active") {
		return GoalMutationResult{}, nil, &ClosedError{ID: in.GoalID, Status: g.values.status}
	}
	totals, _, _, err := accountAllocation(ctx, tx, g.values.accountID)
	if err != nil {
		return GoalMutationResult{}, nil, err
	}
	if release {
		if amount > g.current {
			return GoalMutationResult{}, nil, &ExceedsCurrentError{Current: formatSigned(g.current), Requested: formatSigned(amount)}
		}
		amount = -amount
	} else {
		available, _ := contract.ParseSignedAmount(totals.Unallocated)
		if available < amount {
			return GoalMutationResult{}, nil, &InsufficientBalanceError{Balance: totals.Balance, Allocated: totals.Allocated, Unallocated: totals.Unallocated, Requested: formatSigned(amount)}
		}
	}
	kind := "allocation"
	if release {
		kind = "release"
	}
	_, err = insertEntry(ctx, tx, g.values.id, g.values.accountID, amount, kind, date, note, nil, nil, key, fingerprint, timestamp(now))
	if err != nil {
		return GoalMutationResult{}, nil, err
	}
	g.current += amount
	goal, err := buildGoal(g.values, g.current)
	if err != nil {
		return GoalMutationResult{}, nil, err
	}
	totals, _, _, err = accountAllocation(ctx, tx, g.values.accountID)
	if err != nil {
		return GoalMutationResult{}, nil, err
	}
	if err = tx.Commit(); err != nil {
		return GoalMutationResult{}, nil, err
	}
	return GoalMutationResult{Goal: goal, Account: totals, Changed: true}, nil, nil
}

func formatSigned(v int64) string { x, _ := contract.FormatSignedAmount(v); return x }
func (s *Store) Allocate(ctx context.Context, in AllocationInput) (GoalMutationResult, []contract.FieldIssue, error) {
	return s.mutateAllocation(ctx, in, false)
}
func (s *Store) Release(ctx context.Context, in AllocationInput) (GoalMutationResult, []contract.FieldIssue, error) {
	return s.mutateAllocation(ctx, in, true)
}

func (s *Store) Fund(ctx context.Context, in FundingInput) (FundingResult, []contract.FieldIssue, error) {
	now := s.now()
	amount, date, key, note, fields := validateMutation(in.GoalID, in.Amount, in.Date, in.IdempotencyKey, in.Note, now.Format("2006-01-02"))
	if in.SourceAccountID < 1 {
		fields = append(fields, contract.FieldIssue{Field: "source_account_id", Reason: "must be a positive integer"})
	}
	if len(fields) > 0 {
		return FundingResult{}, fields, nil
	}
	fp := hash(struct {
		Op                     string
		GoalID, Source, Amount int64
		Date                   string
		Note                   *string
	}{"fund", in.GoalID, in.SourceAccountID, amount, date, note})
	stamp := timestamp(now)
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return FundingResult{}, nil, err
	}
	defer tx.Rollback()
	var eid, gid, tid int64
	var oldfp string
	err = tx.QueryRowContext(ctx, `SELECT id,goal_id,transfer_id,fingerprint FROM savings_goal_entries WHERE idempotency_key=?`, key).Scan(&eid, &gid, &tid, &oldfp)
	if err == nil {
		if oldfp != fp {
			return FundingResult{}, nil, &IdempotencyConflictError{Key: key}
		}
		g, e := loadGoal(ctx, tx, gid)
		if e != nil {
			return FundingResult{}, nil, e
		}
		goal, e := buildGoal(g.values, g.current)
		if e != nil {
			return FundingResult{}, nil, e
		}
		totals, _, _, e := accountAllocation(ctx, tx, g.values.accountID)
		if e != nil {
			return FundingResult{}, nil, e
		}
		tr, e := account.TransferInTx(ctx, tx, account.TransferInTxInput{SourceAccountID: in.SourceAccountID, DestinationAccountID: g.values.accountID, AmountHundredths: amount, Date: date, Note: note, IdempotencyKey: "savings-goal-fund:" + key, Fingerprint: hash(struct{ Parent string }{fp}), Timestamp: stamp})
		return FundingResult{GoalMutationResult: GoalMutationResult{Goal: goal, Account: totals, IdempotentReplay: true}, Transfer: tr.Transfer, SourceBalance: tr.SourceBalance, DestinationBalance: tr.DestinationBalance}, nil, e
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return FundingResult{}, nil, err
	}
	g, err := loadGoal(ctx, tx, in.GoalID)
	if err != nil {
		return FundingResult{}, nil, err
	}
	if g.values.status != "active" {
		return FundingResult{}, nil, &ClosedError{ID: g.values.id, Status: g.values.status}
	}
	if g.active != 1 {
		return FundingResult{}, nil, ErrAccountInactive
	}
	if in.SourceAccountID == g.values.accountID {
		return FundingResult{}, []contract.FieldIssue{{Field: "source_account_id", Reason: "must differ from the holding account"}}, nil
	}
	tr, err := account.TransferInTx(ctx, tx, account.TransferInTxInput{SourceAccountID: in.SourceAccountID, DestinationAccountID: g.values.accountID, AmountHundredths: amount, Date: date, Note: note, IdempotencyKey: "savings-goal-fund:" + key, Fingerprint: hash(struct{ Parent string }{fp}), Timestamp: stamp})
	if err != nil {
		return FundingResult{}, nil, err
	}
	tid = tr.Transfer.ID
	_, err = insertEntry(ctx, tx, g.values.id, g.values.accountID, amount, "transfer_funding", date, note, &tid, nil, key, fp, stamp)
	if err != nil {
		return FundingResult{}, nil, err
	}
	g.current += amount
	goal, err := buildGoal(g.values, g.current)
	if err != nil {
		return FundingResult{}, nil, err
	}
	totals, _, _, err := accountAllocation(ctx, tx, g.values.accountID)
	if err != nil {
		return FundingResult{}, nil, err
	}
	if err = tx.Commit(); err != nil {
		return FundingResult{}, nil, err
	}
	return FundingResult{GoalMutationResult: GoalMutationResult{Goal: goal, Account: totals, Changed: true}, Transfer: tr.Transfer, SourceBalance: tr.SourceBalance, DestinationBalance: tr.DestinationBalance}, nil, nil
}

func (s *Store) ReverseFunding(ctx context.Context, in ReverseFundingInput) (FundingResult, []contract.FieldIssue, error) {
	var fields []contract.FieldIssue
	if in.EntryID < 1 {
		fields = append(fields, contract.FieldIssue{Field: "entry_id", Reason: "must be a positive integer"})
	}
	key := contract.TrimASCIIWhitespace(in.IdempotencyKey)
	if key == "" {
		fields = append(fields, contract.FieldIssue{Field: "idempotency_key", Reason: "must not be empty"})
	}
	var note *string
	if in.Note != nil {
		n := contract.TrimASCIIWhitespace(*in.Note)
		if n != "" {
			note = &n
		}
	}
	if len(fields) > 0 {
		return FundingResult{}, fields, nil
	}
	now := s.now()
	stamp := timestamp(now)
	date := now.Format("2006-01-02")
	fp := hash(struct {
		Op      string
		EntryID int64
		Note    *string
	}{"reverse_funding", in.EntryID, note})
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return FundingResult{}, nil, err
	}
	defer tx.Rollback()
	var replayGoal, replayTransfer int64
	var replayFP string
	err = tx.QueryRowContext(ctx, `SELECT goal_id,transfer_id,fingerprint FROM savings_goal_entries WHERE idempotency_key=?`, key).Scan(&replayGoal, &replayTransfer, &replayFP)
	if err == nil {
		if replayFP != fp {
			return FundingResult{}, nil, &IdempotencyConflictError{Key: key}
		}
		g, e := loadGoal(ctx, tx, replayGoal)
		if e != nil {
			return FundingResult{}, nil, e
		}
		goal, e := buildGoal(g.values, g.current)
		if e != nil {
			return FundingResult{}, nil, e
		}
		totals, _, _, e := accountAllocation(ctx, tx, g.values.accountID)
		if e != nil {
			return FundingResult{}, nil, e
		}
		tr, e := account.ReverseTransferInTx(ctx, tx, account.ReverseTransferInTxInput{TransferID: replayTransfer, Note: note, Date: date, IdempotencyKey: "savings-goal-reverse:" + key, Fingerprint: hash(struct{ Parent string }{fp}), Timestamp: stamp})
		return FundingResult{GoalMutationResult: GoalMutationResult{Goal: goal, Account: totals, IdempotentReplay: true}, Transfer: tr.Transfer, SourceBalance: tr.SourceBalance, DestinationBalance: tr.DestinationBalance}, nil, e
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return FundingResult{}, nil, err
	}
	var goalID, accountID, delta, transferID int64
	var kind string
	err = tx.QueryRowContext(ctx, `SELECT goal_id,account_id,delta_hundredths,kind,transfer_id FROM savings_goal_entries WHERE id=?`, in.EntryID).Scan(&goalID, &accountID, &delta, &kind, &transferID)
	if errors.Is(err, sql.ErrNoRows) {
		return FundingResult{}, nil, &FundingNotFoundError{EntryID: in.EntryID}
	}
	if err != nil {
		return FundingResult{}, nil, err
	}
	if kind != "transfer_funding" || delta < 1 {
		return FundingResult{}, nil, &FundingDependencyConflictError{EntryID: in.EntryID}
	}
	var reversed int
	err = tx.QueryRowContext(ctx, `SELECT 1 FROM savings_goal_entries WHERE reversal_of_entry_id=?`, in.EntryID).Scan(&reversed)
	if err == nil {
		return FundingResult{}, nil, &FundingDependencyConflictError{EntryID: in.EntryID}
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return FundingResult{}, nil, err
	}
	g, err := loadGoal(ctx, tx, goalID)
	if err != nil {
		return FundingResult{}, nil, err
	}
	if g.values.status == "cancelled" || g.current < delta {
		return FundingResult{}, nil, &FundingDependencyConflictError{EntryID: in.EntryID}
	}
	_, err = insertEntry(ctx, tx, goalID, accountID, -delta, "reversal", date, note, &transferID, &in.EntryID, key, fp, stamp)
	if err != nil {
		return FundingResult{}, nil, err
	}
	tr, err := account.ReverseTransferInTx(ctx, tx, account.ReverseTransferInTxInput{TransferID: transferID, Note: note, Date: date, IdempotencyKey: "savings-goal-reverse:" + key, Fingerprint: hash(struct{ Parent string }{fp}), Timestamp: stamp})
	if err != nil {
		return FundingResult{}, nil, err
	}
	g.current -= delta
	goal, err := buildGoal(g.values, g.current)
	if err != nil {
		return FundingResult{}, nil, err
	}
	totals, _, _, err := accountAllocation(ctx, tx, accountID)
	if err != nil {
		return FundingResult{}, nil, err
	}
	if err = tx.Commit(); err != nil {
		return FundingResult{}, nil, err
	}
	return FundingResult{GoalMutationResult: GoalMutationResult{Goal: goal, Account: totals, Changed: true}, Transfer: tr.Transfer, SourceBalance: tr.SourceBalance, DestinationBalance: tr.DestinationBalance}, nil, nil
}

func (s *Store) Complete(ctx context.Context, id int64) (LifecycleResult, []contract.FieldIssue, error) {
	return s.lifecycle(ctx, id, true)
}
func (s *Store) Cancel(ctx context.Context, id int64) (LifecycleResult, []contract.FieldIssue, error) {
	return s.lifecycle(ctx, id, false)
}
func (s *Store) lifecycle(ctx context.Context, id int64, complete bool) (LifecycleResult, []contract.FieldIssue, error) {
	if id < 1 {
		return LifecycleResult{}, []contract.FieldIssue{{Field: "goal_id", Reason: "must be a positive integer"}}, nil
	}
	now := s.now()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return LifecycleResult{}, nil, err
	}
	defer tx.Rollback()
	g, err := loadGoal(ctx, tx, id)
	if err != nil {
		return LifecycleResult{}, nil, err
	}
	totals, _, _, err := accountAllocation(ctx, tx, g.values.accountID)
	if err != nil {
		return LifecycleResult{}, nil, err
	}
	released := "0.00"
	if complete {
		if g.values.status == "completed" {
			goal, _ := buildGoal(g.values, g.current)
			return LifecycleResult{Goal: goal, Account: totals}, nil, nil
		}
		if g.values.status != "active" {
			return LifecycleResult{}, nil, &ClosedError{ID: id, Status: g.values.status}
		}
		if g.current < g.values.targetAmount {
			goal, _ := buildGoal(g.values, g.current)
			return LifecycleResult{}, nil, &TargetNotReachedError{Target: goal.TargetAmount, Current: goal.CurrentAmount, Remaining: goal.RemainingAmount}
		}
		stamp := timestamp(now)
		_, err = tx.ExecContext(ctx, `UPDATE savings_goals SET status='completed',completed_at=?,updated_at=? WHERE id=?`, stamp, stamp, id)
		g.values.status = "completed"
		g.values.completedAt = &stamp
		g.values.updatedAt = stamp
	} else {
		if g.values.status == "cancelled" {
			goal, _ := buildGoal(g.values, g.current)
			return LifecycleResult{Goal: goal, Account: totals}, nil, nil
		}
		if g.values.status != "active" {
			return LifecycleResult{}, nil, &ClosedError{ID: id, Status: g.values.status}
		}
		stamp := timestamp(now)
		if g.current != 0 {
			released = formatSigned(g.current)
			_, err = insertEntry(ctx, tx, id, g.values.accountID, -g.current, "cancellation_release", now.Format("2006-01-02"), nil, nil, nil, fmt.Sprintf("savings-goal-cancel:%d", id), hash(struct {
				Op string
				ID int64
			}{"cancel", id}), stamp)
			if err != nil {
				return LifecycleResult{}, nil, err
			}
			g.current = 0
		}
		_, err = tx.ExecContext(ctx, `UPDATE savings_goals SET status='cancelled',cancelled_at=?,updated_at=? WHERE id=?`, stamp, stamp, id)
		g.values.status = "cancelled"
		g.values.cancelledAt = &stamp
		g.values.updatedAt = stamp
	}
	if err != nil {
		return LifecycleResult{}, nil, err
	}
	goal, err := buildGoal(g.values, g.current)
	if err != nil {
		return LifecycleResult{}, nil, err
	}
	totals, _, _, err = accountAllocation(ctx, tx, g.values.accountID)
	if err != nil {
		return LifecycleResult{}, nil, err
	}
	if err = tx.Commit(); err != nil {
		return LifecycleResult{}, nil, err
	}
	return LifecycleResult{Goal: goal, Account: totals, Changed: true, ReleasedAmount: released}, nil, nil
}

func (s *Store) Overview(ctx context.Context, includeInactive, includeClosed bool) (contract.SavingsOverview, error) {
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return contract.SavingsOverview{}, err
	}
	defer tx.Rollback()
	where := "WHERE active=1"
	if includeInactive {
		where = ""
	}
	rows, err := tx.QueryContext(ctx, `SELECT id FROM accounts `+where+` ORDER BY name COLLATE NOCASE,id`)
	if err != nil {
		return contract.SavingsOverview{}, err
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			return contract.SavingsOverview{}, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	out := contract.SavingsOverview{Accounts: []contract.SavingsAccountAllocation{}, Goals: []contract.SavingsGoal{}}
	var tb, ta, tu, ts int64
	for _, id := range ids {
		a, b, al, e := accountAllocation(ctx, tx, id)
		if e != nil {
			return out, e
		}
		u, _ := contract.ParseSignedAmount(a.Unallocated)
		sh, _ := contract.ParseAmount(a.AllocationShortfall)
		tb += b
		ta += al
		tu += u
		ts += sh
		out.Accounts = append(out.Accounts, a)
	}
	q := `SELECT g.id FROM savings_goals g JOIN accounts a ON a.id=g.account_id WHERE 1=1`
	if !includeInactive {
		q += ` AND a.active=1`
	}
	if !includeClosed {
		q += ` AND g.status='active'`
	}
	q += ` ORDER BY CASE WHEN g.status='active' THEN 0 ELSE 1 END,CASE WHEN g.status='active' AND g.target_date IS NULL THEN 1 ELSE 0 END,CASE WHEN g.status='active' THEN g.target_date END,CASE WHEN g.status!='active' THEN COALESCE(g.completed_at,g.cancelled_at,g.updated_at) END DESC,g.name COLLATE NOCASE,g.id`
	rows, err = tx.QueryContext(ctx, q)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		g, e := loadGoal(ctx, tx, id)
		if e != nil {
			return out, e
		}
		goal, e := buildGoal(g.values, g.current)
		if e != nil {
			return out, e
		}
		out.Goals = append(out.Goals, goal)
		switch goal.Status {
		case "active":
			out.Counts.Active++
			if goal.TargetReached {
				out.Counts.Reached++
			}
		case "completed":
			out.Counts.Completed++
		case "cancelled":
			out.Counts.Cancelled++
		}
	}
	rows.Close()
	out.TotalBalance = formatSigned(tb)
	out.TotalAllocated = formatSigned(ta)
	out.TotalUnallocated = formatSigned(tu)
	out.TotalAllocationShortfall = formatSigned(ts)
	err = tx.Commit()
	return out, err
}
