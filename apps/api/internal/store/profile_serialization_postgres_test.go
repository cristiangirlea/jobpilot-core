package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	database "jobpilot/core/api/internal/db"
	"jobpilot/core/api/internal/models"
)

func TestProfileRepairImportSerializationPostgres(t *testing.T) {
	dsn := os.Getenv("JOBPILOT_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires migrated throwaway database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	s := New(pool)
	for _, repairFirst := range []bool{true, false} {
		t.Run(fmt.Sprintf("repair_first_%t", repairFirst), func(t *testing.T) {
			must := func(err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			u, err := s.CreateUser(ctx, fmt.Sprintf("import-race-%d@example.test", time.Now().UnixNano()), "fake")
			must(err)
			must(s.SaveProfile(ctx, u.ID, models.Profile{Name: "Keep name", Email: "keep@example.test", Summary: "Old summary", SalaryExpectation: 12345, NoticePeriod: "Keep notice"}))
			_, err = s.AddExperience(ctx, u.ID, models.WorkExperience{Company: "Old Employer", Title: "Old role"})
			must(err)
			_, err = s.AddBlacklist(ctx, u.ID, models.BlacklistEntry{CompanyName: "Explicit exclusion"})
			must(err)
			_, err = s.AddProject(ctx, u.ID, models.Project{Name: "Keep project", Skills: []string{}})
			must(err)
			source, err := s.SaveCV(ctx, u.ID, models.CVDocument{Kind: "base", Filename: "repair.txt", Format: "text", Parsed: map[string]any{"summary": "Repaired summary", "experiences": []models.WorkExperience{{Company: "Repair Employer", Title: "Engineer"}}}}, []byte("synthetic repair"))
			must(err)
			proposal, err := s.ProposeProfileRepair(ctx, u.ID, source.ID, RepairByBrowser)
			must(err)
			imported, err := s.SaveCV(ctx, u.ID, models.CVDocument{Kind: "base", Filename: "import.txt", Format: "text", Parsed: map[string]any{"phone": "555-0100", "summary": "Must not import", "experiences": []models.WorkExperience{{Company: "Imported Employer", Title: "Developer"}}, "skills": []models.Skill{{Name: "Go"}}}}, []byte("synthetic import"))
			must(err)
			// Stall the first operation after it takes profile locks, at its source row.
			blocker, err := pool.Begin(ctx)
			must(err)
			defer blocker.Rollback(context.Background())
			sourceID := imported.ID
			if repairFirst {
				sourceID = source.ID
			}
			_, err = blocker.Exec(ctx, `SELECT id FROM cv_documents WHERE user_id=$1 AND id=$2 FOR UPDATE`, u.ID, sourceID)
			must(err)
			var blockerPID uint32 = blocker.Conn().PgConn().PID()
			repairDone, importDone := make(chan error, 1), make(chan error, 1)
			repair := func() {
				_, e := s.DecideProfileRepair(ctx, u.ID, proposal.ID, "apply", proposal.BeforeFingerprint)
				repairDone <- e
			}
			doImport := func() {
				_, e := s.ImportCV(ctx, u.ID, imported.ID, CVImportRequest{Fields: []string{"phone"}, ImportExperiences: true, ImportSkills: true})
				importDone <- e
			}
			if repairFirst {
				go repair()
			} else {
				go doImport()
			}
			deadline := time.Now().Add(3 * time.Second)
			var firstPID int
			for firstPID == 0 && time.Now().Before(deadline) {
				err = pool.QueryRow(ctx, `SELECT coalesce((SELECT pid FROM pg_stat_activity WHERE $1::int=ANY(pg_blocking_pids(pid)) LIMIT 1),0)`, blockerPID).Scan(&firstPID)
				must(err)
				if firstPID == 0 {
					time.Sleep(10 * time.Millisecond)
				}
			}
			if firstPID == 0 {
				t.Fatal("first operation did not reach source lock")
			}
			if repairFirst {
				go doImport()
			} else {
				go repair()
			}
			// The second operation must wait for the first, not merely run later:
			// it queues at the user's gate, and neither finishes while the first
			// is held at its source row.
			queued := func() int {
				profileGates.Lock()
				defer profileGates.Unlock()
				if gate := profileGates.users[u.ID]; gate != nil {
					return gate.refs
				}
				return 0
			}
			for deadline = time.Now().Add(5 * time.Second); queued() != 2 && time.Now().Before(deadline); {
				time.Sleep(10 * time.Millisecond)
			}
			if queued() != 2 {
				t.Fatalf("%d operations at the profile gate, want the holder and one waiting", queued())
			}
			select {
			case err = <-repairDone:
				t.Fatalf("repair finished while the first operation was held: %v", err)
			case err = <-importDone:
				t.Fatalf("import finished while the first operation was held: %v", err)
			case <-time.After(700 * time.Millisecond):
			}
			must(blocker.Commit(ctx))
			must(<-importDone)
			repairErr := <-repairDone
			wantSummary := "Repaired summary"
			wantBlacklist := 2
			if repairFirst {
				must(repairErr)
			} else {
				if !errors.Is(repairErr, ErrRepairConflict) {
					t.Fatalf("repair must conflict after import: %v", repairErr)
				}
				wantSummary = "Old summary"
				wantBlacklist = 1
			}
			p, err := s.Profile(ctx, u.ID)
			must(err)
			b, err := s.Blacklist(ctx, u.ID)
			must(err)
			if p.Summary != wantSummary || p.Phone != "555-0100" || p.Name != "Keep name" || p.Email != "keep@example.test" || p.SalaryExpectation != 12345 || p.NoticePeriod != "Keep notice" || len(p.Projects) != 1 || len(p.Experiences) != 2 || len(p.Skills) != 1 || len(b) != wantBlacklist {
				t.Fatal("scoped import lost fields, children or exclusions")
			}
			again, err := s.ImportCV(ctx, u.ID, imported.ID, CVImportRequest{Fields: []string{"phone", "email"}, ImportExperiences: true, ImportSkills: true})
			must(err)
			if again.AddedExperiences != 0 || again.AddedSkills != 0 || again.Profile.Email != "keep@example.test" {
				t.Fatal("duplicate additive import")
			}
		})
	}
}

// The profile lock belongs to one user. Another API process holding it (here a
// bare transaction) makes that user's writes wait in the database, leaves every
// other account alone, and the waiting writes share one pooled connection.
func TestProfileLockIsPerUserPostgres(t *testing.T) {
	dsn := os.Getenv("JOBPILOT_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires migrated throwaway database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	s := New(pool)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	busy, err := s.CreateUser(ctx, fmt.Sprintf("lock-busy-%d@example.test", time.Now().UnixNano()), "fake")
	must(err)
	other, err := s.CreateUser(ctx, fmt.Sprintf("lock-other-%d@example.test", time.Now().UnixNano()), "fake")
	must(err)
	holder, err := pool.Begin(ctx)
	must(err)
	defer holder.Rollback(context.Background())
	_, err = holder.Exec(ctx, profileLockSQL, busy.ID)
	must(err)

	writers := int(pool.Config().MaxConns) + 4
	done := make(chan error, writers)
	for i := 0; i < writers; i++ {
		go func() {
			_, e := s.AddSkill(ctx, busy.ID, models.Skill{Name: fmt.Sprintf("Waiting %d", i)})
			done <- e
		}()
	}
	// One writer reaches the database and waits there for the holder.
	waiting := false
	for deadline := time.Now().Add(3 * time.Second); !waiting && time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		must(pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1::int=ANY(pg_blocking_pids(pid)))`, holder.Conn().PgConn().PID()).Scan(&waiting))
	}
	if !waiting {
		t.Fatal("the user's write did not wait for the profile lock")
	}
	select {
	case err = <-done:
		t.Fatalf("a write finished while the lock was held: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	// The holder and one waiting writer: the rest queue without a connection.
	if held := pool.Stat().AcquiredConns(); held > 2 {
		t.Fatalf("%d connections held while %d writes wait; they must queue before the pool", held, writers)
	}
	// Another account reads and writes its profile at once.
	started := time.Now()
	_, err = s.AddSkill(ctx, other.ID, models.Skill{Name: "Unblocked"})
	must(err)
	must(s.SaveProfile(ctx, other.ID, models.Profile{Summary: "Unblocked"}))
	_, err = s.AddBlacklist(ctx, other.ID, models.BlacklistEntry{CompanyName: "Unblocked Co"})
	must(err)
	_, err = s.Profile(ctx, busy.ID)
	must(err)
	if took := time.Since(started); took > 2*time.Second {
		t.Fatalf("another account waited %s for a lock that is not its own", took)
	}
	must(holder.Commit(ctx))
	for i := 0; i < writers; i++ {
		must(<-done)
	}
	skills, err := s.Skills(ctx, busy.ID)
	must(err)
	if len(skills) != writers {
		t.Fatalf("%d of %d waiting writes landed", len(skills), writers)
	}
	// A write that gives up while queued in this process leaves no trace: the
	// gate goes back to its holder alone and is removed when the holder leaves.
	leave, err := enterProfileGate(ctx, busy.ID)
	must(err)
	short, stop := context.WithTimeout(ctx, 100*time.Millisecond)
	_, err = s.AddSkill(short, busy.ID, models.Skill{Name: "Gave up at the gate"})
	stop()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a queued write that gave up: %v", err)
	}
	profileGates.Lock()
	refs := profileGates.users[busy.ID].refs
	profileGates.Unlock()
	if refs != 1 {
		t.Fatalf("%d references to the gate after a waiter gave up, want the holder's one", refs)
	}
	leave()
	profileGates.Lock()
	_, kept := profileGates.users[busy.ID]
	profileGates.Unlock()
	if kept {
		t.Fatal("the gate outlived its holder")
	}
	// A wait given up in the database leaves the gate usable too.
	holder, err = pool.Begin(ctx)
	must(err)
	defer holder.Rollback(context.Background())
	_, err = holder.Exec(ctx, profileLockSQL, busy.ID)
	must(err)
	short, stop = context.WithTimeout(ctx, 200*time.Millisecond)
	_, err = s.AddSkill(short, busy.ID, models.Skill{Name: "Given up"})
	stop()
	if err == nil {
		t.Fatal("a write went through a held lock")
	}
	must(holder.Rollback(ctx))
	_, err = s.AddSkill(ctx, busy.ID, models.Skill{Name: "After the wait"})
	must(err)
	profileGates.Lock()
	_, left := profileGates.users[busy.ID]
	profileGates.Unlock()
	if left {
		t.Fatal("a gate was left behind")
	}
}

// A profile with long lists is applied and restored exactly, ids and dates included.
func TestProfileRepairLongListsPostgres(t *testing.T) {
	dsn := os.Getenv("JOBPILOT_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires migrated throwaway database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	s := New(pool)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	u, err := s.CreateUser(ctx, fmt.Sprintf("long-lists-%d@example.test", time.Now().UnixNano()), "fake")
	must(err)
	_, err = pool.Exec(ctx, `INSERT INTO company_blacklist(user_id,company_name,normalized_name,reason) SELECT $1,'Excluded '||n,'excluded '||n,'User exclusion' FROM generate_series(1,1500) n`, u.ID)
	must(err)
	_, err = pool.Exec(ctx, `INSERT INTO skills(user_id,name,level,years) SELECT $1,'Old skill '||n,'advanced',n*0.5 FROM generate_series(1,400) n`, u.ID)
	must(err)
	_, err = pool.Exec(ctx, `INSERT INTO work_experiences(user_id,company,company_domain,title,start_date,end_date,current,description) SELECT $1,'Old Co '||n,'old'||n||'.example.test','Role '||n,'2010-01','2011-01',n=1,'Line one'||chr(10)||'Line two' FROM generate_series(1,150) n`, u.ID)
	must(err)
	skills := make([]models.Skill, 120)
	for i := range skills {
		skills[i] = models.Skill{Name: fmt.Sprintf("New skill %d", i), Years: float64(i) / 4}
	}
	doc, err := s.SaveCV(ctx, u.ID, models.CVDocument{Kind: "base", Filename: "long.txt", Format: "text", Parsed: map[string]any{"skills": skills, "experiences": []models.WorkExperience{{Company: "New Co", Title: "Engineer", Current: true, StartDate: "2020-01"}}}}, []byte("synthetic long"))
	must(err)
	state := func() ProfileSnapshot {
		t.Helper()
		bounded, tx, cleanup, e := repairTx(ctx, s, u.ID)
		must(e)
		defer cleanup()
		v, e := snapshot(bounded, tx, u.ID)
		must(e)
		return v
	}
	before := state()
	r, err := s.ProposeProfileRepair(ctx, u.ID, doc.ID, RepairByBrowser)
	must(err)
	applied, err := s.DecideProfileRepair(ctx, u.ID, r.ID, "apply", r.BeforeFingerprint)
	must(err)
	now := state()
	if len(now.Profile.Skills) != 120 || len(now.Profile.Experiences) != 1 || len(now.Blacklist) != 1500+150 || repairHash(now) != applied.AppliedFingerprint {
		t.Fatalf("applied state: %d skills, %d roles, %d exclusions", len(now.Profile.Skills), len(now.Profile.Experiences), len(now.Blacklist))
	}
	_, err = s.DecideProfileRepair(ctx, u.ID, r.ID, "restore", applied.AppliedFingerprint)
	must(err)
	if repairHash(state()) != repairHash(before) {
		t.Fatal("a long profile was not restored exactly")
	}
}

// Deleting a row of one's own keeps its answers now that it runs under the lock.
func TestDeleteOwnedPostgres(t *testing.T) {
	dsn := os.Getenv("JOBPILOT_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires migrated throwaway database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	s := New(pool)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	u, err := s.CreateUser(ctx, fmt.Sprintf("delete-owned-%d@example.test", time.Now().UnixNano()), "fake")
	must(err)
	other, err := s.CreateUser(ctx, fmt.Sprintf("delete-other-%d@example.test", time.Now().UnixNano()), "fake")
	must(err)
	role, err := s.AddExperience(ctx, u.ID, models.WorkExperience{Company: "Owned Co", Title: "Engineer"})
	must(err)
	skill, err := s.AddSkill(ctx, u.ID, models.Skill{Name: "Owned skill"})
	must(err)
	project, err := s.AddProject(ctx, u.ID, models.Project{Name: "Owned project", Skills: []string{"Go"}})
	must(err)
	exclusion, err := s.AddBlacklist(ctx, u.ID, models.BlacklistEntry{CompanyName: "Owned exclusion"})
	must(err)
	var preset string
	must(pool.QueryRow(ctx, `INSERT INTO filter_presets(user_id,name) VALUES($1,'Owned preset') RETURNING id`, u.ID).Scan(&preset))
	if role.ID == "" || skill.ID == "" || project.ID == "" || exclusion.ID == "" || exclusion.NormalizedName == "" {
		t.Fatal("a profile write under the lock returned no id")
	}
	if err = s.DeleteOwned(ctx, "users", u.ID, u.ID); err == nil || err.Error() != "invalid table" {
		t.Fatalf("a table outside the list: %v", err)
	}
	for table, id := range map[string]string{"work_experiences": role.ID, "skills": skill.ID, "projects": project.ID, "company_blacklist": exclusion.ID, "filter_presets": preset} {
		if err = s.DeleteOwned(ctx, table, other.ID, id); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("%s: another account deleted the row: %v", table, err)
		}
		must(s.DeleteOwned(ctx, table, u.ID, id))
		if err = s.DeleteOwned(ctx, table, u.ID, id); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("%s: deleting a missing row: %v", table, err)
		}
	}
	p, err := s.Profile(ctx, u.ID)
	must(err)
	list, err := s.Blacklist(ctx, u.ID)
	must(err)
	if len(p.Experiences)+len(p.Skills)+len(p.Projects)+len(list) != 0 {
		t.Fatal("rows survived their deletion")
	}
}

func TestProfileTransactionBoundsPostgres(t *testing.T) {
	dsn := os.Getenv("JOBPILOT_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires migrated throwaway database")
	}
	ctx := context.Background()
	pool, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	schema := fmt.Sprintf("repair_bounds_%d", time.Now().UnixNano())
	quoted := pgx.Identifier{schema}.Sanitize()
	_, err = pool.Exec(ctx, `CREATE SCHEMA `+quoted)
	must(err)
	defer pool.Exec(ctx, `DROP SCHEMA `+quoted+` CASCADE`)
	for _, table := range []string{"users", "user_profiles", "work_experiences", "skills", "projects", "company_blacklist", "cv_documents"} {
		_, err = pool.Exec(ctx, `CREATE TABLE `+quoted+`.`+pgx.Identifier{table}.Sanitize()+` (LIKE public.`+pgx.Identifier{table}.Sanitize()+` INCLUDING ALL)`)
		must(err)
	}
	config, err := pgxpool.ParseConfig(dsn)
	must(err)
	config.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	isolated, err := pgxpool.NewWithConfig(ctx, config)
	must(err)
	defer isolated.Close()
	s := New(isolated)
	u, err := s.CreateUser(ctx, fmt.Sprintf("bounds-%d@example.test", time.Now().UnixNano()), "fake")
	must(err)
	must(s.SaveProfile(ctx, u.ID, models.Profile{Summary: "Before"}))
	checkRollback := func() {
		t.Helper()
		p, e := s.Profile(ctx, u.ID)
		must(e)
		if p.Summary != "Before" {
			t.Fatal("partial write survived cancellation")
		}
	}
	t.Run("whole transaction deadline across successful statements", func(t *testing.T) {
		bounded, tx, cleanup, e := repairTx(ctx, s, u.ID)
		must(e)
		defer cleanup()
		deadline, ok := bounded.Deadline()
		if !ok || time.Until(deadline) > profileTransactionTimeout {
			t.Fatal("missing total deadline")
		}
		must((&profileStore{tx}).SaveProfile(bounded, u.ID, models.Profile{Summary: "Partial"}))
		for e == nil {
			_, e = tx.Exec(bounded, `SELECT pg_sleep(0.3)`)
		}
		if bounded.Err() != context.DeadlineExceeded {
			t.Fatalf("not total deadline: %v", e)
		}
		if e = tx.Commit(bounded); e == nil {
			t.Fatal("expired transaction committed")
		}
		cleanup()
		checkRollback()
	})
	t.Run("statement timeout", func(t *testing.T) {
		bounded, tx, cleanup, e := repairTx(ctx, s, u.ID)
		must(e)
		defer cleanup()
		must((&profileStore{tx}).SaveProfile(bounded, u.ID, models.Profile{Summary: "Partial"}))
		_, e = tx.Exec(bounded, `SELECT pg_sleep(7)`)
		if e == nil || bounded.Err() != nil {
			t.Fatalf("server statement timeout missing: %v", e)
		}
		cleanup()
		checkRollback()
	})
	t.Run("idle protection", func(t *testing.T) {
		bounded, tx, cleanup, e := repairTx(ctx, s, u.ID)
		must(e)
		defer cleanup()
		must((&profileStore{tx}).SaveProfile(bounded, u.ID, models.Profile{Summary: "Partial"}))
		time.Sleep(5500 * time.Millisecond)
		if e = tx.Commit(bounded); e == nil {
			t.Fatal("idle transaction committed")
		}
		cleanup()
		checkRollback()
	})
	t.Run("caller cancellation", func(t *testing.T) {
		parent, cancel := context.WithCancel(ctx)
		bounded, tx, cleanup, e := repairTx(parent, s, u.ID)
		must(e)
		defer cleanup()
		must((&profileStore{tx}).SaveProfile(bounded, u.ID, models.Profile{Summary: "Partial"}))
		cancel()
		if e = tx.Commit(bounded); e == nil {
			t.Fatal("cancelled transaction committed")
		}
		cleanup()
		checkRollback()
	})
	// SQL error after profile and role writes must roll back the whole import.
	t.Run("import child failure", func(t *testing.T) {
		conn, e := isolated.Acquire(ctx)
		must(e)
		defer conn.Release()
		_, e = conn.Exec(ctx, `CREATE FUNCTION test_fail_import_skill() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.name='Synthetic failure sentinel' THEN RAISE EXCEPTION 'synthetic failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER test_fail_import_skill BEFORE INSERT ON skills FOR EACH ROW EXECUTE FUNCTION test_fail_import_skill()`)
		must(e)
		defer conn.Exec(ctx, `DROP TRIGGER test_fail_import_skill ON skills; DROP FUNCTION test_fail_import_skill()`)
		doc, e := s.SaveCV(ctx, u.ID, models.CVDocument{Kind: "base", Filename: "failure.txt", Format: "text", Parsed: map[string]any{"summary": "Partial", "experiences": []models.WorkExperience{{Title: "Engineer", Company: "Synthetic"}}, "skills": []models.Skill{{Name: "Synthetic failure sentinel"}}}}, []byte("synthetic"))
		must(e)
		if _, e = s.ImportCV(ctx, u.ID, doc.ID, CVImportRequest{ImportExperiences: true, ImportSkills: true}); e == nil {
			t.Fatal("expected import failure")
		}
		checkRollback()
		roles, e := s.Experiences(ctx, u.ID)
		must(e)
		if len(roles) != 0 {
			t.Fatal("partial imported role survived")
		}
	})
}
