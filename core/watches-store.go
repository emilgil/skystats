package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog/log"
)

// watchKey identifies one aircraft's match against one watch.
type watchKey struct {
	WatchID int
	Hex     string
}

// activeMatchStore mirrors watch_active_matches in memory so the 2s tick can
// diff without a round trip. Postgres stays the durable record; the last-seen
// timestamps are in-memory only and reset to "now" on restart, which just gives
// every loaded match a fresh grace window.
type activeMatchStore struct {
	mu      sync.Mutex
	matches map[watchKey]time.Time
}

func newActiveMatchStore() *activeMatchStore {
	return &activeMatchStore{matches: map[watchKey]time.Time{}}
}

var activeMatchCache = newActiveMatchStore()

// forget drops every cached match for a watch, so a rewritten or deleted watch
// cannot leave stale state behind.
func (s *activeMatchStore) forget(watchID int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key := range s.matches {
		if key.WatchID == watchID {
			delete(s.matches, key)
		}
	}
}

// load replaces the cache from Postgres at startup.
func (s *activeMatchStore) load(pg *postgres, now time.Time) {

	rows, err := pg.db.Query(context.Background(), `SELECT watch_id, hex FROM watch_active_matches`)
	if err != nil {
		log.Error().Err(err).Msg("activeMatchStore.load() - query failed")
		return
	}
	defer rows.Close()

	loaded := map[watchKey]time.Time{}
	for rows.Next() {
		var key watchKey
		if err := rows.Scan(&key.WatchID, &key.Hex); err != nil {
			log.Error().Err(err).Msg("activeMatchStore.load() - error scanning rows")
			continue
		}
		loaded[key] = now
	}
	if err := rows.Err(); err != nil {
		log.Error().Err(err).Msg("activeMatchStore.load() - row iteration failed")
	}

	s.mu.Lock()
	s.matches = loaded
	s.mu.Unlock()

	log.Debug().Msgf("Loaded %d active watch matches", len(loaded))
}

// snapshot returns a copy of the current match state.
func (s *activeMatchStore) snapshot() map[watchKey]time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[watchKey]time.Time, len(s.matches))
	for k, v := range s.matches {
		out[k] = v
	}
	return out
}

// apply folds one tick's outcome into the cache: everything still matching gets
// its timestamp refreshed, everything ended is dropped.
func (s *activeMatchStore) apply(current map[watchKey]bool, ended []watchKey, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key := range current {
		s.matches[key] = now
	}
	for _, key := range ended {
		delete(s.matches, key)
	}
}

// watchStore caches the enabled watch definitions so the 2s tick does not
// re-read them from Postgres every time. The daemon is the only writer, so
// invalidating on write keeps the cache exact rather than eventually
// consistent.
type watchStore struct {
	mu      sync.RWMutex
	watches []Watch
	loaded  bool
}

var watchCache = &watchStore{}

func (s *watchStore) invalidate() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loaded = false
	s.watches = nil
}

// enabled returns the enabled watches, loading them on first use and after any
// invalidation. On a load error it returns an empty slice and leaves the cache
// unloaded so the next tick retries.
func (s *watchStore) enabled(pg *postgres) []Watch {

	s.mu.RLock()
	if s.loaded {
		watches := s.watches
		s.mu.RUnlock()
		return watches
	}
	s.mu.RUnlock()

	all, err := listWatches(pg)
	if err != nil {
		log.Error().Err(err).Msg("watchStore.enabled() - unable to load watches")
		return nil
	}

	enabled := make([]Watch, 0, len(all))
	for _, w := range all {
		if w.Enabled && len(w.Conditions) > 0 {
			enabled = append(enabled, w)
		}
	}

	s.mu.Lock()
	s.watches = enabled
	s.loaded = true
	s.mu.Unlock()

	return enabled
}

