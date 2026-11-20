package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

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
