package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/jordanp2002/local-finance-mcp/internal/budget"
	"github.com/jordanp2002/local-finance-mcp/internal/category"
	"github.com/jordanp2002/local-finance-mcp/internal/database"
	"github.com/jordanp2002/local-finance-mcp/internal/merchant"
	"github.com/jordanp2002/local-finance-mcp/internal/summary"
	"github.com/jordanp2002/local-finance-mcp/internal/transaction"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const version = "0.1.0"

type Config struct {
	DatabasePath string
}

func New(db *sql.DB, now func() time.Time, logger *log.Logger) *mcp.Server {
	if logger == nil {
		logger = log.New(os.Stderr, "local-finance-mcp: ", 0)
	}
	if now == nil {
		now = time.Now
	}

	srv := mcp.NewServer(&mcp.Implementation{
		Name:    "local-finance-mcp",
		Version: version,
	}, nil)
	categoryStore := &category.Store{DB: db, Now: now}
	registerBudgetTools(srv, &budget.Store{DB: db, Now: now}, logger)
	registerCategoryTools(srv, categoryStore, logger)
	registerMerchantTools(srv, &merchant.Store{DB: db}, categoryStore, logger)
	registerSummaryTools(srv, &summary.Store{DB: db}, logger)
	registerTransactionTools(srv, &transaction.Store{DB: db, Now: now}, logger)
	return srv
}

func Run(ctx context.Context, config Config) error {
	db, err := database.Open(ctx, config.DatabasePath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}

	logger := log.New(os.Stderr, "local-finance-mcp: ", 0)
	runErr := New(db, time.Now, logger).Run(ctx, &mcp.StdioTransport{})
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
