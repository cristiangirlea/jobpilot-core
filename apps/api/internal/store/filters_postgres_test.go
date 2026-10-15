package store

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	database "jobpilot/core/api/internal/db"
	"jobpilot/core/api/internal/models"
)

// Filter presets belong to one user. Another user can neither list, change
// nor activate them, and activating a preset deactivates only the owner's
// others. An agent's set_filters relies on this, since it saves by id. This
// runs on a migrated database named by JOBPILOT_TEST_DATABASE_URL, and is
// skipped without one; CI's integration job provides it.
func TestFilterPresetsAgainstPostgres(t *testing.T) {
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
	ana, err := st.CreateUser(ctx, "filters-ana-"+run+"@example.test", "not a real hash")
	if err != nil {
		t.Fatal(err)
	}
	bob, err := st.CreateUser(ctx, "filters-bob-"+run+"@example.test", "not a real hash")
	if err != nil {
		t.Fatal(err)
	}
	preset := func(name string, active bool) models.FilterPreset {
		return models.FilterPreset{Name: name, Active: active, IncludedTitles: []string{"Backend Engineer"}, ExcludedTitleWords: []string{}, Locations: []string{},
			RemoteTypes: []string{"remote"}, Currency: "EUR", EmploymentTypes: []string{}, ExcludedIndustries: []string{}, RequiredSkills: []string{},
			IncludedTechnologies: []string{"Go"}, ExcludedTechnologies: []string{"PHP"}, MaxAgeDays: 30}
	}
	active := func(u string) string {
		f, err := st.ActiveFilter(ctx, u)
		if err != nil {
			t.Fatal(err)
		}
		if f == nil {
			return ""
		}
		return f.ID
	}

	anaFirst, err := st.SaveFilter(ctx, ana.ID, preset("Ana first", true))
	if err != nil {
		t.Fatal(err)
	}
	bobOwn, err := st.SaveFilter(ctx, bob.ID, preset("Bob own", true))
	if err != nil {
		t.Fatal(err)
	}

	// Bob does not see Ana's preset.
	bobs, err := st.Filters(ctx, bob.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(bobs) != 1 || bobs[0].ID != bobOwn.ID {
		t.Fatalf("Bob should see only his own preset, got %+v", bobs)
	}

	// Bob cannot change or activate Ana's preset by its id, and the failed
	// attempt does not deactivate Bob's own preset either.
	stolen := preset("Taken over", true)
	stolen.ID = anaFirst.ID
	stolen.ExcludedTechnologies = []string{"Go"}
	if _, err = st.SaveFilter(ctx, bob.ID, stolen); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("saving another user's preset must find nothing, got %v", err)
	}
	anas, err := st.Filters(ctx, ana.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(anas) != 1 || anas[0].Name != "Ana first" || len(anas[0].ExcludedTechnologies) != 1 || anas[0].ExcludedTechnologies[0] != "PHP" || !anas[0].Active {
		t.Fatalf("Ana's preset must be unchanged, got %+v", anas)
	}
	if active(bob.ID) != bobOwn.ID {
		t.Fatal("a refused save must not deactivate the caller's own preset")
	}

	// Activating a second preset leaves exactly one active, for Ana only.
	anaSecond, err := st.SaveFilter(ctx, ana.ID, preset("Ana second", false))
	if err != nil {
		t.Fatal(err)
	}
	if active(ana.ID) != anaFirst.ID {
		t.Fatal("saving an inactive preset must not change which one is active")
	}
	anaSecond.Active = true
	anaSecond.MaxAgeDays = 7
	if _, err = st.SaveFilter(ctx, ana.ID, anaSecond); err != nil {
		t.Fatal(err)
	}
	if active(ana.ID) != anaSecond.ID {
		t.Fatal("the updated preset should be the active one")
	}
	if anas, err = st.Filters(ctx, ana.ID); err != nil {
		t.Fatal(err)
	}
	activeCount := 0
	for _, f := range anas {
		if f.Active {
			activeCount++
		}
		if f.ID == anaSecond.ID && f.MaxAgeDays != 7 {
			t.Fatalf("the update must be stored, got %+v", f)
		}
	}
	if activeCount != 1 {
		t.Fatalf("exactly one preset may be active, got %d", activeCount)
	}
	if active(bob.ID) != bobOwn.ID {
		t.Fatal("activating Ana's preset must not touch Bob's")
	}
}
