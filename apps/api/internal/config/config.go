package config

import (
	"os"
	"strconv"
	"time"
)

type Config struct {
	Addr        string
	DatabaseURL string
	JWTSecret   string
	FrontendURL string
	TokenTTL    time.Duration
}

func Load() Config {
	hours, _ := strconv.Atoi(env("TOKEN_TTL_HOURS", "168"))
	return Config{
		Addr:        env("API_ADDR", ":8080"),
		DatabaseURL: env("DATABASE_URL", "postgres://jobpilot:jobpilot@localhost:5432/jobpilot?sslmode=disable"),
		JWTSecret:   env("JWT_SECRET", "development-only-change-me"),
		FrontendURL: env("FRONTEND_URL", "http://localhost:3000"),
		TokenTTL:    time.Duration(hours) * time.Hour,
	}
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
