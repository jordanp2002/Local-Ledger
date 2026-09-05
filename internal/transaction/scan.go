package transaction

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/jordanp2002/Local-Ledger/internal/contract"
)

const transactionColumns = `
	t.id,
	t.merchant,
	t.date,
	t.note,
	t.created_at,
	t.updated_at
`

func scanTransactionParent(row interface{ Scan(dest ...any) error }) (contract.Transaction, error) {
	var recorded contract.Transaction
	var note sql.NullString
	if err := row.Scan(
		&recorded.ID,
		&recorded.Merchant,
		&recorded.Date,
		&note,
		&recorded.CreatedAt,
		&recorded.UpdatedAt,
	); err != nil {
		return contract.Transaction{}, err
	}
	if note.Valid {
		recorded.Note = &note.String
	}
	return recorded, nil
}

func loadTransaction(ctx context.Context, q rowQueryer, id int64) (contract.Transaction, error) {
	recorded, err := scanTransactionParent(q.QueryRowContext(ctx, `
		SELECT `+transactionColumns+`
		FROM transactions AS t
		WHERE t.id = ?
	`, id))
	if err != nil {
		return contract.Transaction{}, err
	}
	allocations, err := loadAllocations(ctx, q, []int64{id})
	if err != nil {
		return contract.Transaction{}, err
	}
	return withTransactionAllocations(recorded, allocations[id])
}

func loadAllocations(ctx context.Context, q rowQueryer, transactionIDs []int64) (map[int64][]contract.TransactionAllocation, error) {
	allocations := make(map[int64][]contract.TransactionAllocation, len(transactionIDs))
	if len(transactionIDs) == 0 {
		return allocations, nil
	}
	args := make([]any, len(transactionIDs))
	for i, id := range transactionIDs {
		args[i] = id
	}
	rows, err := q.QueryContext(ctx, `
		SELECT a.transaction_id, a.category_id, c.name, a.amount_hundredths
		FROM transaction_allocations AS a
		INNER JOIN categories AS c ON c.id = a.category_id
		WHERE a.transaction_id IN (`+strings.TrimSuffix(strings.Repeat("?,", len(args)), ",")+`)
		ORDER BY a.transaction_id, c.name COLLATE NOCASE ASC, a.category_id ASC
	`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var allocation contract.TransactionAllocation
		var transactionID, amount int64
		if err := rows.Scan(&transactionID, &allocation.CategoryID, &allocation.Category, &amount); err != nil {
			return nil, err
		}
		allocation.Amount, err = contract.FormatAmount(amount)
		if err != nil {
			return nil, err
		}
		allocations[transactionID] = append(allocations[transactionID], allocation)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for _, id := range transactionIDs {
		if len(allocations[id]) == 0 {
			return nil, fmt.Errorf("transaction %d has no allocations", id)
		}
	}
	return allocations, nil
}

func checkedAllocationAdd(left, right int64) (int64, bool) {
	if left < 0 || right <= 0 || right > math.MaxInt64-left {
		return 0, false
	}
	return left + right, true
}

func withTransactionAllocations(recorded contract.Transaction, allocations []contract.TransactionAllocation) (contract.Transaction, error) {
	recorded.Allocations = allocations
	var total int64
	for _, allocation := range allocations {
		amount, err := contract.ParseAmount(allocation.Amount)
		if err != nil {
			return contract.Transaction{}, err
		}
		next, ok := checkedAllocationAdd(total, amount)
		if !ok {
			return contract.Transaction{}, errors.New("transaction allocation total overflow")
		}
		total = next
	}
	formatted, err := contract.FormatAmount(total)
	if err != nil {
		return contract.Transaction{}, err
	}
	recorded.Amount = formatted
	if len(allocations) == 1 {
		categoryID := allocations[0].CategoryID
		category := allocations[0].Category
		recorded.CategoryID = &categoryID
		recorded.Category = &category
	}
	return recorded, nil
}
