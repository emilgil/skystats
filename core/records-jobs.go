package main

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/rs/zerolog/log"
)

// getIntSetting reads an integer user_settings value, returning def on any error.
func getIntSetting(pg *postgres, key string, def int) int {
	var val string
	err := pg.db.QueryRow(context.Background(),
		`SELECT setting_value FROM user_settings WHERE setting_key = $1`, key).Scan(&val)
	if err != nil {
		return def
	}
	n, err := strconv.Atoi(val)
	if err != nil {
		return def
	}
	return n
}

// getStringSetting reads a string user_settings value, returning def on any error.
func getStringSetting(pg *postgres, key, def string) string {
	var val string
	err := pg.db.QueryRow(context.Background(),
		`SELECT setting_value FROM user_settings WHERE setting_key = $1`, key).Scan(&val)
	if err != nil {
		return def
	}
	return val
}

// getBoolSetting reads a boolean user_settings value ("true"/"false"),
// returning def on any error.
func getBoolSetting(pg *postgres, key string, def bool) bool {
	var val string
	err := pg.db.QueryRow(context.Background(),
		`SELECT setting_value FROM user_settings WHERE setting_key = $1`, key).Scan(&val)
	if err != nil {
		return def
	}
	return val == "true"
}

// runLeaderboardSweep deletes records whose first_seen has aged out of their
// period window. all_time is exempt (it only sheds rows via trim-to-100).
func runLeaderboardSweep(pg *postgres) {
	for _, period := range allPeriodTypes {
		window, ok := periodWindow(period)
		if !ok {
			continue // all_time
		}
		cutoff := time.Now().Add(-window)
		ct, err := pg.db.Exec(context.Background(),
			`DELETE FROM records WHERE period_type = $1 AND first_seen < $2`, period, cutoff)
		if err != nil {
			log.Error().Err(err).Msgf("runLeaderboardSweep() - failed for %s", period)
			continue
		}
		log.Debug().Msgf("Leaderboard sweep %s removed %d rows", period, ct.RowsAffected())
	}
}

// clearRecords empties the leaderboards for the given categories, across every
// period_type including all_time. It never touches flight_history: clearing a
// leaderboard does not delete the archived flights that fed it. All categories
// go in one transaction, so a failure part-way leaves every leaderboard intact
// rather than half-cleared.
//
// Cleared rows do not come back. The source flights in aircraft_data stay
// flagged as processed for their category, so ingest only rebuilds the list
// from flights seen after the clear.
func clearRecords(pg *postgres, categories []string) (map[string]int64, error) {
	if err := validateRecordCategories(categories); err != nil {
		return nil, err
	}

	ctx := context.Background()
	tx, err := pg.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) // no-op once committed

	cleared := make(map[string]int64, len(categories))
	for _, category := range categories {
		ct, err := tx.Exec(ctx, `DELETE FROM records WHERE category = $1`, category)
		if err != nil {
			return nil, fmt.Errorf("clearing category %s: %w", category, err)
		}
		cleared[category] += ct.RowsAffected() // += so a repeated key still totals correctly
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return cleared, nil
}

// runHistoryRetention deletes flight_history older than history_retention_days
// that is no longer referenced by any active record. Active records are never
// purged regardless of age.
func runHistoryRetention(pg *postgres) {
	days := getIntSetting(pg, "history_retention_days", 730)
	if days <= 0 {
		days = 730
	}
	cutoff := time.Now().AddDate(0, 0, -days)
	ct, err := pg.db.Exec(context.Background(), `
		DELETE FROM flight_history fh
		WHERE fh.first_seen < $1
		  AND NOT EXISTS (
			SELECT 1 FROM records r
			WHERE r.hex = fh.hex AND r.first_seen = fh.first_seen
		  )`, cutoff)
	if err != nil {
		log.Error().Err(err).Msg("runHistoryRetention() - failed")
		return
	}
	log.Debug().Msgf("History retention removed %d flight_history rows", ct.RowsAffected())
}
