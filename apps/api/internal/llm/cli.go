package llm

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
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
//	claude:  command="claude" args=`-p --output-format text --tools "" --strict-mcp-config --no-session-persistence`   (prompt on stdin)
//	codex:   command="codex"  args="exec --skip-git-repo-check -"   (prompt on stdin)
//
// Each call runs in a new empty folder, created under Dir or the system's
// temporary folder and removed afterwards, so the CLI never sees the API's
// files, another call's files or a project's instructions. It gets only the
// environment variables cliEnv allows, never the API's secrets.
type CLI struct {
	Command   string
	Args      []string
	ModelName string
	Timeout   time.Duration
	Dir       string
	waitDelay time.Duration // 5s unless a test sets it
}

// cliEnv names what a CLI may inherit: the basics a process needs, and the
// CLIs' own settings and sign-in tokens by prefix.
var (
	cliEnvNames    = []string{"PATH", "HOME", "TMPDIR", "TMP", "TEMP", "LANG", "LC_ALL", "TZ", "HTTPS_PROXY", "HTTP_PROXY", "NO_PROXY", "SSL_CERT_FILE", "SYSTEMROOT", "WINDIR", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "PATHEXT", "COMSPEC", "USE_BUILTIN_RIPGREP", "DISABLE_AUTOUPDATER"}
	cliEnvPrefixes = []string{"CLAUDE_", "ANTHROPIC_", "CODEX_", "OPENAI_"}
)

func cliEnv(environ []string) []string {
	out := make([]string, 0, len(environ))
	for _, entry := range environ {
		name, _, ok := strings.Cut(entry, "=")
		if !ok || name == "" {
			continue
		}
		upper := strings.ToUpper(name)
		allowed := false
		for _, n := range cliEnvNames {
			allowed = allowed || upper == n
		}
		for _, p := range cliEnvPrefixes {
			allowed = allowed || strings.HasPrefix(upper, p)
		}
		if allowed {
			out = append(out, entry)
		}
	}
	return out
}

// Output limits: a draft is a few kilobytes, so more is a fault, not a draft.
const (
	maxCLIOutput = 1 << 20
	maxCLIErrors = 64 << 10
)

// cappedBuffer keeps the first limit bytes and drains the rest, so a noisy CLI
// cannot fill the API's memory or block on a full pipe. It wraps rather than
// embeds bytes.Buffer, whose ReadFrom io.Copy would use instead of Write.
type cappedBuffer struct {
	buf      bytes.Buffer
	limit    int
	overflow bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if room := b.limit - b.buf.Len(); room < len(p) {
		b.overflow = true
		if room > 0 {
			b.buf.Write(p[:room])
		}
		return len(p), nil
	}
	return b.buf.Write(p)
}

func (b *cappedBuffer) String() string { return b.buf.String() }

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

	command := c.Command
	if strings.ContainsAny(command, `/\`) && !filepath.IsAbs(command) {
		// A relative path names a file beside the API, not inside the call's folder.
		absolute, err := filepath.Abs(command)
		if err != nil {
			return "", fmt.Errorf("%s: %w", c.Command, err)
		}
		command = absolute
	}
	dir, err := os.MkdirTemp(c.Dir, "jobpilot-cli-")
	if err != nil {
		return "", fmt.Errorf("%s: no working folder: %w", c.Command, err)
	}
	defer func() {
		// Windows keeps a folder a leftover child still runs in; say so rather than lose it.
		if err := os.RemoveAll(dir); err != nil {
			slog.Warn("the CLI's working folder was not removed", "folder", dir, "error", err)
		}
	}()

	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = dir
	cmd.Env = cliEnv(os.Environ())
	// A child the CLI started can hold the pipes open after a timeout; stop waiting for it.
	cmd.WaitDelay = c.waitDelay
	if cmd.WaitDelay <= 0 {
		cmd.WaitDelay = 5 * time.Second
	}
	if !inline {
		cmd.Stdin = strings.NewReader(text)
	}
	stdout, stderr := &cappedBuffer{limit: maxCLIOutput}, &cappedBuffer{limit: maxCLIErrors}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		if errors.Is(err, exec.ErrWaitDelay) {
			// The output may be cut short, so it is no draft.
			return "", fmt.Errorf("%s exited but a process it started kept its output open", c.Command)
		}
		if ctx.Err() != nil {
			return "", fmt.Errorf("%s timed out after %s: %s", c.Command, timeout, truncate(detail, 400))
		}
		return "", fmt.Errorf("%s failed: %s", c.Command, truncate(detail, 400))
	}
	if stdout.overflow {
		return "", fmt.Errorf("%s wrote more than %d bytes; not a draft", c.Command, maxCLIOutput)
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
