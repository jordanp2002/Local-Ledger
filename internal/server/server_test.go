package server

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestStdioLifecycle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.Command(os.Args[0], "-test.run=TestHelperProcess")
	cmd.Env = append(os.Environ(), "LOCAL_FINANCE_MCP_TEST_HELPER=1")

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.1.0"}, nil)
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatalf("initialize stdio session: %v", err)
	}
	defer session.Close()
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
}

func TestHelperProcess(t *testing.T) {
	if os.Getenv("LOCAL_FINANCE_MCP_TEST_HELPER") != "1" {
		return
	}

	if err := Run(context.Background()); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}
