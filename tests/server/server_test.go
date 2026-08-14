package server_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jordanp2002/local-finance-mcp/internal/database"
	"github.com/jordanp2002/local-finance-mcp/internal/server"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestStdioLifecycle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	databasePath := filepath.Join(t.TempDir(), "finance.db")
	cmd := exec.Command(os.Args[0], "-test.run=TestHelperProcess")
	cmd.Env = testEnvironment(
		"LOCAL_FINANCE_MCP_TEST_HELPER=1",
		"LOCAL_FINANCE_DB_PATH="+databasePath,
	)

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.1.0"}, nil)
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatalf("initialize stdio session: %v", err)
	}
	sessionClosed := false
	defer func() {
		if !sessionClosed {
			closeAndWaitSession(t, session)
		}
	}()
	if result := session.InitializeResult(); result == nil || result.ServerInfo == nil || result.ServerInfo.Name != "local-finance-mcp" {
		t.Fatalf("unexpected initialize result: %#v", result)
	}

	result, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(result.Tools) != 0 {
		t.Fatalf("got %d tools, want 0", len(result.Tools))
	}

	closeAndWaitSession(t, session)
	sessionClosed = true
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatal("helper process has not exited after closing the client session")
	}

	if _, err := os.Stat(databasePath); err != nil {
		t.Fatalf("stat migrated database: %v", err)
	}
	db, err := database.Open(ctx, databasePath)
	if err != nil {
		t.Fatalf("open migrated database for verification: %v", err)
	}
	defer db.Close()
	var version int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		t.Fatalf("query migration version: %v", err)
	}
	if version != 1 {
		t.Fatalf("migration version = %d, want 1", version)
	}
}

func closeAndWaitSession(t *testing.T, session *mcp.ClientSession) {
	t.Helper()
	if err := session.Close(); err != nil && !errors.Is(err, mcp.ErrConnectionClosed) {
		t.Errorf("close stdio session: %v", err)
	}
	if err := session.Wait(); err != nil && !errors.Is(err, mcp.ErrConnectionClosed) {
		t.Errorf("wait for stdio session: %v", err)
	}
}

func TestHelperProcess(t *testing.T) {
	if os.Getenv("LOCAL_FINANCE_MCP_TEST_HELPER") != "1" {
		return
	}

	if err := server.Run(context.Background(), server.Config{DatabasePath: os.Getenv("LOCAL_FINANCE_DB_PATH")}); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func testEnvironment(values ...string) []string {
	env := os.Environ()
	for _, value := range values {
		name, _, ok := strings.Cut(value, "=")
		if !ok {
			continue
		}
		env = appendWithoutEnvironmentVariable(env, name)
		env = append(env, value)
	}
	return env
}

func appendWithoutEnvironmentVariable(env []string, name string) []string {
	filtered := env[:0]
	prefix := name + "="
	for _, value := range env {
		if !strings.HasPrefix(value, prefix) {
			filtered = append(filtered, value)
		}
	}
	return filtered
}
