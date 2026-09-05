// Package sinkingfund implements derived, period-based sinking-fund balances.
package sinkingfund

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

var (
	ErrNotActive        = errors.New("sinking fund not active")
	ErrActive           = errors.New("sinking fund active")
	ErrCategoryNotFound = errors.New("category not found")
	ErrCategoryInactive = errors.New("category inactive")
	ErrMissingSnapshot  = errors.New("sinking fund requires a budget snapshot")
	ErrMissingBudget    = errors.New("sinking fund requires a category budget row")
	ErrRolloverConflict = errors.New("sinking fund conflicts with budget rollover")
)

type Period struct {
	ID             int64   `json:"id"`
	CategoryID     int64   `json:"category_id"`
	Category       string  `json:"category"`
	CategoryActive bool    `json:"category_active"`
	StartMonth     string  `json:"start_month"`
	EndMonth       *string `json:"end_month"`
	CreatedAt      string  `json:"created_at"`
	UpdatedAt      string  `json:"updated_at"`
}

type Balance struct {
	Period           Period
	CurrentMonth     string
	BaseContribution int64
	OpeningBalance   int64
	AvailableBalance int64
	Spending         int64
	ClosingBalance   int64
}

type EnableInput struct{ Category string }
type DisableInput struct{ Category string }
type ListInput struct {
	Category       *string
	IncludeHistory bool
}

type EnableResult struct {
	Changed         bool
	Period          Period
	Balance         Balance
	ReleasedBalance *int64
}
type DisableResult struct {
	Changed         bool
	Period          Period
	ReleasedBalance int64
	EffectiveMonth  string
}
type ListResult struct{ Funds []Balance }

type Store struct {
	DB  *sql.DB
	Now func() time.Time
}

type CategoryNotFoundError struct {
	Requested        string
	ActiveCategories []contract.Category
}

func (e *CategoryNotFoundError) Error() string {
	return fmt.Sprintf("category %q not found", e.Requested)
}
func (e *CategoryNotFoundError) Is(target error) bool { return target == ErrCategoryNotFound }

type CategoryInactiveError struct {
	Category         contract.Category
	ActiveCategories []contract.Category
}

func (e *CategoryInactiveError) Error() string {
	return fmt.Sprintf("category %q is inactive", e.Category.Name)
}
func (e *CategoryInactiveError) Is(target error) bool { return target == ErrCategoryInactive }

type MissingError struct {
	Category string
	Snapshot bool
}

func (e *MissingError) Error() string {
	if e.Snapshot {
		return "budget snapshot is required"
	}
	return "category budget row is required"
}
func (e *MissingError) Is(target error) bool {
	if e.Snapshot {
		return target == ErrMissingSnapshot
	}
	return target == ErrMissingBudget
}

type RolloverConflictError struct {
	CategoryID int64
	Category   string
	Months     []string
}

func (e *RolloverConflictError) Error() string {
	return fmt.Sprintf("category %q has a budget rollover overlapping the sinking fund", e.Category)
}
func (e *RolloverConflictError) Is(target error) bool { return target == ErrRolloverConflict }

