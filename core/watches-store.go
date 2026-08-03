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
// diff without a round trip. Postgres stays the durable record and its
// matched_at column is a real last-confirmed timestamp — evaluateWatches
// refreshes it periodically — so a restart can tell a match that was still live
// when the daemon went down from one that expired while it was down.
//
// gen is the per-watch generation guard, mirroring watchStore.gen: forget()
// bumps a watch's generation, begin() records the generations one tick starts
// from, and apply() refuses to write back keys whose watch has moved on since.
// Without it a forget() landing between begin() and apply() would be undone by
// apply() re-inserting continuing matches the transaction had just deleted, and
// the edited watch's aircraft would never get its promised fresh notification.
// The captured generations live on the store rather than being passed back in
// so that begin() and apply() read them under the same mutex; evaluateWatches
// is the only caller and runs on the single ingest-tick goroutine, so exactly
// one begin()/apply() pair is ever in flight.
type activeMatchStore struct {
	mu      sync.Mutex
	matches map[watchKey]time.Time
	gen     map[int]uint64
	tickGen map[int]uint64
}

func newActiveMatchStore() *activeMatchStore {
	return &activeMatchStore{
		matches: map[watchKey]time.Time{},
		gen:     map[int]uint64{},
		tickGen: map[int]uint64{},
	}
}

var activeMatchCache = newActiveMatchStore()

// forget drops every cached match for a watch, so a rewritten or deleted watch
// cannot leave stale state behind, and bumps that watch's generation so a tick
// already in flight cannot put the keys back.
func (s *activeMatchStore) forget(watchID int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key := range s.matches {
		if key.WatchID == watchID {
			delete(s.matches, key)
		}
	}
	if s.gen == nil {
		s.gen = map[int]uint64{}
	}
	s.gen[watchID]++
}

// loadedMatch is one watch_active_matches row as read at startup.
type loadedMatch struct {
	Key       watchKey
	MatchedAt time.Time
}

// partitionLoadedMatches splits the persisted matches into those still inside
// the grace window — which keep their real matched_at, so a match that was live
// when the daemon stopped is not re-notified — and those whose last
// confirmation is older than the window. The latter represent matches that
// ended while the daemon was down: they must not seed the cache, or the
// aircraft's next sighting would be silently swallowed as "already matching".
func partitionLoadedMatches(rows []loadedMatch, now time.Time, grace time.Duration) (map[watchKey]time.Time, []watchKey) {

	active := make(map[watchKey]time.Time, len(rows))
	var expired []watchKey

	for _, row := range rows {
		if now.Sub(row.MatchedAt) > grace {
			expired = append(expired, row.Key)
			continue
		}
		active[row.Key] = row.MatchedAt
	}

	return active, expired
}

// load replaces the cache from Postgres at startup, dropping matches that
// expired while the daemon was down and deleting their rows so table and cache
// stay in step.
func (s *activeMatchStore) load(pg *postgres, now time.Time, grace time.Duration) {

	rows, err := pg.db.Query(context.Background(), `SELECT watch_id, hex, matched_at FROM watch_active_matches`)
	if err != nil {
		log.Error().Err(err).Msg("activeMatchStore.load() - query failed")
		return
	}
	defer rows.Close()

	persisted := []loadedMatch{}
	for rows.Next() {
		var row loadedMatch
		if err := rows.Scan(&row.Key.WatchID, &row.Key.Hex, &row.MatchedAt); err != nil {
			log.Error().Err(err).Msg("activeMatchStore.load() - error scanning rows")
			continue
		}
		persisted = append(persisted, row)
	}
	if err := rows.Err(); err != nil {
		log.Error().Err(err).Msg("activeMatchStore.load() - row iteration failed")
	}
	rows.Close()

	loaded, expired := partitionLoadedMatches(persisted, now, grace)

	if len(expired) > 0 {
		// now.Sub(matched_at) > grace is exactly matched_at < now-grace, so one
		// statement clears precisely the rows partitionLoadedMatches dropped.
		cutoff := now.Add(-grace)
		if _, err := pg.db.Exec(context.Background(),
			`DELETE FROM watch_active_matches WHERE matched_at < $1`, cutoff); err != nil {
			log.Error().Err(err).Msg("activeMatchStore.load() - unable to clear expired matches")
		} else {
			log.Info().Msgf("Dropped %d watch matches that expired while the daemon was down", len(expired))
		}
	}

	s.mu.Lock()
	s.matches = loaded
	s.mu.Unlock()

	log.Debug().Msgf("Loaded %d active watch matches", len(loaded))
}

// begin opens a tick: it returns a copy of the current match state and records
// the per-watch generations that state was taken at, so the matching apply()
// can tell whether a forget() landed in between.
func (s *activeMatchStore) begin() map[watchKey]time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.tickGen = make(map[int]uint64, len(s.gen))
	for id, g := range s.gen {
		s.tickGen[id] = g
	}

	out := make(map[watchKey]time.Time, len(s.matches))
	for k, v := range s.matches {
		out[k] = v
	}
	return out
}

// snapshot returns a copy of the current match state without opening a tick.
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
// its timestamp refreshed, everything ended is dropped. Keys belonging to a
// watch that was forgotten since begin() are skipped entirely — that watch's
// rows are gone from Postgres, so caching them would leave the two disagreeing
// for the whole grace window.
func (s *activeMatchStore) apply(current map[watchKey]bool, ended []watchKey, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key := range current {
		if s.gen[key.WatchID] != s.tickGen[key.WatchID] {
			continue
		}
		s.matches[key] = now
	}
	for _, key := range ended {
		delete(s.matches, key)
	}
}

// watchStore caches the enabled watch definitions so the 2s tick does not
// re-read them from Postgres every time. The daemon is the only writer, so
// invalidating on write keeps the cache exact rather than eventually
// consistent — but the fetch in enabled() runs without the lock held, so a
// write can land while it is in flight. gen guards against that: invalidate()
// bumps it, and publish() only commits its fetched snapshot if gen has not
// moved since the fetch started. A discarded snapshot just means the next
// call re-fetches; it never leaves stale data marked as loaded.
type watchStore struct {
	mu      sync.RWMutex
	watches []Watch
	loaded  bool
	gen     uint64
}

var watchCache = &watchStore{}

func (s *watchStore) invalidate() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loaded = false
	s.watches = nil
	s.gen++
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
	gen := s.gen
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

	s.publish(enabled, gen)

	return enabled
}

// publish commits a freshly fetched watch list to the cache, but only if no
// invalidate() happened since the fetch started (gen unchanged). Otherwise the
// snapshot predates a write and is discarded so the cache stays unloaded and
// the next call re-fetches, rather than resurrecting stale data.
func (s *watchStore) publish(watches []Watch, gen uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.gen != gen {
		return
	}
	s.watches = watches
	s.loaded = true
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