// listWatches returns every watch with its conditions attached, newest first.
func listWatches(pg *postgres) ([]Watch, error) {

	rows, err := pg.db.Query(context.Background(), `
		SELECT id, name, enabled, combinator, COALESCE(apprise_key, ''), created_at, updated_at
		FROM watches
		ORDER BY created_at DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	watches := []Watch{}
	byID := map[int]int{}

	for rows.Next() {
		var w Watch
		if err := rows.Scan(&w.ID, &w.Name, &w.Enabled, &w.Combinator, &w.AppriseKey, &w.CreatedAt, &w.UpdatedAt); err != nil {
			return nil, err
		}
		w.Conditions = []WatchCondition{}
		byID[w.ID] = len(watches)
		watches = append(watches, w)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if len(watches) == 0 {
		return watches, nil
	}

	condRows, err := pg.db.Query(context.Background(), `
		SELECT id, watch_id, field, operator, value
		FROM watch_conditions
		ORDER BY watch_id, id`)
	if err != nil {
		return nil, err
	}
	defer condRows.Close()

	for condRows.Next() {
		var c WatchCondition
		if err := condRows.Scan(&c.ID, &c.WatchID, &c.Field, &c.Operator, &c.Value); err != nil {
			return nil, err
		}
		if idx, ok := byID[c.WatchID]; ok {
			watches[idx].Conditions = append(watches[idx].Conditions, c)
		}
	}

	return watches, condRows.Err()
}

// getWatch returns a single watch with its conditions.
func getWatch(pg *postgres, id int) (*Watch, error) {

	var w Watch
	err := pg.db.QueryRow(context.Background(), `
		SELECT id, name, enabled, combinator, COALESCE(apprise_key, ''), created_at, updated_at
		FROM watches WHERE id = $1`, id).
		Scan(&w.ID, &w.Name, &w.Enabled, &w.Combinator, &w.AppriseKey, &w.CreatedAt, &w.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	rows, err := pg.db.Query(context.Background(), `
		SELECT id, watch_id, field, operator, value
		FROM watch_conditions WHERE watch_id = $1 ORDER BY id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	w.Conditions = []WatchCondition{}
	for rows.Next() {
		var c WatchCondition
		if err := rows.Scan(&c.ID, &c.WatchID, &c.Field, &c.Operator, &c.Value); err != nil {
			return nil, err
		}
		w.Conditions = append(w.Conditions, c)
	}

	return &w, rows.Err()
}

// createWatch inserts a watch and its conditions in one transaction.
func createWatch(pg *postgres, w Watch) (*Watch, error) {

	ctx := context.Background()
	tx, err := pg.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var id int
	err = tx.QueryRow(ctx, `
		INSERT INTO watches (name, enabled, combinator, apprise_key)
		VALUES ($1, $2, $3, NULLIF($4, ''))
		RETURNING id`, w.Name, w.Enabled, w.Combinator, w.AppriseKey).Scan(&id)
	if err != nil {
		return nil, err
	}

	if err := insertConditions(ctx, tx, id, w.Conditions); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	watchCache.invalidate()
	return getWatch(pg, id)
}

// updateWatch replaces a watch and its full condition list. Conditions are
// deleted and re-inserted rather than diffed: the list is short and a full
// replace keeps the API contract simple.
func updateWatch(pg *postgres, id int, w Watch) (*Watch, error) {

	ctx := context.Background()
	tx, err := pg.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	ct, err := tx.Exec(ctx, `
		UPDATE watches
		SET name = $2, enabled = $3, combinator = $4, apprise_key = NULLIF($5, ''), updated_at = now()
		WHERE id = $1`, id, w.Name, w.Enabled, w.Combinator, w.AppriseKey)
	if err != nil {
		return nil, err
	}
	if ct.RowsAffected() == 0 {
		return nil, nil
	}

	if _, err := tx.Exec(ctx, `DELETE FROM watch_conditions WHERE watch_id = $1`, id); err != nil {
		return nil, err
	}
	if err := insertConditions(ctx, tx, id, w.Conditions); err != nil {
		return nil, err
	}

	// The condition set may have changed, so any aircraft currently counted as
	// matching is no longer trustworthy. Clearing lets the next tick re-decide
	// and notify afresh under the new rules.
	if _, err := tx.Exec(ctx, `DELETE FROM watch_active_matches WHERE watch_id = $1`, id); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	watchCache.invalidate()
	activeMatchCache.forget(id)
	return getWatch(pg, id)
}

func insertConditions(ctx context.Context, tx pgx.Tx, watchID int, conditions []WatchCondition) error {
	for _, c := range conditions {
		_, err := tx.Exec(ctx, `
			INSERT INTO watch_conditions (watch_id, field, operator, value)
			VALUES ($1, $2, $3, $4)`, watchID, c.Field, c.Operator, c.Value)
		if err != nil {
			return fmt.Errorf("insert condition %s/%s: %w", c.Field, c.Operator, err)
		}
	}
	return nil
}

// deleteWatch removes a watch. Conditions and active matches cascade;
// notification history is kept with watch_id set to NULL.
func deleteWatch(pg *postgres, id int) error {

	ct, err := pg.db.Exec(context.Background(), `DELETE FROM watches WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}

	watchCache.invalidate()
	activeMatchCache.forget(id)
	return nil
}
