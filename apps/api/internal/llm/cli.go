package llm

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// PromptPlaceholder lets a CLI that cannot read stdin receive the prompt as an
// argument instead, e.g. LLM_CLI_ARGS="exec {{prompt}}".
const PromptPlaceholder = "{{prompt}}"

// CLI runs a locally installed coding-agent CLI as a subprocess. This is the
// cheapest option because it bills against an existing subscription rather than
// per token. Known-good configurations:
//
//	claude:  command="claude" args="-p --output-format text"   (prompt on stdin)
//	codex:   command="codex"  args="exec {{prompt}}"
type CLI struct {
	Command   string
	Args      []string
	ModelName string
	Timeout   time.Duration
}

func (c *CLI) Name() string { return "cli:" + c.Command }

func (c *CLI) Model() string {
	if strings.TrimSpace(c.ModelName) == "" {
		return "default"
	}
	return c.ModelName
}

func (c *CLI) Complete(ctx context.Context, req Request) (string, error) {
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	text := combine(req)
	args := make([]string, 0, len(c.Args))
	inline := false
	for _, arg := range c.Args {
		if strings.Contains(arg, PromptPlaceholder) {
			inline = true
			arg = strings.ReplaceAll(arg, PromptPlaceholder, text)
		}
		args = append(args, arg)
	}

	cmd := exec.CommandContext(ctx, c.Command, args...)
	if !inline {
		cmd.Stdin = strings.NewReader(text)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		if ctx.Err() != nil {
			return "", fmt.Errorf("%s timed out after %s: %s", c.Command, timeout, detail)
		}
		return "", fmt.Errorf("%s failed: %s", c.Command, truncate(detail, 400))
	}
	return stdout.String(), nil
}

// combine folds the system instruction into the prompt, because most agent CLIs
// expose no separate system-prompt channel.
func combine(req Request) string {
	system := strings.TrimSpace(req.System)
	prompt := strings.TrimSpace(req.Prompt)
	if system == "" {
		return prompt
	}
	return system + "\n\n---\n\n" + prompt
}

func truncate(v string, max int) string {
	if len(v) <= max {
		return v
	}
	return v[:max] + "..."
}
