package store

import (
	"context"

	"jobpilot/core/api/internal/models"
)

func (s *Store) AccountSettings(ctx context.Context, u string) (models.AccountSettings, error) {
	var x models.AccountSettings
	err := s.DB.QueryRow(ctx, `SELECT COALESCE((SELECT quiet_application_days FROM discovery_settings WHERE user_id=$1),10)`, u).Scan(&x.QuietApplicationDays)
	return x, err
}

func (s *Store) SaveAccountSettings(ctx context.Context, u string, days int) (models.AccountSettings, error) {
	var x models.AccountSettings
	// This column stays in place for existing installations, but the preference
	// is independent of admin-only discovery controls and changes no schedule.
	err := s.DB.QueryRow(ctx, `INSERT INTO discovery_settings(user_id,quiet_application_days) VALUES($1,$2)
	 ON CONFLICT(user_id) DO UPDATE SET quiet_application_days=EXCLUDED.quiet_application_days
	 RETURNING quiet_application_days`, u, days).Scan(&x.QuietApplicationDays)
	return x, err
}
