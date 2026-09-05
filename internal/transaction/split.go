package transaction

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/jordanp2002/Local-Ledger/internal/contract"
	"github.com/jordanp2002/Local-Ledger/internal/rollover"
)

type canonicalSplit struct {
	Merchant    string                     `json:"merchant"`
	Date        string                     `json:"date"`
	DateOmitted bool                       `json:"date_omitted"`
	Note        *string                    `json:"note"`
	Allocations []canonicalSplitAllocation `json:"allocations"`
}

type canonicalSplitAllocation struct {
	CategoryID       int64 `json:"category_id"`
	AmountHundredths int64 `json:"amount_hundredths"`
}

func (s *Store) addSplit(ctx context.Context, in validatedSplit) (AddResult, []contract.FieldIssue, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return AddResult{}, nil, err
	}
	defer func() { _ = tx.Rollback() }()
	before, err := rollover.Snapshot(ctx, tx)
	if err != nil {
		return AddResult{}, nil, err
	}

	allocations, err := resolveSplitAllocations(ctx, tx, in.allocations)
	if err != nil {
		return AddResult{}, nil, err
	}
	fingerprint, err := fingerprintSplit(in, allocations)
	if err != nil {
		return AddResult{}, nil, err
	}
	if in.idempotencyKey != "" {
		existing, found, err := lookupIdempotency(ctx, tx, in.idempotencyKey)
		if err != nil {
			return AddResult{}, nil, err
		}
		if found {
			replay, replayErr := replayIdempotencyRecord(ctx, tx, in.idempotencyKey, fingerprint, existing)
			return replay, nil, replayErr
		}
	}

	writeAllocations := make([]InTxAllocation, len(allocations))
	for i, allocation := range allocations {
		writeAllocations[i] = InTxAllocation{
			CategoryID:       allocation.categoryID,
			AmountHundredths: allocation.amount,
		}
	}
	var note *string
	if in.note.Valid {
		note = &in.note.String
	}
	recorded, err := AddInTx(ctx, tx, InTxInput{
		Merchant:    in.merchant,
		Date:        in.date,
		Note:        note,
		Allocations: writeAllocations,
	})
	if err != nil {
		if isUniqueConstraintOn(err, "transaction_idempotency") {
			_ = tx.Rollback()
			replay, replayErr := replayIdempotency(ctx, s.DB, in.idempotencyKey, fingerprint)
			return replay, nil, replayErr
		}
		return AddResult{}, nil, err
	}
	if in.idempotencyKey != "" {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO transaction_idempotency (
				idempotency_key, request_fingerprint, transaction_id, category_source, merchant_mapping_action
			)
			VALUES (?, ?, ?, ?, ?)
		`, in.idempotencyKey, fingerprint, recorded.ID, CategorySourceProvided, MappingActionPreserved); err != nil {
			if isUniqueConstraintOn(err, "transaction_idempotency") {
				_ = tx.Rollback()
				replay, replayErr := replayIdempotency(ctx, s.DB, in.idempotencyKey, fingerprint)
				return replay, nil, replayErr
			}
			return AddResult{}, nil, err
		}
	}
	offers, err := rollover.BuildOffers(ctx, tx, before, transactionOfferChanges(recorded))
	if err != nil {
		return AddResult{}, nil, err
	}
	if err := tx.Commit(); err != nil {
		return AddResult{}, nil, err
	}
	return AddResult{
		Transaction:           recorded,
		CategorySource:        CategorySourceProvided,
		MerchantMappingAction: MappingActionPreserved,
		RolloverOffers:        offers,
	}, nil, nil
}

func resolveSplitAllocations(ctx context.Context, tx *sql.Tx, inputs []AllocationInput) ([]validatedAllocation, error) {
	resolved := make([]validatedAllocation, len(inputs))
	seen := make(map[int64]struct{}, len(inputs))
	for i, input := range inputs {
		category, err := resolveActiveCategory(ctx, tx, input.Category)
		if err != nil {
			return nil, err
		}
		if _, exists := seen[category.ID]; exists {
			return nil, errors.New("split transaction categories must be distinct")
		}
		amount, err := contract.ParseAmount(input.Amount)
		if err != nil || amount <= 0 {
			return nil, errors.New("split transaction allocation amount is invalid")
		}
		seen[category.ID] = struct{}{}
		resolved[i] = validatedAllocation{categoryID: category.ID, categoryName: category.Name, amount: amount}
	}
	return resolved, nil
}

func fingerprintSplit(in validatedSplit, allocations []validatedAllocation) (string, error) {
	canonical := canonicalSplit{
		Merchant:    in.merchant,
		Date:        in.date,
		DateOmitted: in.dateOmitted,
		Allocations: make([]canonicalSplitAllocation, len(allocations)),
	}
	if in.dateOmitted {
		canonical.Date = ""
	}
	if in.note.Valid {
		canonical.Note = &in.note.String
	}
	for i, allocation := range allocations {
		canonical.Allocations[i] = canonicalSplitAllocation{
			CategoryID:       allocation.categoryID,
			AmountHundredths: allocation.amount,
		}
	}
	payload, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(append([]byte("split-v1:"), payload...))
	return hex.EncodeToString(digest[:]), nil
}
