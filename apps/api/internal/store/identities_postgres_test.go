package store

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"jobpilot/core/api/internal/auth"
	database "jobpilot/core/api/internal/db"
)

// A Google identity is never linked to an account by email alone, because
// sign-up does not verify addresses: an account someone registered in the
// email owner's name must not absorb the owner's Google sign-in. Linking is
// explicit, from the signed-in account; after it the identity is found by its
// subject even when the email changes. A new account comes from Google only
// when sign-up is allowed, and takes no password.
func TestSignInWithIdentityAgainstPostgres(t *testing.T) {
	url := os.Getenv("JOBPILOT_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set JOBPILOT_TEST_DATABASE_URL to a migrated database to run this")
	}
	ctx := context.Background()
	pool, err := database.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	st := New(pool)
	run := time.Now().Format("20060102150405.000000")

	hash, err := auth.Hash("a password of hers")
	if err != nil {
		t.Fatal(err)
	}
	ana, err := st.CreateUser(ctx, "identity-ana-"+run+"@example.test", hash)
	if err != nil {
		t.Fatal(err)
	}
	for _, signup := range []bool{false, true} {
		if _, err := st.SignInWithIdentity(ctx, "google", "sub-ana-"+run, "Identity-Ana-"+run+"@Example.test", signup); !errors.Is(err, ErrLinkRequired) {
			t.Fatalf("signup=%v: an identity was linked by email alone: %v", signup, err)
		}
	}
	if err := st.LinkIdentity(ctx, ana.ID, "google", "sub-ana-"+run, "identity-ana-"+run+"@example.test"); err != nil {
		t.Fatal(err)
	}
	if err := st.LinkIdentity(ctx, ana.ID, "google", "sub-ana-"+run, "identity-ana-"+run+"@example.test"); err != nil {
		t.Fatalf("linking again to the same account: %v", err)
	}
	again, err := st.SignInWithIdentity(ctx, "google", "sub-ana-"+run, "renamed-"+run+"@example.test", false)
	if err != nil || again.ID != ana.ID {
		t.Fatalf("finding the linked identity by subject: %+v, %v", again, err)
	}
	if _, h, err := st.Credentials(ctx, ana.Email); err != nil || !auth.Verify(h, "a password of hers") {
		t.Fatalf("password login stopped working after linking: %v", err)
	}
	bob, err := st.CreateUser(ctx, "identity-bob-"+run+"@example.test", hash)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.LinkIdentity(ctx, bob.ID, "google", "sub-ana-"+run, "identity-ana-"+run+"@example.test"); !errors.Is(err, ErrIdentityTaken) {
		t.Fatalf("an identity linked to Ana was linked to Bob too: %v", err)
	}
	if back, err := st.SignInWithIdentity(ctx, "google", "sub-ana-"+run, "x@example.test", false); err != nil || back.ID != ana.ID {
		t.Fatalf("a refused link moved the identity: %+v, %v", back, err)
	}

	stranger := "identity-new-" + run + "@example.test"
	if _, err := st.SignInWithIdentity(ctx, "google", "sub-new-"+run, stranger, false); !errors.Is(err, ErrNoAccount) {
		t.Fatalf("sign-up off: err = %v, want ErrNoAccount", err)
	}
	if _, _, err := st.Credentials(ctx, stranger); err == nil {
		t.Fatal("an account was created with sign-up off")
	}
	created, err := st.SignInWithIdentity(ctx, "google", "sub-new-"+run, stranger, true)
	if err != nil || created.Email != stranger {
		t.Fatalf("sign-up on: %+v, %v", created, err)
	}
	if !created.EmailVerified {
		t.Fatal("Google vouched for the address, so the account should start verified")
	}
	if _, h, err := st.Credentials(ctx, stranger); err != nil || auth.Verify(h, "") || auth.Verify(h, "anything at all") {
		t.Fatalf("an account created through Google accepts a password (err %v)", err)
	}
	if back, err := st.SignInWithIdentity(ctx, "google", "sub-new-"+run, stranger, false); err != nil || back.ID != created.ID || !back.EmailVerified {
		t.Fatalf("a created account is found again with sign-up off: %+v, %v", back, err)
	}

	// The same subject from another provider is another person.
	if _, err := st.SignInWithIdentity(ctx, "other", "sub-ana-"+run, "nobody-"+run+"@example.test", false); !errors.Is(err, ErrNoAccount) {
		t.Fatalf("a subject matched across providers: %v", err)
	}
}
