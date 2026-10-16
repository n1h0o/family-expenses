package main

import (
	"context"
	"embed"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed schema.sql
var schemaFS embed.FS

// запускает хранилище, API и необязательный Telegram-бот.
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	databaseURL := env(
		"DATABASE_URL",
		"postgres://expenses:expenses-dev@localhost:5432/expenses?sslmode=disable",
	)
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	if err = pool.Ping(ctx); err != nil {
		log.Fatal(err)
	}
	schema, err := schemaFS.ReadFile("schema.sql")
	if err != nil {
		log.Fatal(err)
	}
	if _, err = pool.Exec(ctx, string(schema)); err != nil {
		log.Fatal(err)
	}

	store := NewStore(pool)
	api := NewAPI(store)
	mux := http.NewServeMux()
	api.Register(mux)
	mux.Handle("/", http.FileServer(http.Dir(env("WEB_DIR", "web"))))
	server := &http.Server{
		Addr:              ":" + env("PORT", "8080"),
		Handler:           securityHeaders(mux),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		log.Printf("web/API listening on %s", server.Addr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()

	if token := strings.TrimSpace(os.Getenv("TELEGRAM_BOT_TOKEN")); token != "" {
		bot := NewBot(store, NewLLM(), token, env("TIMEZONE", "Europe/Ulyanovsk"))
		go bot.Run(ctx)
	} else {
		log.Print("TELEGRAM_BOT_TOKEN is empty; Telegram polling is disabled")
	}
	<-ctx.Done()
	shutdown, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdown)
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// добавляет базовые заголовки защиты к каждому ответу.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set(
			"Content-Security-Policy",
			"default-src 'self'; style-src 'self'; script-src 'self'; img-src 'self' data:; connect-src 'self'",
		)
		next.ServeHTTP(w, r)
	})
}
