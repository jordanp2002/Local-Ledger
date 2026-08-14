package config_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jordanp2002/local-finance-mcp/internal/config"
)

func TestConfigFromEnvRejectsMissingDatabasePath(t *testing.T) {
	t.Setenv(config.DatabasePathEnv, "")
	if err := os.Unsetenv(config.DatabasePathEnv); err != nil {
		t.Fatalf("unset %s: %v", config.DatabasePathEnv, err)
	}

	_, err := config.DatabasePathFromEnv()
	if err == nil || !strings.Contains(err.Error(), "is not set") {
		t.Fatalf("configFromEnv() error = %v, want unset-variable error", err)
	}
}

func TestConfigFromEnvRejectsExplicitlyEmptyDatabasePath(t *testing.T) {
	t.Setenv(config.DatabasePathEnv, "")

	_, err := config.DatabasePathFromEnv()
	if err == nil || !strings.Contains(err.Error(), "is empty") {
		t.Fatalf("configFromEnv() error = %v, want empty-variable error", err)
	}
}

func TestConfigFromEnvRejectsRelativeDatabasePath(t *testing.T) {
	t.Setenv(config.DatabasePathEnv, filepath.Join("data", "finance.db"))

	_, err := config.DatabasePathFromEnv()
	if err == nil || !strings.Contains(err.Error(), "absolute path") {
		t.Fatalf("configFromEnv() error = %v, want relative-path error", err)
	}
}

func TestConfigFromEnvAcceptsAbsoluteDatabasePath(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "finance.db")
	t.Setenv(config.DatabasePathEnv, databasePath)

	got, err := config.DatabasePathFromEnv()
	if err != nil {
		t.Fatalf("DatabasePathFromEnv() error = %v", err)
	}
	if got != databasePath {
		t.Fatalf("DatabasePathFromEnv() = %q, want %q", got, databasePath)
	}
}

func TestConfigFromEnvErrorsAreActionable(t *testing.T) {
	t.Setenv(config.DatabasePathEnv, "relative.db")

	_, err := config.DatabasePathFromEnv()
	if err == nil {
		t.Fatal("configFromEnv() error = nil")
	}
	if errors.Is(err, os.ErrNotExist) {
		t.Fatalf("configFromEnv() returned unrelated filesystem error: %v", err)
	}
	if !strings.Contains(err.Error(), config.DatabasePathEnv) {
		t.Fatalf("configFromEnv() error = %v, want environment variable name", err)
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
