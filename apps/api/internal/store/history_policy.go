package store

import (
	"errors"
	"time"

	"jobpilot/core/api/internal/models"
)

func ValidateHistoryPolicy(x *models.FilterPreset) error {
	if x.HistoryMode == "" {
		x.HistoryMode = "warn"
	}
	if x.HistoryLookback == "" {
		x.HistoryLookback = "all"
	}
	switch x.HistoryMode {
	case "warn", "company", "role":
	default:
		return errors.New("history_mode must be warn, company or role")
	}
	switch x.HistoryLookback {
	case "all", "three_months":
		x.HistorySince = ""
	case "since":
		day, err := time.Parse("2006-01-02", x.HistorySince)
		if err != nil || day.Year() < 1 {
			return errors.New("history_since must be YYYY-MM-DD with a year of at least 1 when history_lookback is since")
		}
	default:
		return errors.New("history_lookback must be all, three_months or since")
	}
	return nil
}
