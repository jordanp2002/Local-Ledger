package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/jordanp2002/local-finance-mcp/internal/config"
	"github.com/jordanp2002/local-finance-mcp/internal/server"
)

func run(ctx context.Context) error {
	databasePath, err := config.DatabasePath()
	if err != nil {
		return err
	}
	return server.Run(ctx, server.Config{DatabasePath: databasePath})
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx); config.ShouldReportError(err) {
		log.New(os.Stderr, "local-finance-mcp: ", 0).Fatal(err)
	}
}
