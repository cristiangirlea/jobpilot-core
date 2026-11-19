package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"
)

type config struct {
	apiURL, modelURL, token string
	concurrency, max        int
	timeout                 time.Duration
	dryRun, summary         bool
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := realMain(ctx, os.Args[1:], os.Getenv, os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

// realMain is main without the process, so tests can read the exit code.
func realMain(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	cfg, err := settings(args, getenv, stdout)
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err == nil {
		if cfg.summary {
			err = runSummary(ctx, cfg, stdout)
		} else {
			_, err = run(ctx, cfg, stdout)
		}
	}
	if err != nil {
		if !errors.Is(err, errStopped) {
			fmt.Fprintln(stderr, err)
		}
		return 1
	}
	return 0
}

func settings(args []string, getenv func(string) string, help io.Writer) (config, error) {
	var cfg config
	flags := flag.NewFlagSet("extract", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.Usage = func() {}
	flags.StringVar(&cfg.modelURL, "model-url", "http://127.0.0.1:8081", "Model server origin on this computer")
	flags.IntVar(&cfg.concurrency, "concurrency", 4, "Concurrent model requests (1 to 8)")
	flags.DurationVar(&cfg.timeout, "timeout", 2*time.Minute, "Model request timeout (10s to 15m)")
	flags.IntVar(&cfg.max, "max", 0, "Maximum postings to ask (0 for no limit)")
	flags.BoolVar(&cfg.dryRun, "dry-run", false, "Ask the model without posting answers")
	flags.BoolVar(&cfg.summary, "summary", false, "Print extraction and shadow scoring counts")
	if err := flags.Parse(args); errors.Is(err, flag.ErrHelp) {
		flags.SetOutput(help)
		fmt.Fprintln(help, "Usage: extract [flags]")
		flags.PrintDefaults()
		return cfg, err
	} else if err != nil || flags.NArg() != 0 {
		return cfg, errors.New("invalid arguments (see --help)")
	}
	if cfg.summary && (cfg.dryRun || cfg.max > 0) {
		return cfg, errors.New("summary cannot be combined with dry-run or a positive max")
	}
	if cfg.concurrency < 1 || cfg.concurrency > 8 {
		return cfg, errors.New("concurrency must be between 1 and 8")
	}
	if cfg.timeout < 10*time.Second || cfg.timeout > 15*time.Minute {
		return cfg, errors.New("timeout must be between 10s and 15m")
	}
	if cfg.max < 0 {
		return cfg, errors.New("max must be zero or greater")
	}
	var err error
	cfg.apiURL, err = validateAPIURL(getenv("JOBPILOT_API_URL"))
	if err != nil {
		return cfg, err
	}
	cfg.modelURL, err = validateModelURL(cfg.modelURL)
	if err != nil {
		return cfg, err
	}
	cfg.token, err = readToken(getenv("JOBPILOT_TOKEN"), getenv("JOBPILOT_TOKEN_FILE"))
	return cfg, err
}

func validateAPIURL(raw string) (string, error) {
	u := origin(raw)
	if u == nil || (u.Scheme != "https" && (u.Scheme != "http" || !loopbackHost(u.Hostname()))) {
		return "", errors.New("JOBPILOT_API_URL must be an HTTPS origin or a loopback HTTP origin")
	}
	return u.String(), nil
}

func validateModelURL(raw string) (string, error) {
	u := origin(raw)
	if u == nil || u.Scheme != "http" || !loopbackHost(u.Hostname()) {
		return "", errors.New("model-url must be a loopback HTTP origin")
	}
	return u.String(), nil
}

func origin(raw string) *url.URL {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil || u.ForceQuery || strings.ContainsAny(raw, "?#") {
		return nil
	}
	if path := u.EscapedPath(); path != "" && path != "/" {
		return nil
	}
	if strings.HasPrefix(u.Host, "[") {
		if !strings.Contains(u.Hostname(), ":") || net.ParseIP(u.Hostname()) == nil {
			return nil
		}
	} else if strings.Contains(u.Hostname(), ":") {
		return nil
	}
	if strings.HasSuffix(u.Host, ":") {
		return nil
	}
	if port := u.Port(); port != "" {
		if n, err := strconv.ParseUint(port, 10, 16); err != nil || n == 0 {
			return nil
		}
	}
	u.Path = ""
	return u
}

func loopbackHost(host string) bool {
	return strings.EqualFold(host, "localhost") || net.ParseIP(host).IsLoopback()
}

func readToken(value, path string) (string, error) {
	if value != "" && path != "" {
		return "", errors.New("set only one of JOBPILOT_TOKEN and JOBPILOT_TOKEN_FILE")
	}
	if path == "" {
		if value == "" {
			return "", errors.New("JOBPILOT_TOKEN or JOBPILOT_TOKEN_FILE is required")
		}
		return checkToken(value)
	}
	const limit = 64 << 10
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > limit {
		return "", errors.New("JOBPILOT_TOKEN_FILE must name a readable regular file of at most 64 KiB")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", errors.New("cannot open JOBPILOT_TOKEN_FILE")
	}
	defer file.Close()
	// Bound the read in case the file grows after Stat.
	raw, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || len(raw) > limit {
		return "", errors.New("cannot read JOBPILOT_TOKEN_FILE within 64 KiB")
	}
	value = strings.TrimSuffix(string(raw), "\r\n")
	if len(value) == len(raw) {
		value = strings.TrimSuffix(value, "\n")
	}
	if value == "" {
		return "", errors.New("JOBPILOT_TOKEN_FILE is empty")
	}
	return checkToken(value)
}

// checkToken refuses what cannot be sent as a bearer token, without echoing it.
func checkToken(value string) (string, error) {
	if strings.ContainsFunc(value, func(r rune) bool { return r <= ' ' || r >= 0x7f }) {
		return "", errors.New("the agent token must be printable characters without spaces (check the file's encoding and line ending)")
	}
	return value, nil
}
