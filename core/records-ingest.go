package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog/log"
)

// recordCandidate is one flight's contribution to a single leaderboard category.
type recordCandidate struct {
	Hex          string
	Flight       string
	Registration string
	Type         string
	FirstSeen    time.Time
	LastSeen     time.Time
	MetricValue  float64
	Details      map[string]any
}

// upsertFlightHistory merges one flight's known columns into flight_history.
// metricCols maps column name -> value for the columns this pass knows about;
// other columns keep their previous value via COALESCE. Column names come from
// our own code (never user input), so the dynamic SQL is safe.
func upsertFlightHistory(pg *postgres, hex, flight, registration, aircraftType string, firstSeen, lastSeen time.Time, metricCols map[string]any) {
	cols := []string{"hex", "flight", "registration", "type", "first_seen", "last_seen"}
	args := []any{hex, flight, registration, aircraftType, firstSeen, lastSeen}

	names := make([]string, 0, len(metricCols))
	for k := range metricCols {
		names = append(names, k)
	}
	sort.Strings(names) // deterministic column order
	for _, k := range names {
		cols = append(cols, k)
		args = append(args, metricCols[k])
	}

	placeholders := make([]string, len(args))
	for i := range args {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
	}

	updates := []string{"last_seen = EXCLUDED.last_seen"}
	for _, k := range names {
		updates = append(updates, fmt.Sprintf("%s = COALESCE(EXCLUDED.%s, flight_history.%s)", k, k, k))
	}

	query := fmt.Sprintf(
		`INSERT INTO flight_history (%s) VALUES (%s)
		 ON CONFLICT (hex, first_seen) DO UPDATE SET %s`,
		strings.Join(cols, ", "), strings.Join(placeholders, ", "), strings.Join(updates, ", "))

	if _, err := pg.db.Exec(context.Background(), query, args...); err != nil {
		log.Error().Err(err).Msg("upsertFlightHistory() - failed")
	}
}

// writeRecords inserts each candidate into every period bucket whose window
// contains its first_seen (all_time always), then trims each affected bucket
// to maxRows=100. Variant A: insert-then-trim, no threshold pre-gating.
func writeRecords(pg *postgres, category string, candidates []recordCandidate) {
	if len(candidates) == 0 {
		return
	}
	meta, ok := recordCategories[category]
	if !ok {
		log.Error().Msgf("writeRecords() - unknown category %s", category)
		return
	}

	now := time.Now()
	affected := map[string]bool{}
	batch := &pgx.Batch{}
	queued := 0

	insert := `
		INSERT INTO records (category, period_type, hex, flight, registration, type,
			first_seen, last_seen, metric_name, metric_value, details)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (category, period_type, hex, first_seen) DO UPDATE SET
			metric_value = EXCLUDED.metric_value,
			last_seen = EXCLUDED.last_seen,
			details = EXCLUDED.details`

	for _, c := range candidates {
		detailsJSON, err := json.Marshal(c.Details)
		if err != nil {
			log.Error().Err(err).Msg("writeRecords() - marshal details")
			continue
		}
		for _, period := range periodsForFirstSeen(c.FirstSeen, now) {
			batch.Queue(insert,
				category, period, c.Hex, c.Flight, c.Registration, c.Type,
				c.FirstSeen, c.LastSeen, meta.MetricName, c.MetricValue, detailsJSON)
			affected[period] = true
			queued++
		}
	}

	br := pg.db.SendBatch(context.Background(), batch)
	for i := 0; i < queued; i++ {
		if _, err := br.Exec(); err != nil {
			log.Error().Err(err).Msgf("writeRecords() - insert failed (%s)", category)
		}
	}
	br.Close()

	for period := range affected {
		trimRecordsBucket(pg, meta, period, 100)
	}
}

// trimRecordsBucket keeps only the best maxRows rows in one (category, period)
// bucket, deleting the rest. Best-first order comes from the category metadata.
func trimRecordsBucket(pg *postgres, meta recordCategory, period string, maxRows int) {
	query := fmt.Sprintf(`
		DELETE FROM records
		WHERE category = $1 AND period_type = $2
		  AND id NOT IN (
			SELECT id FROM records
			WHERE category = $1 AND period_type = $2
			ORDER BY metric_value %s, first_seen ASC
			LIMIT $3
		  )`, meta.bestFirstSQL())

	if _, err := pg.db.Exec(context.Background(), query, meta.Name, period, maxRows); err != nil {
		log.Error().Err(err).Msgf("trimRecordsBucket() - failed (%s/%s)", meta.Name, period)
	}
}
