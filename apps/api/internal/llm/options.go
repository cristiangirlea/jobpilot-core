package llm

import (
	"fmt"
	"strings"
	"time"
)

// Options is the environment-driven provider selection, kept free of any
// dependency on the config package so wiring stays in main.
type Options struct {
	Provider       string // "none" (default), "cli", or "ollama"
	Model          string
	BackupProvider string
	BackupModel    string
	BackupTimeout  time.Duration
	CLICommand     string
	CLIArgs        []string
	CLIDir         string
	OllamaURL      string
	Timeout        time.Duration
	CallsPerHour   int
}

// Build resolves Options into a client. An unset or "none" provider yields a
// disabled client, which is the supported default: the product must run with no
// AI configured at all.
func Build(options Options, cache Cache) (*Client, error) {
	var primary Provider
	switch strings.ToLower(strings.TrimSpace(options.Provider)) {
	case "", "none", "off", "disabled":
	case "cli":
		command := strings.TrimSpace(options.CLICommand)
		if command == "" {
			return nil, fmt.Errorf("LLM_PROVIDER=cli requires LLM_CLI_COMMAND (for example \"claude\" or \"codex\")")
		}
		primary = &CLI{Command: command, Args: options.CLIArgs, ModelName: options.Model, Timeout: options.Timeout, Dir: options.CLIDir}
	case "ollama":
		primary = NewOllama(options.OllamaURL, options.Model, options.Timeout)
	default:
		return nil, fmt.Errorf("unknown LLM_PROVIDER %q (expected none, cli, or ollama)", options.Provider)
	}
	client := New(primary, cache, options.CallsPerHour, time.Hour)
	switch strings.ToLower(strings.TrimSpace(options.BackupProvider)) {
	case "", "none", "off", "disabled":
		return client, nil
	case "ollama":
		if primary == nil {
			return nil, fmt.Errorf("LLM_BACKUP_PROVIDER needs LLM_PROVIDER")
		}
		if primary.Name() == "ollama" {
			return nil, fmt.Errorf("LLM_BACKUP_PROVIDER and LLM_PROVIDER cannot both be ollama")
		}
		return client.WithBackup(NewOllama(options.OllamaURL, options.BackupModel, options.BackupTimeout)), nil
	default:
		return nil, fmt.Errorf("unknown LLM_BACKUP_PROVIDER %q (expected none or ollama)", options.BackupProvider)
	}
}