func (s *Store) Enable(ctx context.Context, in EnableInput) (EnableResult, []contract.FieldIssue, error) {
	name, fields := validateName(in.Category)
	if len(fields) > 0 {
		return EnableResult{}, fields, nil
	}
	if s == nil || s.DB == nil {
		return EnableResult{}, nil, errors.New("sinking fund store database is nil")
	}
	now := s.now()
	month := now.Format("2006-01")
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return EnableResult{}, nil, err
	}
	defer func() { _ = tx.Rollback() }()
	cat, err := lookupCategory(ctx, tx, name)
	if err != nil {
		return EnableResult{}, nil, err
	}
	if cat == nil {
		return EnableResult{}, nil, &CategoryNotFoundError{Requested: name}
	}
	if !cat.Active {
		return EnableResult{}, nil, &CategoryInactiveError{Category: *cat}
	}
	period, found, err := findPeriodAt(ctx, tx, cat.ID, month, false)
	if err != nil {
		return EnableResult{}, nil, err
	}
	if found && period.EndMonth == nil {
		balance, err := calculateBalance(ctx, tx, period, month)
		if err != nil {
			return EnableResult{}, nil, err
		}
		if err := tx.Commit(); err != nil {
			return EnableResult{}, nil, err
		}
		return EnableResult{Period: period, Balance: balance}, nil, nil
	}
	if found && period.EndMonth != nil && *period.EndMonth >= month {
		return EnableResult{}, nil, ErrActive
	}
	var releasedBalance *int64
	var completedID int64
	if err := tx.QueryRowContext(ctx, `
		SELECT id FROM sinking_fund_periods
		WHERE category_id = ? AND end_month < ?
		ORDER BY end_month DESC, id DESC LIMIT 1
	`, cat.ID, month).Scan(&completedID); err == nil {
		completed, err := loadPeriod(ctx, tx, completedID)
		if err != nil {
			return EnableResult{}, nil, err
		}
		completedBalance, err := calculateBalance(ctx, tx, completed, *completed.EndMonth)
		if err != nil {
			return EnableResult{}, nil, err
		}
		value := completedBalance.ClosingBalance
		releasedBalance = &value
	} else if !errors.Is(err, sql.ErrNoRows) {
		return EnableResult{}, nil, err
	}
	var snapshot int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM budgets WHERE month = ? LIMIT 1`, month).Scan(&snapshot); errors.Is(err, sql.ErrNoRows) {
		return EnableResult{}, nil, &MissingError{Category: name, Snapshot: true}
	} else if err != nil {
		return EnableResult{}, nil, err
	}
	var amount int64
	if err := tx.QueryRowContext(ctx, `SELECT amount_hundredths FROM budgets WHERE month = ? AND category_id = ?`, month, cat.ID).Scan(&amount); errors.Is(err, sql.ErrNoRows) {
		return EnableResult{}, nil, &MissingError{Category: name}
	} else if err != nil {
		return EnableResult{}, nil, err
	}
	if months, err := overlappingRollovers(ctx, tx, cat.ID, month, month); err != nil {
		return EnableResult{}, nil, err
	} else if len(months) > 0 {
		return EnableResult{}, nil, &RolloverConflictError{CategoryID: cat.ID, Category: cat.Name, Months: months}
	}
	stamp := formatTimestamp(now)
	res, err := tx.ExecContext(ctx, `INSERT INTO sinking_fund_periods (category_id,start_month,created_at,updated_at) VALUES (?,?,?,?)`, cat.ID, month, stamp, stamp)
	if err != nil {
		return EnableResult{}, nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return EnableResult{}, nil, err
	}
	period, err = loadPeriod(ctx, tx, id)
	if err != nil {
		return EnableResult{}, nil, err
	}
	balance, err := calculateBalance(ctx, tx, period, month)
	if err != nil {
		return EnableResult{}, nil, err
	}
	if err := tx.Commit(); err != nil {
		return EnableResult{}, nil, err
	}
	return EnableResult{Changed: true, Period: period, Balance: balance, ReleasedBalance: releasedBalance}, nil, nil
}

func (s *Store) Disable(ctx context.Context, in DisableInput) (DisableResult, []contract.FieldIssue, error) {
	name, fields := validateName(in.Category)
	if len(fields) > 0 {
		return DisableResult{}, fields, nil
	}
	if s == nil || s.DB == nil {
		return DisableResult{}, nil, errors.New("sinking fund store database is nil")
	}
	now := s.now()
	month := now.Format("2006-01")
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return DisableResult{}, nil, err
	}
	defer func() { _ = tx.Rollback() }()
	cat, err := lookupCategory(ctx, tx, name)
	if err != nil {
		return DisableResult{}, nil, err
	}
	if cat == nil {
		return DisableResult{}, nil, &CategoryNotFoundError{Requested: name}
	}
	period, found, err := findPeriodAt(ctx, tx, cat.ID, month, false)
	if err != nil {
		return DisableResult{}, nil, err
	}
	if !found {
		return DisableResult{}, nil, ErrNotActive
	}
	balance, err := calculateBalance(ctx, tx, period, month)
	if err != nil {
		return DisableResult{}, nil, err
	}
	if period.EndMonth != nil {
		next, err := nextMonth(month)
		if err != nil {
			return DisableResult{}, nil, err
		}
		if err := tx.Commit(); err != nil {
			return DisableResult{}, nil, err
		}
		return DisableResult{Changed: false, Period: period, ReleasedBalance: balance.ClosingBalance, EffectiveMonth: next}, nil, nil
	}
	stamp := formatTimestamp(now)
	if _, err := tx.ExecContext(ctx, `UPDATE sinking_fund_periods SET end_month=?,updated_at=? WHERE id=?`, month, stamp, period.ID); err != nil {
		return DisableResult{}, nil, err
	}
	period.EndMonth = &month
	period.UpdatedAt = stamp
	next, err := nextMonth(month)
	if err != nil {
		return DisableResult{}, nil, err
	}
	if err := tx.Commit(); err != nil {
		return DisableResult{}, nil, err
	}
	return DisableResult{Changed: true, Period: period, ReleasedBalance: balance.ClosingBalance, EffectiveMonth: next}, nil, nil
}

func (s *Store) List(ctx context.Context, in ListInput) (ListResult, []contract.FieldIssue, error) {
	if s == nil || s.DB == nil {
		return ListResult{}, nil, errors.New("sinking fund store database is nil")
	}
	now := s.now()
	month := now.Format("2006-01")
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return ListResult{}, nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var categoryID *int64
	if in.Category != nil {
		name, fields := validateName(*in.Category)
		if len(fields) > 0 {
			return ListResult{}, fields, nil
		}
		cat, err := lookupCategory(ctx, tx, name)
		if err != nil {
			return ListResult{}, nil, err
		}
		if cat == nil {
			return ListResult{}, nil, &CategoryNotFoundError{Requested: name}
		}
		categoryID = &cat.ID
	}
	where, args := "", []any{}
	if categoryID != nil {
		where = " WHERE p.category_id = ?"
		args = append(args, *categoryID)
	}
	rows, err := tx.QueryContext(ctx, `SELECT p.id FROM sinking_fund_periods p`+where+` ORDER BY p.category_id,p.start_month,p.id`, args...)
	if err != nil {
		return ListResult{}, nil, err
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return ListResult{}, nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return ListResult{}, nil, err
	}
	result := ListResult{Funds: make([]Balance, 0)}
	for _, id := range ids {
		p, err := loadPeriod(ctx, tx, id)
		if err != nil {
			return ListResult{}, nil, err
		}
		active := p.EndMonth == nil || *p.EndMonth >= month
		if !in.IncludeHistory && !active {
			continue
		}
		finalMonth := month
		if p.EndMonth != nil && *p.EndMonth < finalMonth {
			finalMonth = *p.EndMonth
		}
		b, err := calculateBalance(ctx, tx, p, finalMonth)
		if err != nil {
			return ListResult{}, nil, err
		}
		result.Funds = append(result.Funds, b)
	}
	// Active/current periods first, then category and start month.
	for i := range result.Funds {
		for j := i + 1; j < len(result.Funds); j++ {
			a, b := result.Funds[i], result.Funds[j]
			ai := a.Period.EndMonth == nil || *a.Period.EndMonth >= month
			aj := b.Period.EndMonth == nil || *b.Period.EndMonth >= month
			if (!ai && aj) || (ai == aj && (strings.ToLower(a.Period.Category) > strings.ToLower(b.Period.Category) || (strings.EqualFold(a.Period.Category, b.Period.Category) && (a.Period.StartMonth > b.Period.StartMonth || (a.Period.StartMonth == b.Period.StartMonth && a.Period.ID > b.Period.ID))))) {
				result.Funds[i], result.Funds[j] = result.Funds[j], result.Funds[i]
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return ListResult{}, nil, err
	}
	return result, nil, nil
}

// BalancesForMonth returns all fund balances active in month. It is the seam
// consumed by summary reporting and performs only reads on the supplied tx.
func BalancesForMonth(ctx context.Context, tx *sql.Tx, month string) (map[int64]Balance, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id FROM sinking_fund_periods WHERE start_month <= ? AND (end_month IS NULL OR end_month >= ?)`, month, month)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[int64]Balance)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		p, err := loadPeriod(ctx, tx, id)
		if err != nil {
			return nil, err
		}
		b, err := calculateBalance(ctx, tx, p, month)
		if err != nil {
			return nil, err
		}
		result[p.CategoryID] = b
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func calculateBalance(ctx context.Context, tx *sql.Tx, p Period, month string) (Balance, error) {
	prior := month
	var err error
	prior, err = previousMonth(month)
	if err != nil {
		return Balance{}, err
	}
	base, err := sum(ctx, tx, `SELECT amount_hundredths FROM budgets WHERE category_id=? AND month>=? AND month<=?`, p.CategoryID, p.StartMonth, prior)
	if err != nil {
		return Balance{}, err
	}
	spent, err := spending(ctx, tx, p.CategoryID, p.StartMonth, prior)
	if err != nil {
		return Balance{}, err
	}
	opening, ok := subtract(base, spent)
	if !ok {
		return Balance{}, overflow("opening balance")
	}
	contribution, err := budgetAmount(ctx, tx, p.CategoryID, month)
	if err != nil {
		return Balance{}, err
	}
	available, ok := add(opening, contribution)
	if !ok {
		return Balance{}, overflow("available balance")
	}
	currentSpent, err := spending(ctx, tx, p.CategoryID, month, month)
	if err != nil {
		return Balance{}, err
	}
	closing, ok := subtract(available, currentSpent)
	if !ok {
		return Balance{}, overflow("closing balance")
	}
	return Balance{Period: p, CurrentMonth: month, BaseContribution: contribution, OpeningBalance: opening, AvailableBalance: available, Spending: currentSpent, ClosingBalance: closing}, nil
}
func spending(ctx context.Context, tx *sql.Tx, id int64, from, to string) (int64, error) {
	return sum(ctx, tx, `SELECT a.amount_hundredths FROM transaction_allocations a JOIN transactions t ON t.id=a.transaction_id WHERE a.category_id=? AND t.date>=? AND t.date<=?`, id, from+"-01", dateEnd(to))
}
func sum(ctx context.Context, tx *sql.Tx, q string, args ...any) (int64, error) {
	rows, err := tx.QueryContext(ctx, q, args...)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var total int64
	for rows.Next() {
		var n int64
		if err := rows.Scan(&n); err != nil {
			return 0, err
		}
		var ok bool
		total, ok = add(total, n)
		if !ok {
			return 0, overflow("sum")
		}
	}
	return total, rows.Err()
}
func budgetAmount(ctx context.Context, tx *sql.Tx, id int64, month string) (int64, error) {
	var n int64
	err := tx.QueryRowContext(ctx, `SELECT amount_hundredths FROM budgets WHERE category_id=? AND month=?`, id, month).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return n, err
}
func findPeriodAt(ctx context.Context, tx *sql.Tx, id int64, month string, openOnly bool) (Period, bool, error) {
	q := `SELECT id FROM sinking_fund_periods WHERE category_id=? AND start_month<=? AND (end_month IS NULL OR end_month>=?)`
	if openOnly {
		q += ` AND end_month IS NULL`
	}
	q += ` ORDER BY start_month DESC,id DESC LIMIT 1`
	var pid int64
	err := tx.QueryRowContext(ctx, q, id, month, month).Scan(&pid)
	if errors.Is(err, sql.ErrNoRows) {
		return Period{}, false, nil
	}
	if err != nil {
		return Period{}, false, err
	}
	p, err := loadPeriod(ctx, tx, pid)
	return p, true, err
}
func loadPeriod(ctx context.Context, tx *sql.Tx, id int64) (Period, error) {
	var p Period
	var active int
	err := tx.QueryRowContext(ctx, `SELECT p.id,p.category_id,c.name,c.active,p.start_month,p.end_month,p.created_at,p.updated_at FROM sinking_fund_periods p JOIN categories c ON c.id=p.category_id WHERE p.id=?`, id).Scan(&p.ID, &p.CategoryID, &p.Category, &active, &p.StartMonth, &p.EndMonth, &p.CreatedAt, &p.UpdatedAt)
	p.CategoryActive = active == 1
	return p, err
}
func lookupCategory(ctx context.Context, tx *sql.Tx, name string) (*contract.Category, error) {
	var c contract.Category
	var active int
	err := tx.QueryRowContext(ctx, `SELECT id,name,active,created_at,updated_at FROM categories WHERE name=? COLLATE NOCASE`, name).Scan(&c.ID, &c.Name, &active, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	c.Active = active == 1
	return &c, err
}
func overlappingRollovers(ctx context.Context, tx *sql.Tx, id int64, from, to string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT source_month FROM budget_rollovers WHERE category_id=? AND (source_month BETWEEN ? AND ? OR target_month BETWEEN ? AND ?) UNION SELECT target_month FROM budget_rollovers WHERE category_id=? AND (source_month BETWEEN ? AND ? OR target_month BETWEEN ? AND ?) ORDER BY 1`, id, from, to, from, to, id, from, to, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []string
	for rows.Next() {
		var m string
		if err := rows.Scan(&m); err != nil {
			return nil, err
		}
		result = append(result, m)
	}
	return result, rows.Err()
}

// OverlappingMonths identifies sinking-fund periods touching a month range.
// It is used by the rollover write boundary for mutual exclusion.
func OverlappingMonths(ctx context.Context, tx *sql.Tx, id int64, from, to string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT start_month, end_month
		FROM sinking_fund_periods
		WHERE category_id = ?
		  AND start_month <= ?
		  AND (end_month IS NULL OR end_month >= ?)
		ORDER BY start_month, id
	`, id, to, from)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	months := make([]string, 0)
	for rows.Next() {
		var start string
		var end sql.NullString
		if err := rows.Scan(&start, &end); err != nil {
			return nil, err
		}
		months = append(months, start)
		if end.Valid && end.String != start {
			months = append(months, end.String)
		}
	}
	return months, rows.Err()
}

