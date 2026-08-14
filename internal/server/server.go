package server

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const version = "0.1.0"

func New() *mcp.Server {
	return mcp.NewServer(&mcp.Implementation{
		Name:    "local-finance-mcp",
		Version: version,
	}, nil)
}

func Run(ctx context.Context) error {
	return New().Run(ctx, &mcp.StdioTransport{})
}
