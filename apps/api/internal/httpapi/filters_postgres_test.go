package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"jobpilot/core/api/internal/auth"
	"jobpilot/core/api/internal/discovery"
	"jobpilot/core/api/internal/models"
)

// Older clients must not disable rules during an unrelated edit.
func TestFilterPutPreservesOmittedGatesPostgres(t *testing.T) {
	st := accountsStore(t)
	ctx := context.Background()
	u, err := st.CreateUser(ctx, "filter-put-"+time.Now().Format("20060102150405.000000")+"@example.test", "not a real hash")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = st.DB.Exec(ctx, `DELETE FROM users WHERE id=$1`, u.ID) })
	const secret = "filter-put-test-secret"
	token, err := auth.Token(u.ID, u.Email, secret, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	h := (&Server{Store: st, Discovery: discovery.NewService(st), Secret: secret, TokenTTL: time.Hour}).Handler()
	for _, method := range []string{http.MethodPost, http.MethodPut} {
		path := "/api/filters"
		if method == http.MethodPut {
			created := call(t, h, http.MethodPost, path, token, `{"name":"Invalid policy test"}`)
			if created.Code != http.StatusCreated {
				t.Fatalf("create: %d %s", created.Code, created.Body)
			}
			var f models.FilterPreset
			if err := json.Unmarshal(created.Body.Bytes(), &f); err != nil {
				t.Fatal(err)
			}
			path += "/" + f.ID
		}
		invalid := call(t, h, method, path, token, `{"name":"Invalid policy","history_mode":"never"}`)
		if invalid.Code != http.StatusUnprocessableEntity {
			t.Fatalf("%s invalid history_mode: %d %s", method, invalid.Code, invalid.Body)
		}
	}
	for _, tc := range []struct {
		name, fields          string
		eligibility, language bool
	}{
		{"omit both", "", true, true},
		{"disable eligibility only", `,"reject_eligibility_requirements":false`, false, true},
		{"disable language only", `,"reject_unlisted_languages":false`, true, false},
		{"disable both", `,"reject_eligibility_requirements":false,"reject_unlisted_languages":false`, false, false},
		{"explicit true", `,"reject_eligibility_requirements":true,"reject_unlisted_languages":true`, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			created := call(t, h, http.MethodPost, "/api/filters", token, `{"name":"Gates","history_mode":"company","history_lookback":"since","history_since":"2026-01-15","reject_eligibility_requirements":true,"reject_unlisted_languages":true}`)
			if created.Code != http.StatusCreated {
				t.Fatalf("create: %d %s", created.Code, created.Body)
			}
			var f models.FilterPreset
			if err := json.Unmarshal(created.Body.Bytes(), &f); err != nil {
				t.Fatal(err)
			}
			updated := call(t, h, http.MethodPut, "/api/filters/"+f.ID, token, `{"name":"Edited","max_age_days":7`+tc.fields+`}`)
			if updated.Code != http.StatusOK {
				t.Fatalf("update: %d %s", updated.Code, updated.Body)
			}
			if err := json.Unmarshal(updated.Body.Bytes(), &f); err != nil {
				t.Fatal(err)
			}
			if f.HistoryMode != "company" || f.HistoryLookback != "since" || f.HistorySince != "2026-01-15" {
				t.Fatalf("response history policy changed: %+v", f)
			}
			if f.RejectEligibilityRequirements != tc.eligibility || f.RejectUnlistedLanguages != tc.language || f.MaxAgeDays != 7 {
				t.Fatalf("response rules changed: %+v", f)
			}
			stored, err := st.Filters(ctx, u.ID)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, got := range stored {
				if got.ID == f.ID {
					found = true
					if got.HistoryMode != "company" || got.HistoryLookback != "since" || got.HistorySince != "2026-01-15" {
						t.Fatalf("stored history policy changed: %+v", got)
					}
					if got.RejectEligibilityRequirements != tc.eligibility || got.RejectUnlistedLanguages != tc.language || got.MaxAgeDays != 7 {
						t.Fatalf("stored rules changed: %+v", got)
					}
				}
			}
			if !found {
				t.Fatal("missing stored filter")
			}
		})
	}
}
