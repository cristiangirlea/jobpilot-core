package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Addr        string
	DatabaseURL string
	JWTSecret   string
	FrontendURL string
	// ATSGreenhouseURL is the Greenhouse board API origin. Tests point it at
	// a fake so a composed application can be exercised end to end.
	ATSGreenhouseURL string
	TokenTTL         time.Duration
	LLM              LLMConfig
}

// LLMConfig selects the optional AI provider. The zero value ("none") keeps the
// product fully deterministic, which is the supported default.
type LLMConfig struct {
	Provider     string
	Model        string
	CLICommand   string
	CLIArgs      []string
	OllamaURL    string
	Timeout      time.Duration
	CallsPerHour int
}

func Load() Config {
	hours, _ := strconv.Atoi(env("TOKEN_TTL_HOURS", "168"))
	return Config{
		Addr:             env("API_ADDR", ":8080"),
		DatabaseURL:      env("DATABASE_URL", "postgres://jobpilot:jobpilot@localhost:5432/jobpilot?sslmode=disable"),
		JWTSecret:        env("JWT_SECRET", "development-only-change-me"),
		FrontendURL:      env("FRONTEND_URL", "http://localhost:3000"),
		ATSGreenhouseURL: env("ATS_GREENHOUSE_URL", "https://boards-api.greenhouse.io"),
		TokenTTL:         time.Duration(hours) * time.Hour,
		LLM:              loadLLM(),
	}
}

func loadLLM() LLMConfig {
	seconds, _ := strconv.Atoi(env("LLM_TIMEOUT_SECONDS", "90"))
	calls, _ := strconv.Atoi(env("LLM_CALLS_PER_HOUR", "120"))
	return LLMConfig{
		Provider:     env("LLM_PROVIDER", "none"),
		Model:        env("LLM_MODEL", ""),
		CLICommand:   env("LLM_CLI_COMMAND", ""),
		CLIArgs:      strings.Fields(env("LLM_CLI_ARGS", "")),
		OllamaURL:    env("LLM_OLLAMA_URL", "http://localhost:11434"),
		Timeout:      time.Duration(seconds) * time.Second,
		CallsPerHour: calls,
	}
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
