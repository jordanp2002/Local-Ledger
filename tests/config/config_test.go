package config_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jordanp2002/Local-Ledger/internal/config"
)

func TestDatabasePathUsesDefaultWhenEnvironmentIsUnset(t *testing.T) {
	t.Setenv(config.DatabasePathEnv, "")
	if err := os.Unsetenv(config.DatabasePathEnv); err != nil {
		t.Fatalf("unset %s: %v", config.DatabasePathEnv, err)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir() error = %v", err)
	}
	want := filepath.Join(home, "LocalLedger", "finance.db")
	got, err := config.DatabasePath()
	if err != nil {
		t.Fatalf("DatabasePath() error = %v", err)
	}
	if got != want {
		t.Fatalf("DatabasePath() = %q, want %q", got, want)
	}
}

func TestDatabasePathRejectsExplicitlyEmptyOverride(t *testing.T) {
	t.Setenv(config.DatabasePathEnv, "")

	_, err := config.DatabasePath()
	if err == nil || !strings.Contains(err.Error(), "is empty") {
		t.Fatalf("DatabasePath() error = %v, want empty-variable error", err)
	}
}

func TestDatabasePathRejectsRelativeOverride(t *testing.T) {
	t.Setenv(config.DatabasePathEnv, filepath.Join("data", "finance.db"))

	_, err := config.DatabasePath()
	if err == nil || !strings.Contains(err.Error(), "absolute path") {
		t.Fatalf("DatabasePath() error = %v, want relative-path error", err)
	}
}

func TestDatabasePathAcceptsAbsoluteOverride(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "finance.db")
	t.Setenv(config.DatabasePathEnv, databasePath)

	got, err := config.DatabasePath()
	if err != nil {
		t.Fatalf("DatabasePath() error = %v", err)
	}
	if got != databasePath {
		t.Fatalf("DatabasePath() = %q, want %q", got, databasePath)
	}
}

func TestDatabasePathErrorsAreActionable(t *testing.T) {
	t.Setenv(config.DatabasePathEnv, "relative.db")

	_, err := config.DatabasePath()
	if err == nil {
		t.Fatal("DatabasePath() error = nil")
	}
	if errors.Is(err, os.ErrNotExist) {
		t.Fatalf("DatabasePath() returned unrelated filesystem error: %v", err)
	}
	if !strings.Contains(err.Error(), config.DatabasePathEnv) {
		t.Fatalf("DatabasePath() error = %v, want environment variable name", err)
	}
}

func TestShouldReportErrorPreservesNonCancellationFailures(t *testing.T) {
	closeErr := errors.New("database close failed")
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "cancellation only", err: context.Canceled, want: false},
		{name: "wrapped cancellation only", err: fmt.Errorf("stdio loop: %w", context.Canceled), want: false},
		{name: "joined cancellation and close failure", err: errors.Join(context.Canceled, closeErr), want: true},
		{name: "wrapped joined cancellation and close failure", err: fmt.Errorf("shutdown: %w", errors.Join(context.Canceled, closeErr)), want: true},
		{name: "close failure only", err: closeErr, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := config.ShouldReportError(tt.err); got != tt.want {
				t.Fatalf("ShouldReportError(%v) = %t, want %t", tt.err, got, tt.want)
			}
		})
	}
}
