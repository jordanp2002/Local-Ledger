package recurring

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/jordanp2002/local-finance-mcp/internal/contract"
)

// PreviewDue inspects active recurring expense templates due on or before today for the current month.
func (s *Store) PreviewDue(ctx context.Context) (PreviewDueResult, error) {
	if s == nil || s.DB == nil {
		return PreviewDueResult{}, errors.New("recurring store database is nil")
	}
	if s.Now == nil {
		return PreviewDueResult{}, errors.New("recurring store clock is nil")
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return PreviewDueResult{}, err
	}
	defer func() { _ = tx.Rollback() }()

	return calculateDue(ctx, tx, s.Now())
}

func calculateDue(ctx context.Context, q queryer, now time.Time) (PreviewDueResult, error) {
	year, month, day := now.Date()
	monthStr := fmt.Sprintf("%04d-%02d", year, month)
	asOfDate := fmt.Sprintf("%04d-%02d-%02d", year, month, day)
	daysInMonth := time.Date(year, month+1, 0, 0, 0, 0, 0, now.Location()).Day()

	rows, err := q.QueryContext(ctx, `
		SELECT
			r.id,
			r.merchant,
			r.amount_hundredths,
			r.category_id,
			c.name,
			c.active,
			r.day_of_month,
			r.note
		FROM recurring_transactions r
		INNER JOIN categories c ON c.id = r.category_id
		WHERE r.active = 1
		  AND NOT EXISTS (
			  SELECT 1
			  FROM recurring_transaction_runs run
			  WHERE run.recurring_transaction_id = r.id
			    AND run.month = ?
		  )
	`, monthStr)
	if err != nil {
		return PreviewDueResult{}, err
	}
	defer rows.Close()

	dueTransactions := make([]contract.DueTransaction, 0)
	blocked := make([]contract.BlockedDueTransaction, 0)
	var totalHundredths int64 = 0

	for rows.Next() {
		var (
			id               int64
			merchant         string
			amountHundredths int64
			categoryID       int64
			categoryName     string
			categoryActive   int64
			dayOfMonth       int64
			note             sql.NullString
		)
		if err := rows.Scan(
			&id,
			&merchant,
			&amountHundredths,
			&categoryID,
			&categoryName,
			&categoryActive,
			&dayOfMonth,
			&note,
		); err != nil {
			return PreviewDueResult{}, err
		}

		effectiveDay := int(dayOfMonth)
		if effectiveDay > daysInMonth {
			effectiveDay = daysInMonth
		}
		if effectiveDay > day {
			continue
		}

		dueDate := fmt.Sprintf("%04d-%02d-%02d", year, month, effectiveDay)

		if categoryActive == 1 {
			amountStr, err := contract.FormatAmount(amountHundredths)
			if err != nil {
				return PreviewDueResult{}, err
			}
			var ok bool
			totalHundredths, ok = checkedAdd(totalHundredths, amountHundredths)
			if !ok {
				return PreviewDueResult{}, errors.New("amount overflow")
			}
			var notePtr *string
			if note.Valid {
				notePtr = &note.String
			}
			dueTransactions = append(dueTransactions, contract.DueTransaction{
				RecurringTransactionID: id,
				Merchant:               merchant,
				Amount:                 amountStr,
				CategoryID:             categoryID,
				Category:               categoryName,
				DueDate:                dueDate,
				Note:                   notePtr,
			})
		} else {
			blocked = append(blocked, contract.BlockedDueTransaction{
				RecurringTransactionID: id,
				Merchant:               merchant,
				Category:               categoryName,
				DueDate:                dueDate,
				Reason:                 "category_inactive",
			})
		}
	}
	if err := rows.Err(); err != nil {
		return PreviewDueResult{}, err
	}

	sort.Slice(dueTransactions, func(i, j int) bool {
		if dueTransactions[i].DueDate != dueTransactions[j].DueDate {
			return dueTransactions[i].DueDate < dueTransactions[j].DueDate
		}
		leftKey := asciiNoCase(dueTransactions[i].Merchant)
		rightKey := asciiNoCase(dueTransactions[j].Merchant)
		if leftKey != rightKey {
			return leftKey < rightKey
		}
		return dueTransactions[i].RecurringTransactionID < dueTransactions[j].RecurringTransactionID
	})

	sort.Slice(blocked, func(i, j int) bool {
		if blocked[i].DueDate != blocked[j].DueDate {
			return blocked[i].DueDate < blocked[j].DueDate
		}
		leftKey := asciiNoCase(blocked[i].Merchant)
		rightKey := asciiNoCase(blocked[j].Merchant)
		if leftKey != rightKey {
			return leftKey < rightKey
		}
		return blocked[i].RecurringTransactionID < blocked[j].RecurringTransactionID
	})

	totalAmountStr, err := contract.FormatAmount(totalHundredths)
	if err != nil {
		return PreviewDueResult{}, err
	}

	return PreviewDueResult{
		AsOfDate:        asOfDate,
		Month:           monthStr,
		TotalAmount:     totalAmountStr,
		DueTransactions: dueTransactions,
		Blocked:         blocked,
	}, nil
}

func checkedAdd(left, right int64) (int64, bool) {
	if left < 0 || right < 0 || right > math.MaxInt64-left {
		return 0, false
	}
	return left + right, true
}

func asciiNoCase(value string) string {
	key := []byte(value)
	for i, character := range key {
		if character >= 'A' && character <= 'Z' {
			key[i] = character + ('a' - 'A')
		}
	}
	return string(key)
}
