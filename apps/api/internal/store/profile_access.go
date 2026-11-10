package store

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"jobpilot/core/api/internal/models"
)

// The same profile readers and writers run on the pool or inside a transaction.
type profileDB interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}
type profileStore struct{ DB profileDB }

// Every write to one user's profile (fields, roles, skills, projects, excluded
// companies) holds that user's advisory lock for its transaction, so a repair or
// an import can compare the profile and then write it with nothing in between.
// The lock is per user: one account's writes never wait for another's.
const profileLockSQL = `SELECT pg_advisory_xact_lock(hashtextextended('profile:' || $1::text, 0))`

// profileGates queues one user's profile writes in this process before any of
// them takes a pooled connection, so requests waiting for the lock cannot use
// up the pool. A gate exists only while someone holds or waits for it.
var profileGates = struct {
	sync.Mutex
	users map[string]*profileGate
}{users: map[string]*profileGate{}}

type profileGate struct {
	held chan struct{}
	refs int
}

func enterProfileGate(ctx context.Context, u string) (leave func(), err error) {
	profileGates.Lock()
	gate := profileGates.users[u]
	if gate == nil {
		gate = &profileGate{held: make(chan struct{}, 1)}
		profileGates.users[u] = gate
	}
	gate.refs++
	profileGates.Unlock()
	forget := func() {
		profileGates.Lock()
		if gate.refs--; gate.refs == 0 {
			delete(profileGates.users, u)
		}
		profileGates.Unlock()
	}
	select {
	case gate.held <- struct{}{}:
		return func() { <-gate.held; forget() }, nil
	case <-ctx.Done():
		forget()
		return nil, ctx.Err()
	}
}

// writeProfile runs one ordinary profile write under the user's lock.
func (s *Store) writeProfile(ctx context.Context, u string, write func(*profileStore) error) error {
	leave, err := enterProfileGate(ctx, u)
	if err != nil {
		return err
	}
	defer leave()
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err = tx.Exec(ctx, profileLockSQL, u); err != nil {
		return err
	}
	if err = write(&profileStore{tx}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) Profile(c context.Context, u string) (models.Profile, error) {
	return (&profileStore{s.DB}).Profile(c, u)
}
func (s *Store) Experiences(c context.Context, u string) ([]models.WorkExperience, error) {
	return (&profileStore{s.DB}).Experiences(c, u)
}
func (s *Store) Skills(c context.Context, u string) ([]models.Skill, error) {
	return (&profileStore{s.DB}).Skills(c, u)
}
func (s *Store) Projects(c context.Context, u string) ([]models.Project, error) {
	return (&profileStore{s.DB}).Projects(c, u)
}
func (s *Store) Blacklist(c context.Context, u string) ([]models.BlacklistEntry, error) {
	return (&profileStore{s.DB}).Blacklist(c, u)
}

func (s *Store) SaveProfile(c context.Context, u string, p models.Profile) error {
	return s.writeProfile(c, u, func(ps *profileStore) error { return ps.SaveProfile(c, u, p) })
}
func (s *Store) AddExperience(c context.Context, u string, x models.WorkExperience) (models.WorkExperience, error) {
	err := s.writeProfile(c, u, func(ps *profileStore) (e error) { x, e = ps.AddExperience(c, u, x); return })
	return x, err
}
func (s *Store) AddSkill(c context.Context, u string, x models.Skill) (models.Skill, error) {
	err := s.writeProfile(c, u, func(ps *profileStore) (e error) { x, e = ps.AddSkill(c, u, x); return })
	return x, err
}
func (s *Store) AddProject(c context.Context, u string, x models.Project) (models.Project, error) {
	err := s.writeProfile(c, u, func(ps *profileStore) (e error) { x, e = ps.AddProject(c, u, x); return })
	return x, err
}
func (s *Store) AddBlacklist(c context.Context, u string, x models.BlacklistEntry) (models.BlacklistEntry, error) {
	err := s.writeProfile(c, u, func(ps *profileStore) (e error) { x, e = ps.AddBlacklist(c, u, x); return })
	return x, err
}

// ownedTables are the tables DeleteOwned may delete a user's row from.
var ownedTables = map[string]bool{"work_experiences": true, "skills": true, "projects": true, "company_blacklist": true, "filter_presets": true}

func (s *Store) DeleteOwned(c context.Context, table, u, id string) error {
	if !ownedTables[table] {
		return errors.New("invalid table")
	}
	return s.writeProfile(c, u, func(ps *profileStore) error {
		tag, err := ps.DB.Exec(c, fmt.Sprintf(`DELETE FROM %s WHERE id=$1 AND user_id=$2`, table), id, u)
		if err == nil && tag.RowsAffected() == 0 {
			return pgx.ErrNoRows
		}
		return err
	})
}
