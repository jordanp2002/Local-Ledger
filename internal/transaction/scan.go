package transaction

import (
	"database/sql"

	"github.com/jordanp2002/local-finance-mcp/internal/contract"
)

const transactionColumns = `
	t.id,
	t.amount_hundredths,
	t.merchant,
	t.date,
	t.category_id,
	c.name,
	t.note,
	t.created_at,
	t.updated_at
`

func scanTransaction(row interface{ Scan(dest ...any) error }) (contract.Transaction, error) {
	var recorded contract.Transaction
	var hundredths int64
	var note sql.NullString
	if err := row.Scan(
		&recorded.ID,
		&hundredths,
		&recorded.Merchant,
		&recorded.Date,
		&recorded.CategoryID,
		&recorded.Category,
		&note,
		&recorded.CreatedAt,
		&recorded.UpdatedAt,
	); err != nil {
		return contract.Transaction{}, err
	}
	formatted, err := contract.FormatAmount(hundredths)
	if err != nil {
		return contract.Transaction{}, err
	}
	recorded.Amount = formatted
	if note.Valid {
		recorded.Note = &note.String
	}
	return recorded, nil
}
