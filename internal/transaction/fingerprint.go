package transaction

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

type canonicalAddBatch struct {
	Transactions []canonicalAddBatchRow `json:"transactions"`
}

type canonicalAddBatchRow struct {
	AmountHundredths int64   `json:"amount_hundredths"`
	Merchant         string  `json:"merchant"`
	Category         *string `json:"category"`
	Date             string  `json:"date"`
	Note             *string `json:"note"`
}

type canonicalAdd struct {
	AmountHundredths int64   `json:"amount_hundredths"`
	Merchant         string  `json:"merchant"`
	Category         *string `json:"category"`
	Date             string  `json:"date"`
	DateOmitted      bool    `json:"date_omitted"`
	Note             *string `json:"note"`
}

func fingerprintAdd(row validatedAdd) (string, error) {
	date := row.date
	if row.dateOmitted {
		date = ""
	}
	var note *string
	if row.note.Valid {
		note = &row.note.String
	}
	payload, err := json.Marshal(canonicalAdd{
		AmountHundredths: row.amountHundredths,
		Merchant:         row.merchant,
		Category:         row.category,
		Date:             date,
		DateOmitted:      row.dateOmitted,
		Note:             note,
	})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

func fingerprintAddBatch(rows []validatedAdd) (string, error) {
	canonical := canonicalAddBatch{Transactions: make([]canonicalAddBatchRow, len(rows))}
	for i, row := range rows {
		var note *string
		if row.note.Valid {
			note = &row.note.String
		}
		canonical.Transactions[i] = canonicalAddBatchRow{
			AmountHundredths: row.amountHundredths,
			Merchant:         row.merchant,
			Category:         row.category,
			Date:             row.date,
			Note:             note,
		}
	}
	payload, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}
