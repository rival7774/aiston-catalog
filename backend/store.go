package main

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

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
	if err != nil || json.Unmarshal(b, &c) != nil || c.Category != id || c.ID < 1 || c.ID > 2147483647 || c.Created.IsZero() {
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
