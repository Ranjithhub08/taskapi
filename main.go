package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"taskapi/handlers"
	"taskapi/llm"
	"taskapi/store"
)

func main() {
	port := getenv("PORT", "8080")
	dbPath := getenv("DB_PATH", "tasks.db")

	// WAL mode and a busy timeout make SQLite friendlier under concurrent requests.
	dsn := "file:" + dbPath + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	st, err := store.New(dsn)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer st.Close()

	api := &handlers.API{Store: st, LLM: llm.NewFromEnv()}

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           api.Routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      90 * time.Second, // longer than the 60 s LLM timeout
		IdleTimeout:       60 * time.Second,
	}

	// Listen for Ctrl+C so we can shut down cleanly.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Printf("listening on http://localhost:%s (llm enabled: %v)", port, api.LLM.Enabled())
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("shutting down...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown: %v", err)
	}
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
