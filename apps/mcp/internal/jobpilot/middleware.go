package jobpilot

import (
	"context"
	"log/slog"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// logging records one line per tool call: which tool, how long it took, and
// whether it failed. Arguments are deliberately never logged, because they
// carry the user's notes, profile facts and application text, and a stdio
// server's log is whatever the host decided to do with stderr.
func logging(logger *slog.Logger) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method != "tools/call" {
				return next(ctx, method, req)
			}
			name := "unknown"
			if call, ok := req.(*mcp.CallToolRequest); ok && call.Params != nil {
				name = call.Params.Name
			}
			start := time.Now()
			result, err := next(ctx, method, req)
			failed := err != nil
			if out, ok := result.(*mcp.CallToolResult); ok && out != nil && out.IsError {
				failed = true
			}
			logger.Info("tool call",
				"tool", name,
				"duration_ms", time.Since(start).Milliseconds(),
				"is_error", failed)
			return result, err
		}
	}
}
