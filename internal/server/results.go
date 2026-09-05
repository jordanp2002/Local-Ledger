package server

import (
	"log"

	"github.com/jordanp2002/Local-Ledger/internal/contract"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func toolOK(output any) (*mcp.CallToolResult, any, error) {
	return nil, output, nil
}

func toolError(envelope contract.ErrorEnvelope) (*mcp.CallToolResult, any, error) {
	return &mcp.CallToolResult{IsError: true}, envelope, nil
}

func invalidInputEnvelope(fields []contract.FieldIssue) contract.ErrorEnvelope {
	return contract.NewErrorEnvelope(contract.NewError(
		contract.ErrorCodeInvalidInput,
		"",
		false,
		map[string]any{"fields": fields},
	))
}

func internalToolError(logger *log.Logger, tool string, err error) (*mcp.CallToolResult, any, error) {
	if logger != nil {
		logger.Printf("%s: %v", tool, err)
	}
	return toolError(contract.NewInternalErrorEnvelope())
}
