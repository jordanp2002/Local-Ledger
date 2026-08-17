package server

import "github.com/modelcontextprotocol/go-sdk/mcp"

func readOnlyToolAnnotations() *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{
		OpenWorldHint: boolPointer(false),
		ReadOnlyHint:  true,
	}
}

func writableToolAnnotations(destructive, idempotent bool) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{
		DestructiveHint: boolPointer(destructive),
		IdempotentHint:  idempotent,
		OpenWorldHint:   boolPointer(false),
	}
}

func boolPointer(value bool) *bool {
	return &value
}
