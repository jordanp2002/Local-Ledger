package recurring

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/jordanp2002/Local-Ledger/internal/contract"
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

	now := s.Now()
	return calculateDue(ctx, tx, now)
}

func calculateDue(ctx context.Context, q queryer, now time.Time) (PreviewDueResult, error) {
	schedule, err := calculateSchedule(ctx, q, now)
	if err != nil {
		return PreviewDueResult{}, err
	}

	dueTransactions := make([]contract.DueTransaction, 0)
	blocked := make([]contract.BlockedDueTransaction, 0)
	var totalHundredths int64
	for _, item := range schedule.items {
		if !item.due {
			continue
		}
		if item.categoryActive {
			amount, err := contract.FormatAmount(item.amountHundredths)
			if err != nil {
				return PreviewDueResult{}, err
			}
			var ok bool
			totalHundredths, ok = checkedAdd(totalHundredths, item.amountHundredths)
			if !ok {
				return PreviewDueResult{}, errors.New("amount overflow")
			}
			dueTransactions = append(dueTransactions, contract.DueTransaction{
				RecurringTransactionID: item.id,
				Merchant:               item.merchant,
				Amount:                 amount,
				CategoryID:             item.categoryID,
				Category:               item.categoryName,
				DueDate:                item.scheduledDate,
				Note:                   item.note,
			})
			continue
		}
		blocked = append(blocked, contract.BlockedDueTransaction{
			RecurringTransactionID: item.id,
			Merchant:               item.merchant,
			Category:               item.categoryName,
			DueDate:                item.scheduledDate,
			Reason:                 "category_inactive",
		})
	}

	totalAmount, err := contract.FormatAmount(totalHundredths)
	if err != nil {
		return PreviewDueResult{}, err
	}
	return PreviewDueResult{
		AsOfDate:        schedule.asOfDate,
		Month:           schedule.month,
		TotalAmount:     totalAmount,
		DueTransactions: dueTransactions,
		Blocked:         blocked,
	}, nil
}

type scheduledRecurring struct {
	id               int64
	merchant         string
	amountHundredths int64
	categoryID       int64
	categoryName     string
	categoryActive   bool
	scheduledDate    string
	note             *string
	due              bool
}

type recurringSchedule struct {
	asOfDate string
	month    string
	items    []scheduledRecurring
}

func calculateSchedule(ctx context.Context, q queryer, now time.Time) (recurringSchedule, error) {
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
		return recurringSchedule{}, err
	}
	defer rows.Close()

	items := make([]scheduledRecurring, 0)
	for rows.Next() {
		var (
			item           scheduledRecurring
			categoryActive int64
			dayOfMonth     int64
			note           sql.NullString
		)
		if err := rows.Scan(
			&item.id,
			&item.merchant,
			&item.amountHundredths,
			&item.categoryID,
			&item.categoryName,
			&categoryActive,
			&dayOfMonth,
			&note,
		); err != nil {
			return recurringSchedule{}, err
		}
		item.categoryActive = categoryActive == 1
		effectiveDay := int(dayOfMonth)
		if effectiveDay > daysInMonth {
			effectiveDay = daysInMonth
		}
		item.scheduledDate = fmt.Sprintf("%04d-%02d-%02d", year, month, effectiveDay)
		item.due = effectiveDay <= day
		if note.Valid {
			item.note = &note.String
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return recurringSchedule{}, err
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].scheduledDate != items[j].scheduledDate {
			return items[i].scheduledDate < items[j].scheduledDate
		}
		leftKey := asciiNoCase(items[i].merchant)
		rightKey := asciiNoCase(items[j].merchant)
		if leftKey != rightKey {
			return leftKey < rightKey
		}
		return items[i].id < items[j].id
	})
	return recurringSchedule{asOfDate: asOfDate, month: monthStr, items: items}, nil
}

// PreviewUpcoming returns all active, unmaterialized templates scheduled this month.
func (s *Store) PreviewUpcoming(ctx context.Context) (PreviewUpcomingResult, error) {
	if s == nil || s.DB == nil {
		return PreviewUpcomingResult{}, errors.New("recurring store database is nil")
	}
	if s.Now == nil {
		return PreviewUpcomingResult{}, errors.New("recurring store clock is nil")
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return PreviewUpcomingResult{}, err
	}
	defer func() { _ = tx.Rollback() }()

	now := s.Now()
	schedule, err := calculateSchedule(ctx, tx, now)
	if err != nil {
		return PreviewUpcomingResult{}, err
	}

	upcoming := make([]contract.UpcomingTransaction, 0, len(schedule.items))
	blocked := make([]contract.BlockedDueTransaction, 0)
	var totalHundredths int64
	for _, item := range schedule.items {
		if !item.categoryActive && item.due {
			blocked = append(blocked, contract.BlockedDueTransaction{
				RecurringTransactionID: item.id,
				Merchant:               item.merchant,
				Category:               item.categoryName,
				DueDate:                item.scheduledDate,
				Reason:                 "category_inactive",
			})
			continue
		}
		amount, err := contract.FormatAmount(item.amountHundredths)
		if err != nil {
			return PreviewUpcomingResult{}, err
		}
		var ok bool
		totalHundredths, ok = checkedAdd(totalHundredths, item.amountHundredths)
		if !ok {
			return PreviewUpcomingResult{}, errors.New("amount overflow")
		}
		status := "scheduled"
		if item.due {
			status = "due"
		}
		upcoming = append(upcoming, contract.UpcomingTransaction{
			RecurringTransactionID: item.id,
			Merchant:               item.merchant,
			Amount:                 amount,
			CategoryID:             item.categoryID,
			Category:               item.categoryName,
			ScheduledDate:          item.scheduledDate,
			Status:                 status,
			Note:                   item.note,
		})
	}
	totalAmount, err := contract.FormatAmount(totalHundredths)
	if err != nil {
		return PreviewUpcomingResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return PreviewUpcomingResult{}, err
	}
	return PreviewUpcomingResult{
		AsOfDate:             schedule.asOfDate,
		Month:                schedule.month,
		TotalAmount:          totalAmount,
		UpcomingTransactions: upcoming,
		Blocked:              blocked,
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
