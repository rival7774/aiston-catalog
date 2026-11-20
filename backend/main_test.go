package main

import (
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"
)

func TestCursorRoundTripAndCategoryScope(t *testing.T) {
	want := anchor{7, "", time.Date(2025, 1, 1, 12, 30, 0, 123456000, time.UTC), 123}
	raw, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	got, err := decode(token, 7)
	if err != nil || got != want {
		t.Fatalf("decoded %v, error %v", got, err)
	}
	if _, err = decode(token, 8); err == nil {
		t.Fatal("accepted cursor for another category")
	}
}
