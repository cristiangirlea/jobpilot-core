// Command jobpilot-mcp serves the JobPilot REST API to an MCP host such as
// Claude Code over stdio. It is a client of a running API; it holds no state
// and never touches the database directly.
//
// Configuration is environment only:
//
//	JOBPILOT_API_URL   API origin, default http://localhost:8080
//	JOBPILOT_TOKEN     agent token created in Settings (preferred), or a
//	                   short-lived token from /api/auth/login
//	JOBPILOT_EMAIL     with JOBPILOT_PASSWORD: log in instead of, or to
//	JOBPILOT_PASSWORD  refresh, a token
//	JOBPILOT_CV_DIR    the one folder upload_cv may read a CV from; unset,
//	                   upload_cv refuses
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
	"time"

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
		CVDir:    os.Getenv("JOBPILOT_CV_DIR"),
	})
	if err != nil {
		// Start anyway. Exiting here would show the host only a closed
		// connection; starting lets every tool say what is wrong and how to
		// fix it, where the user will actually see it.
		logger.Warn("not configured; every tool will explain how to fix it", "error", err)
		client = jobpilot.Unconfigured(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	contract := "unknown"
	var health jobpilot.Health
	if client.Configured() {
		// A failed check is a warning, not an exit: the API may simply not
		// be up yet, and every tool reports a clear error until it is.
		checkCtx, cancelCheck := context.WithTimeout(ctx, 5*time.Second)
		health, err = client.CheckContract(checkCtx)
		cancelCheck()
	}
	switch {
	case !client.Configured():
	case err != nil:
		logger.Warn("API not reachable at startup; tools will fail until it is", "api", client.BaseURL(), "error", err)
	case client.PredatesContract():
		contract = fmt.Sprint(health.Contract)
		logger.Warn("API is older than this server expects; status-only application updates are refused so notes are not erased", "api_contract", contract)
	default:
		contract = fmt.Sprint(health.Contract)
	}
	server := jobpilot.NewServer(client, version, logger)
	logger.Info("JobPilot MCP server ready", "api", client.BaseURL(), "api_contract", contract, "transport", "stdio")
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
  JOBPILOT_TOKEN     agent token from Settings (preferred), or a login token
  JOBPILOT_EMAIL     with JOBPILOT_PASSWORD, logs in and refreshes the token
  JOBPILOT_PASSWORD  prefer a token: the host stores -e values in plaintext
  JOBPILOT_CV_DIR    the one folder upload_cv may read a .pdf or .docx from;
                     unset (the default), upload_cv refuses

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
