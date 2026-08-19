package llm

import (
	"fmt"
	"strings"
	"time"
)

// Options is the environment-driven provider selection, kept free of any
// dependency on the config package so wiring stays in main.
type Options struct {
	Provider     string // "none" (default), "cli", or "ollama"
	Model        string
	CLICommand   string
	CLIArgs      []string
	OllamaURL    string
	Timeout      time.Duration
	CallsPerHour int
}

// Build resolves Options into a client. An unset or "none" provider yields a
// disabled client, which is the supported default: the product must run with no
// AI configured at all.
func Build(options Options, cache Cache) (*Client, error) {
	limit, window := options.CallsPerHour, time.Hour
	switch strings.ToLower(strings.TrimSpace(options.Provider)) {
	case "", "none", "off", "disabled":
		return New(nil, nil, 0, window), nil
	case "cli":
		command := strings.TrimSpace(options.CLICommand)
		if command == "" {
			return nil, fmt.Errorf("LLM_PROVIDER=cli requires LLM_CLI_COMMAND (for example \"claude\" or \"codex\")")
		}
		return New(&CLI{Command: command, Args: options.CLIArgs, ModelName: options.Model, Timeout: options.Timeout}, cache, limit, window), nil
	case "ollama":
		return New(NewOllama(options.OllamaURL, options.Model, options.Timeout), cache, limit, window), nil
	default:
		return nil, fmt.Errorf("unknown LLM_PROVIDER %q (expected none, cli, or ollama)", options.Provider)
	}
}
