package server

import (
	"context"
	"errors"
	"fmt"

	"github.com/jordanp2002/local-finance-mcp/internal/database"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const version = "0.1.0"

type Config struct {
	DatabasePath string
}

func New() *mcp.Server {
	return mcp.NewServer(&mcp.Implementation{
		Name:    "local-finance-mcp",
		Version: version,
	}, nil)
}

func Run(ctx context.Context, config Config) error {
	db, err := database.Open(ctx, config.DatabasePath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}

	runErr := New().Run(ctx, &mcp.StdioTransport{})
	closeErr := db.Close()
	return joinRunAndCloseErrors(runErr, closeErr)
}

func joinRunAndCloseErrors(runErr, closeErr error) error {
	switch {
	case runErr != nil && closeErr != nil:
		return errors.Join(runErr, closeErr)
	case runErr != nil:
		return runErr
	default:
		return closeErr
	}
}
