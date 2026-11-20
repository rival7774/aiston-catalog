package main

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"
)

// Set CATALOG_TEST_DSN to a disposable database to exercise repeat startup.
func TestRepeatMigration(t *testing.T) {
	dsn := os.Getenv("CATALOG_TEST_DSN")
	if dsn == "" {
		t.Skip("requires a disposable PostgreSQL database")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	for range 2 {
		if err := migrate(ctx, db); err != nil {
			t.Fatal(err)
		}
	}
	var products, categories, versions int
	err = db.QueryRowContext(ctx, "SELECT (SELECT count(*) FROM product), (SELECT count(*) FROM category), (SELECT count(*) FROM migration)").Scan(&products, &categories, &versions)
	if err != nil {
		t.Fatal(err)
	}
	if products != 200000 || categories != 200 || versions != 1 {
		t.Fatalf("unexpected seed counts: products=%d categories=%d migrations=%d", products, categories, versions)
	}
}
