package main

import (
	"context"
	"jobpilot/core/api/internal/config"
	database "jobpilot/core/api/internal/db"
	"jobpilot/core/api/internal/store"
	"log"
)

func main() {
	ctx := context.Background()
	pool, err := database.Open(ctx, config.Load().DatabaseURL)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	if err = store.New(pool).SeedFirstUser(ctx); err != nil {
		log.Fatal(err)
	}
	log.Println("sample profile data added to the first user")
}
