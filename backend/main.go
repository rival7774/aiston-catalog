package main

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/lib/pq"
)

//go:embed schema.sql
var schema string

func migrate(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(419020); CREATE TABLE IF NOT EXISTS migration(version integer PRIMARY KEY)"); err != nil {
		return err
	}
	var applied bool
	if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT FROM migration WHERE version=1)").Scan(&applied); err != nil {
		return err
	}
	if !applied {
		if _, err = tx.ExecContext(ctx, schema); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO migration VALUES (1)"); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func run() error {
	db, err := sql.Open("postgres", "")
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(12)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err = migrate(ctx, db); err != nil {
		return fmt.Errorf("initialize database: %w", err)
	}
	port := os.Getenv("API_PORT")
	if port == "" {
		port = "8080"
	}
	srv := http.Server{Addr: ":" + port, Handler: api{db}, ReadHeaderTimeout: 5 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second}
	done := make(chan error, 1)
	go func() { done <- srv.ListenAndServe() }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
	}
	timeout, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	return srv.Shutdown(timeout)
}
func main() {
	if len(os.Args) == 2 && os.Args[1] == "health" {
		client := http.Client{Timeout: 2 * time.Second}
		port := os.Getenv("API_PORT")
		if port == "" {
			port = "8080"
		}
		resp, err := client.Get("http://127.0.0.1:" + port + "/api/health")
		if err != nil {
			os.Exit(1)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			os.Exit(1)
		}
		return
	}
	if err := run(); err != nil {
		log.Fatal(err)
	}
}
