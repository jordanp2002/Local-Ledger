package config

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const DatabasePathEnv = "LOCAL_LEDGER_DB_PATH"

// DatabasePath returns the configured ledger path or the default path in the
// user's LocalLedger directory.
func DatabasePath() (string, error) {
	databasePath, ok := os.LookupEnv(DatabasePathEnv)
	if !ok {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("find home directory: %w", err)
		}
		return filepath.Join(home, "LocalLedger", "finance.db"), nil
	}
	if databasePath == "" {
		return "", fmt.Errorf("%s is empty", DatabasePathEnv)
	}
	if !filepath.IsAbs(databasePath) {
		return "", fmt.Errorf("%s must be an absolute path: %q", DatabasePathEnv, databasePath)
	}
	return databasePath, nil
}

func ShouldReportError(err error) bool {
	return err != nil && !isCancellationOnly(err)
}

func isCancellationOnly(err error) bool {
	if err == nil {
		return false
	}

	switch unwrapped := err.(type) {
	case interface{ Unwrap() []error }:
		children := unwrapped.Unwrap()
		if len(children) == 0 {
			return errors.Is(err, context.Canceled)
		}
		for _, child := range children {
			if !isCancellationOnly(child) {
				return false
			}
		}
		return true
	case interface{ Unwrap() error }:
		child := unwrapped.Unwrap()
		if child == nil {
			return errors.Is(err, context.Canceled)
		}
		return isCancellationOnly(child)
	default:
		return errors.Is(err, context.Canceled)
	}
}
