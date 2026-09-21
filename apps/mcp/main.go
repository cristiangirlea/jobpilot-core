// Command jobpilot-mcp serves the JobPilot REST API to an MCP host such as
// Claude Code over stdio. It is a client of a running API; it holds no state
// and never touches the database directly.
//
// Configuration is environment only:
//
//	JOBPILOT_API_URL   API origin, default http://localhost:8080
//	JOBPILOT_TOKEN     bearer token from /api/auth/login (or register)
//	JOBPILOT_EMAIL     with JOBPILOT_PASSWORD: log in instead of, or to
//	JOBPILOT_PASSWORD  refresh, a token
//
// stdout carries the protocol, so every log line goes to stderr.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"jobpilot/core/mcp/internal/jobpilot"
)

var version = "dev"

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	slog.SetDefault(logger)
	for _, arg := range os.Args[1:] {
		switch arg {
		case "-h", "--help", "help":
			usage()
			return
		case "--version", "version":
			fmt.Fprintln(os.Stderr, "jobpilot-mcp", version)
			return
		}
	}
	client, err := jobpilot.NewClient(jobpilot.Options{
		BaseURL:  env("JOBPILOT_API_URL", "http://localhost:8080"),
		Token:    os.Getenv("JOBPILOT_TOKEN"),
		Email:    os.Getenv("JOBPILOT_EMAIL"),
		Password: os.Getenv("JOBPILOT_PASSWORD"),
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		usage()
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	server := jobpilot.NewServer(client, version)
	logger.Info("JobPilot MCP server ready", "api", client.BaseURL(), "transport", "stdio")
	// An interrupt cancels ctx and Run returns its error; that is a clean
	// shutdown, not a failure.
	if err = server.Run(ctx, &mcp.StdioTransport{}); err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `Usage: jobpilot-mcp

Serves JobPilot to an MCP host over stdio. Environment:
  JOBPILOT_API_URL   API origin (default http://localhost:8080)
  JOBPILOT_TOKEN     bearer token from POST /api/auth/login
  JOBPILOT_EMAIL     with JOBPILOT_PASSWORD, logs in and refreshes the token
  JOBPILOT_PASSWORD  prefer a token: the host stores -e values in plaintext

Register with Claude Code:
  claude mcp add jobpilot -e JOBPILOT_TOKEN=<token> -- <path to jobpilot-mcp>
`)
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