// ActiveAt reports whether a category uses sinking-fund accounting in month.
func ActiveAt(ctx context.Context, tx *sql.Tx, id int64, month string) (bool, error) {
	var marker int
	err := tx.QueryRowContext(ctx, `
		SELECT 1 FROM sinking_fund_periods
		WHERE category_id = ? AND start_month <= ?
		  AND (end_month IS NULL OR end_month >= ?)
		LIMIT 1
	`, id, month, month).Scan(&marker)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func HasOpenPeriod(ctx context.Context, tx *sql.Tx, id int64) (bool, error) {
	var marker int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM sinking_fund_periods WHERE category_id = ? AND end_month IS NULL LIMIT 1`, id).Scan(&marker)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}
func validateName(v string) (string, []contract.FieldIssue) {
	v = contract.TrimASCIIWhitespace(v)
	if v == "" {
		return "", []contract.FieldIssue{{Field: "category", Reason: "must not be empty"}}
	}
	if strings.ContainsRune(v, '\x00') {
		return "", []contract.FieldIssue{{Field: "category", Reason: "must not contain NUL characters"}}
	}
	return v, nil
}
func (s *Store) now() time.Time {
	if s != nil && s.Now != nil {
		return s.Now()
	}
	return time.Now()
}
func formatTimestamp(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }
func dateEnd(month string) string {
	t, _ := time.Parse("2006-01", month)
	return t.AddDate(0, 1, 0).Add(-time.Second).Format("2006-01-02")
}
func previousMonth(month string) (string, error) {
	t, e := time.Parse("2006-01", month)
	if e != nil {
		return "", e
	}
	return t.AddDate(0, -1, 0).Format("2006-01"), nil
}
func nextMonth(month string) (string, error) {
	t, e := time.Parse("2006-01", month)
	if e != nil {
		return "", e
	}
	return t.AddDate(0, 1, 0).Format("2006-01"), nil
}
func add(a, b int64) (int64, bool) {
	if b > 0 && a > math.MaxInt64-b {
		return 0, false
	}
	if b < 0 && a < math.MinInt64-b {
		return 0, false
	}
	return a + b, true
}
func subtract(a, b int64) (int64, bool) { return add(a, -b) }
func overflow(s string) error           { return fmt.Errorf("sinking fund %s overflow", s) }
