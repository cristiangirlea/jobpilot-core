package discovery

import (
	"testing"
	"time"
)

// LockMatching holds one account's matching work back while another pass
// for that account runs, and leaves other accounts alone.
func TestLockMatchingSerializesOneAccount(t *testing.T) {
	s := &Service{}
	unlock := s.LockMatching("ana")
	entered := make(chan string, 2)
	go func() { defer s.LockMatching("ana")(); entered <- "ana" }()
	go func() { defer s.LockMatching("bob")(); entered <- "bob" }()
	select {
	case who := <-entered:
		if who != "bob" {
			t.Fatalf("%s got in while ana's lock was held", who)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("another account was held back")
	}
	select {
	case <-entered:
		t.Fatal("a second pass for ana ran while the first held the lock")
	case <-time.After(100 * time.Millisecond):
	}
	unlock()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("ana's second pass never ran after the unlock")
	}
	var none *Service
	none.LockMatching("ana")() // a server without discovery locks nothing
}
