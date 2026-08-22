package transaction

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

const (
	IdempotencyReasonPayloadMismatch    = "payload_mismatch"
	IdempotencyReasonTransactionRemoved = "transaction_removed"
)

var ErrIdempotencyConflict = errors.New("idempotency conflict")

type IdempotencyConflictError struct {
	IdempotencyKey string
	Reason         string
	RemovedIndexes []int
}

func (e *IdempotencyConflictError) Error() string {
	if e == nil {
		return ErrIdempotencyConflict.Error()
	}
	return fmt.Sprintf("idempotency key %q conflicts (%s)", e.IdempotencyKey, e.Reason)
}

func (e *IdempotencyConflictError) Is(target error) bool {
	return target == ErrIdempotencyConflict
}

type idempotencyRecord struct {
	fingerprint    string
	transactionID  sql.NullInt64
	categorySource string
	mappingAction  string
}

func addInTx(ctx context.Context, tx *sql.Tx, in validatedAdd) (AddResult, error) {
	if in.idempotencyKey != "" {
		existing, found, err := lookupIdempotency(ctx, tx, in.idempotencyKey)
		if err != nil {
			return AddResult{}, err
		}
		if found {
			return replayIdempotencyRecord(ctx, tx, in.idempotencyKey, in.fingerprint, existing)
		}
	}

	result, err := addValidatedInTx(ctx, tx, in)
	if err != nil {
		return AddResult{}, err
	}
	if in.idempotencyKey == "" {
		return result, nil
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO transaction_idempotency (
			idempotency_key,
			request_fingerprint,
			transaction_id,
			category_source,
			merchant_mapping_action
		)
		VALUES (?, ?, ?, ?, ?)
	`, in.idempotencyKey, in.fingerprint, result.Transaction.ID, result.CategorySource, result.MerchantMappingAction); err != nil {
		return AddResult{}, err
	}
	return result, nil
}

func replayIdempotency(ctx context.Context, q rowQueryer, key, fingerprint string) (AddResult, error) {
	existing, found, err := lookupIdempotency(ctx, q, key)
	if err != nil {
		return AddResult{}, err
	}
	if !found {
		return AddResult{}, fmt.Errorf("idempotency key %q was not found after unique conflict", key)
	}
	return replayIdempotencyRecord(ctx, q, key, fingerprint, existing)
}

func replayIdempotencyRecord(ctx context.Context, q rowQueryer, key, fingerprint string, existing idempotencyRecord) (AddResult, error) {
	if existing.fingerprint != fingerprint {
		return AddResult{}, &IdempotencyConflictError{
			IdempotencyKey: key,
			Reason:         IdempotencyReasonPayloadMismatch,
		}
	}
	if !existing.transactionID.Valid {
		return AddResult{}, &IdempotencyConflictError{
			IdempotencyKey: key,
			Reason:         IdempotencyReasonTransactionRemoved,
		}
	}

	recorded, err := scanTransaction(q.QueryRowContext(ctx, `
		SELECT `+transactionColumns+`
		FROM transactions AS t
		INNER JOIN categories AS c ON c.id = t.category_id
		WHERE t.id = ?
	`, existing.transactionID.Int64))
	if err != nil {
		return AddResult{}, err
	}
	return AddResult{
		Transaction:           recorded,
		CategorySource:        existing.categorySource,
		MerchantMappingAction: existing.mappingAction,
		IdempotentReplay:      true,
	}, nil
}

func lookupIdempotency(ctx context.Context, q rowQueryer, key string) (idempotencyRecord, bool, error) {
	var rec idempotencyRecord
	err := q.QueryRowContext(ctx, `
		SELECT request_fingerprint, transaction_id, category_source, merchant_mapping_action
		FROM transaction_idempotency
		WHERE idempotency_key = ?
	`, key).Scan(&rec.fingerprint, &rec.transactionID, &rec.categorySource, &rec.mappingAction)
	if errors.Is(err, sql.ErrNoRows) {
		return idempotencyRecord{}, false, nil
	}
	if err != nil {
		return idempotencyRecord{}, false, err
	}
	return rec, true, nil
}

func isUniqueConstraintOn(err error, table string) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE constraint failed") && strings.Contains(msg, table)
}
