package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"jobpilot/core/api/internal/store"
)

// A profile transaction that ran out of time is a 503 that says nothing changed,
// and the other outcomes keep their own status.
func TestRespondRepairStatuses(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code int
		text string
	}{
		{nil, 200, ""},
		{fmt.Errorf("commit: %w", context.DeadlineExceeded), 503, ProfileBusy},
		{&pgconn.PgError{Code: "55P03"}, 503, ProfileBusy},
		{&pgconn.PgError{Code: "57014"}, 503, ProfileBusy},
		{&pgconn.PgError{Code: "25P03"}, 503, ProfileBusy},
		{store.ErrRepairConflict, 409, "Reload and create a new proposal"},
		{store.ErrRepairInvalid, 400, "cannot be proposed"},
		{pgx.ErrNoRows, 404, "Record not found"},
		{&pgconn.PgError{Code: "23505"}, 500, "An internal error occurred"},
		{errors.New("anything else"), 500, "An internal error occurred"},
	} {
		w := httptest.NewRecorder()
		respondRepair(w, map[string]string{}, tc.err)
		if w.Code != tc.code || !strings.Contains(w.Body.String(), tc.text) {
			t.Errorf("%v: got %d %s, want %d %q", tc.err, w.Code, w.Body.String(), tc.code, tc.text)
		}
	}
}

func TestRepairBodyIsStrict(t *testing.T) {
	for body, ok := range map[string]bool{
		`{"decision":"apply"}`:                      true,
		`{"decision":"apply","summary":"a fact"}`:   false,
		`{"decision":"apply"}{"decision":"reject"}`: false,
		`[]`: false,
		``:   false,
		`{"decision":"` + strings.Repeat("x", 70<<10) + `"}`: false,
	} {
		var out struct {
			Decision string `json:"decision"`
		}
		w := httptest.NewRecorder()
		got := repairBody(w, httptest.NewRequest("POST", "/", strings.NewReader(body)), &out)
		if got != ok || (!ok && w.Code != 400) {
			t.Errorf("body %.40q: accepted=%v status=%d", body, got, w.Code)
		}
	}
}
