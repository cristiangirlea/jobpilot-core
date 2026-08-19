package auth

import (
	"testing"
	"time"
)

func TestTokenRoundTrip(t *testing.T) {
	tok, err := Token("abc", "a@example.com", "secret", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	id, err := Parse(tok, "secret")
	if err != nil || id != "abc" {
		t.Fatal(id, err)
	}
	if _, err = Parse(tok, "wrong"); err == nil {
		t.Fatal("expected signature failure")
	}
}
