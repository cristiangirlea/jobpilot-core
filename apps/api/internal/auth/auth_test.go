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

// An unknown email must cost the same bcrypt work as a wrong password, or the
// response time tells an attacker which emails have accounts. Compared as the
// fastest of a few runs each, so a busy machine slows both sides alike.
func TestCheckLoginDoesTheSameWorkWithoutAnAccount(t *testing.T) {
	PrepareLogin()
	hash, err := Hash("right password")
	if err != nil {
		t.Fatal(err)
	}
	fastest := func(found bool) time.Duration {
		best := time.Duration(1 << 62)
		for range 3 {
			start := time.Now()
			if CheckLogin(hash, found, "wrong password") {
				t.Fatal("a wrong password must not pass")
			}
			best = min(best, time.Since(start))
		}
		return best
	}
	known, unknown := fastest(true), fastest(false)
	if unknown < known/2 {
		t.Fatalf("an unknown email answered in %v against %v for a wrong password; it should run bcrypt too", unknown, known)
	}
	if CheckLogin(dummyHash(), false, "jobpilot: no account has this password") {
		t.Fatal("without an account the answer is always no, even for the dummy's own password")
	}
	if !CheckLogin(hash, true, "right password") {
		t.Fatal("the right password for an existing account passes")
	}
}
