package main

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

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

type category struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	HasChildren bool   `json:"hasChildren"`
	Count       int    `json:"count"`
}
type product struct {
	ID         int       `json:"id"`
	CategoryID int       `json:"categoryId"`
	Name       string    `json:"name"`
	Price      int       `json:"priceKopecks"`
	Created    time.Time `json:"createdAt"`
}

func collect[T any](rows *sql.Rows, scan func(*sql.Rows) (T, error)) ([]T, error) {
	defer rows.Close()
	result := []T{}
	for rows.Next() {
		item, err := scan(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}
func respond(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.Print(err)
	}
}
func fail(w http.ResponseWriter, status int, msg string) {
	respond(w, status, map[string]string{"error": msg})
}

func positive(s string) (int, error) {
	n, err := strconv.ParseInt(s, 10, 32)
	if err != nil || n < 1 {
		return 0, errors.New("invalid positive integer")
	}
	return int(n), nil
}

type api struct{ db *sql.DB }

const nodeFields = "c.id,c.name,EXISTS(SELECT FROM category child WHERE child.parent_id=c.id),s.total"

func (a api) nodes(ctx context.Context, id int, path bool) (any, error) {
	query := "SELECT " + nodeFields + " FROM category c JOIN category_size s USING(id) WHERE c.parent_id=NULLIF($1,0) OR ($1=0 AND c.parent_id IS NULL) ORDER BY c.id"
	if path {
		query = "SELECT " + nodeFields + " FROM ancestry p JOIN category c ON c.id=p.ancestor JOIN category_size s USING(id) WHERE p.descendant=$1 ORDER BY p.depth DESC"
	}
	rows, err := a.db.QueryContext(ctx, query, id)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(r *sql.Rows) (category, error) {
		var c category
		err := r.Scan(&c.ID, &c.Name, &c.HasChildren, &c.Count)
		return c, err
	})
}

type anchor struct {
	Category int
	Query    string
	Created  time.Time
	ID       int
}

func decode(s string, id int) (anchor, error) {
	var c anchor
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || json.Unmarshal(b, &c) != nil || c.Category != id || c.ID < 1 || c.Created.IsZero() {
		return c, errors.New("invalid cursor")
	}
	return c, nil
}
func (a api) products(ctx context.Context, id, limit int, search string, c anchor) (any, error) {
	filter := ""
	args := []any{id, limit + 1}
	if c.ID != 0 {
		filter = " AND (created_at,product_id)<($3,$4)"
		args = append(args, c.Created, c.ID)
	}
	if search != "" {
		args = append(args, "%"+strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(search)+"%")
		filter += fmt.Sprintf(" AND name ILIKE $%d ESCAPE '!'", len(args))
	}
	rows, err := a.db.QueryContext(ctx, `SELECT p.id,p.category_id,p.name,p.price_kopecks,p.created_at
 FROM (SELECT product_id,created_at FROM feed WHERE ancestor=$1`+filter+`
 ORDER BY created_at DESC,product_id DESC LIMIT $2) f
 JOIN product p ON p.id=f.product_id ORDER BY f.created_at DESC,f.product_id DESC`, args...)
	if err != nil {
		return nil, err
	}
	items, err := collect(rows, func(r *sql.Rows) (product, error) {
		var p product
		err := r.Scan(&p.ID, &p.CategoryID, &p.Name, &p.Price, &p.Created)
		return p, err
	})
	if err != nil {
		return nil, err
	}
	var next *string
	if len(items) > limit {
		items = items[:limit]
		last := items[limit-1]
		b, err := json.Marshal(anchor{id, search, last.Created, last.ID})
		if err != nil {
			return nil, err
		}
		s := base64.RawURLEncoding.EncodeToString(b)
		next = &s
	}
	return map[string]any{"items": items, "nextCursor": next}, nil
}

func (a api) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		w.Header().Set("Allow", "GET")
		fail(w, 405, "Method not allowed")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()
	route := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/"), "/")
	id := 0
	kind := "children"
	if r.URL.Path == "/api/health" {
		if r.URL.RawQuery != "" {
			fail(w, 400, "Invalid query parameter")
			return
		}
		if a.db.PingContext(ctx) != nil {
			fail(w, 503, "Database unavailable")
			return
		}
		respond(w, 200, map[string]bool{"ok": true})
		return
	}
	if r.URL.Path != "/api/categories" {
		if len(route) != 3 || route[0] != "categories" {
			fail(w, 404, "Unknown endpoint")
			return
		}
		var err error
		id, err = positive(route[1])
		if err != nil {
			fail(w, 400, "Invalid category id")
			return
		}
		kind = route[2]
		if kind != "children" && kind != "path" && kind != "products" {
			fail(w, 404, "Unknown endpoint")
			return
		}
	}
	q, parseErr := url.ParseQuery(r.URL.RawQuery)
	if parseErr != nil {
		fail(w, 400, "Invalid query")
		return
	}
	for key, v := range q {
		if kind != "products" || (key != "cursor" && key != "limit" && key != "q") || len(v) != 1 || v[0] == "" {
			fail(w, 400, "Invalid query parameter")
			return
		}
	}
	search := strings.TrimSpace(q.Get("q"))
	if q.Has("q") && (search == "" || len(search) > 256 || strings.IndexByte(search, 0) >= 0 || !utf8.ValidString(search)) {
		fail(w, 400, "Invalid search text")
		return
	}
	limit := 50
	var cursor anchor
	var err error
	if q.Has("limit") {
		limit, err = positive(q.Get("limit"))
		if err != nil || limit > 100 {
			fail(w, 400, "Limit must be between 1 and 100")
			return
		}
	}
	if q.Has("cursor") {
		cursor, err = decode(q.Get("cursor"), id)
		if err != nil || cursor.Query != search {
			fail(w, 400, "Invalid cursor")
			return
		}
	}
	if id != 0 {
		var exists bool
		err = a.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT FROM category WHERE id=$1)", id).Scan(&exists)
		if err == nil && !exists {
			fail(w, 404, "Category not found")
			return
		}
	}
	var data any
	if err == nil {
		if kind == "products" {
			data, err = a.products(ctx, id, limit, search, cursor)
		} else {
			data, err = a.nodes(ctx, id, kind == "path")
		}
	}
	if err != nil {
		if r.Context().Err() == nil {
			log.Print(err)
		}
		fail(w, 503, "Database unavailable")
		return
	}
	respond(w, 200, data)
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
