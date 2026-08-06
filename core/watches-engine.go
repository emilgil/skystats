package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog/log"
)

// watchMatchGrace is how long a match survives without being re-confirmed
// before it is considered over. It mirrors the 10-minute window
// getAircraftsRecentlySeen uses to decide that a new aircraft_data row is a new
// sighting, so "new sighting" and "new notification" agree, and it absorbs the
// occasional tick where readsb drops an aircraft from the feed.
const watchMatchGrace = 10 * time.Minute

// watchMatchRefreshTicks is how many ingest ticks pass between refreshes of the
// matched_at column. At the 2s tick that is one minute — comfortably shorter
// than watchMatchGrace, so a match that is still live when the daemon stops is
// never mistaken on restart for one that expired while it was down.
const watchMatchRefreshTicks = 30

// watchNotifyCap is the most Apprise sends a single tick may trigger. A broad
// watch ("distance under 100 km") can start matching every aircraft in range at
// once; without a cap that is one push per aircraft, all at the same instant.
// Every match is still written to watch_active_matches and to the hit history
// regardless — only the push is dropped — so the Watches tab still shows the
// full picture.
const watchNotifyCap = 50

// watchTickCount drives the periodic matched_at refresh. evaluateWatches runs
// only on the single ingest-tick goroutine, so a plain counter is enough and no
// second ticker is needed.
var watchTickCount int

// diffMatches compares this tick's match set against the previous state.
// started is everything newly matching (one notification each); ended is
// everything that has not been re-confirmed within grace.
func diffMatches(current map[watchKey]bool, previous map[watchKey]time.Time, now time.Time, grace time.Duration) (started, ended []watchKey) {

	for key := range current {
		if _, active := previous[key]; !active {
			started = append(started, key)
		}
	}

	for key, lastMatched := range previous {
		if current[key] {
			continue
		}
		if now.Sub(lastMatched) > grace {
			ended = append(ended, key)
		}
	}

	return started, ended
}

// buildWatchSubject flattens one aircraft into the value set conditions are
// evaluated against. Live readsb values win over database enrichment, which is
// only a fallback for what the feed does not carry.
func buildWatchSubject(a Aircraft, e aircraftEnrichment, distanceKm float64, hasPosition, firstSeenEver bool) watchSubject {

	s := watchSubject{
		Hex:             a.Hex,
		Callsign:        a.Flight,
		Registration:    firstNonEmpty(a.R, stringValue(e.Registration)),
		TypeCode:        firstNonEmpty(a.T, stringValue(e.IcaoType)),
		Model:           firstNonEmpty(a.Desc, stringValue(e.AircraftType)),
		Manufacturer:    stringValue(e.Manufacturer),
		Country:         stringValue(e.CountryName),
		Airline:         stringValue(e.AirlineName),
		Squawk:          a.Squawk,
		DistanceKm:      distanceKm,
		HasPosition:     hasPosition,
		AltitudeFt:      float64(a.AltBaro),
		HasAltitude:     a.AltBaro != 0,
		SpeedKt:         a.Gs,
		HasSpeed:        a.Gs != 0,
		VerticalRateFpm: float64(a.BaroRate),
		FirstSeenEver:   firstSeenEver,
	}

	s.AirlineCodes = nonEmptyValues(stringValue(e.AirlineIcao), stringValue(e.AirlineIata))
	s.Origin = nonEmptyValues(stringValue(e.OriginIcao), stringValue(e.OriginIata))
	s.Destination = nonEmptyValues(stringValue(e.DestinationIcao), stringValue(e.DestinationIata))

	return s
}

func nonEmptyValues(values ...string) []string {
	var out []string
	for _, v := range values {
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

// initWatchEngine primes the in-memory match state from Postgres so a restart
// does not re-notify for aircraft that were already matching.
func initWatchEngine(pg *postgres) {
	activeMatchCache.load(pg, time.Now(), watchMatchGrace)
}

// planWatchSends decides which of one tick's started matches actually get an
// Apprise push. Everything up to capacity is sent; the rest are suppressed and
// summarised in the returned warning (empty when nothing was suppressed).
//
// Allocation is round-robin across watches rather than first-come, so one broad
// rule flooding a tick cannot starve a precise rule the user cares more about.
// capacity <= 0 disables the cap.
func planWatchSends(started []watchKey, names map[int]string, capacity int) (map[watchKey]bool, string) {

	send := make(map[watchKey]bool, len(started))

	if capacity <= 0 || len(started) <= capacity {
		for _, key := range started {
			send[key] = true
		}
		return send, ""
	}

	order := make([]int, 0, len(started))
	pending := map[int][]watchKey{}
	for _, key := range started {
		if _, seen := pending[key.WatchID]; !seen {
			order = append(order, key.WatchID)
		}
		pending[key.WatchID] = append(pending[key.WatchID], key)
	}
	sort.Ints(order)

	for len(send) < capacity {
		progressed := false
		for _, id := range order {
			queue := pending[id]
			if len(queue) == 0 {
				continue
			}
			send[queue[0]] = true
			pending[id] = queue[1:]
			progressed = true
			if len(send) >= capacity {
				break
			}
		}
		if !progressed {
			break
		}
	}

	type suppression struct {
		id    int
		count int
	}
	var suppressed []suppression
	total := 0
	for id, queue := range pending {
		if len(queue) == 0 {
			continue
		}
		suppressed = append(suppressed, suppression{id: id, count: len(queue)})
		total += len(queue)
	}
	if total == 0 {
		return send, ""
	}
	sort.Slice(suppressed, func(i, j int) bool {
		if suppressed[i].count != suppressed[j].count {
			return suppressed[i].count > suppressed[j].count
		}
		return suppressed[i].id < suppressed[j].id
	})

	parts := make([]string, 0, len(suppressed))
	for _, s := range suppressed {
		name := names[s.id]
		if name == "" {
			name = fmt.Sprintf("watch %d", s.id)
		}
		parts = append(parts, fmt.Sprintf("%q: %d", name, s.count))
	}

	warning := fmt.Sprintf(
		"Watch notification cap of %d reached: %d of %d pushes suppressed this tick (%s). Every match was still recorded and is visible in the Watches tab.",
		capacity, total, len(started), strings.Join(parts, ", "))

	return send, warning
}

const insertActiveMatchSQL = `
	INSERT INTO watch_active_matches (watch_id, hex, matched_at)
	VALUES ($1, $2, $3)
	ON CONFLICT (watch_id, hex) DO NOTHING`

// persistStartedMatches writes one watch_active_matches row per started match
// and returns only the keys whose row is durably committed — the caller may
// notify for those and no others.
func persistStartedMatches(pg *postgres, started []watchKey, now time.Time) []watchKey {

	if len(started) == 0 {
		return nil
	}

	batch := &pgx.Batch{}
	for _, key := range started {
		batch.Queue(insertActiveMatchSQL, key.WatchID, key.Hex, now)
	}

	br := pg.db.SendBatch(context.Background(), batch)
	var batchErr error
	for range started {
		if _, err := br.Exec(); err != nil && batchErr == nil {
			batchErr = err
		}
	}
	if err := br.Close(); err != nil && batchErr == nil {
		batchErr = err
	}

	if batchErr == nil {
		return started
	}

	// pgx runs a batch inside one implicit transaction, so a single failing
	// statement rolls back every statement in it: a per-statement nil from
	// Exec() is not proof that row survived. Rather than risk notifying for a
	// match Postgres discarded, redo the tick's inserts one at a time, where
	// each Exec is its own transaction and success really is per key. The
	// insert is idempotent (ON CONFLICT DO NOTHING), so redoing it is free.
	log.Warn().Err(batchErr).Msg("evaluateWatches() - batched match insert failed, retrying one at a time")

	confirmed := make([]watchKey, 0, len(started))
	for _, key := range started {
		if _, err := pg.db.Exec(context.Background(), insertActiveMatchSQL, key.WatchID, key.Hex, now); err != nil {
			log.Error().Err(err).Msgf("evaluateWatches() - unable to record match for watch %d / %s", key.WatchID, key.Hex)
			continue
		}
		confirmed = append(confirmed, key)
	}
	return confirmed
}

// refreshMatchTimestamps stamps every still-matching key as confirmed now, in
// one statement, so matched_at in Postgres is a genuine last-seen time rather
// than the moment the match first started. load() depends on that to tell a
// live match from one that ended during downtime.
func refreshMatchTimestamps(pg *postgres, keys map[watchKey]bool, now time.Time) {

	if len(keys) == 0 {
		return
	}

	watchIDs := make([]int32, 0, len(keys))
	hexes := make([]string, 0, len(keys))
	for key := range keys {
		watchIDs = append(watchIDs, int32(key.WatchID))
		hexes = append(hexes, key.Hex)
	}

	_, err := pg.db.Exec(context.Background(), `
		UPDATE watch_active_matches m
		SET matched_at = $1
		FROM unnest($2::int[], $3::text[]) AS k(watch_id, hex)
		WHERE m.watch_id = k.watch_id AND m.hex = k.hex`, now, watchIDs, hexes)
	if err != nil {
		log.Error().Err(err).Msg("evaluateWatches() - unable to refresh match timestamps")
	}
}

// firstSeen is the process-wide first-sighting tracker, driven by the ingest
// tick.
var firstSeen = newFirstSeenTracker()

// pendingWatchNotifications holds started matches whose notification is waiting
// for the callsign and route to arrive. Like evaluateWatches itself it is only
// ever touched from the single ingest-tick goroutine, so it needs no lock.
var pendingWatchNotifications = newPendingWatchQueue()

// releasableEntries drops pending notifications whose watch disappeared while
// they waited. A deleted or disabled watch is no longer in watchByID, and its
// history row would fail the watch_id foreign key anyway — so the entry is
// discarded before it can spend any of the per-tick send cap.
func releasableEntries(released []pendingWatchNotification, watchByID map[int]Watch) []pendingWatchNotification {

	kept := make([]pendingWatchNotification, 0, len(released))
	for _, e := range released {
		if _, ok := watchByID[e.Key.WatchID]; !ok {
			log.Debug().Msgf("Dropping pending notification for watch %d / %s, which no longer exists", e.Key.WatchID, e.Key.Hex)
			continue
		}
		kept = append(kept, e)
	}
	return kept
}

// evaluateWatches matches the current readsb snapshot against every enabled
// watch and fires one notification per match that starts. Called from the 2s
// ingest tick with the snapshot and enrichment it has already fetched.
func evaluateWatches(pg *postgres, aircraft []Aircraft, enrichment map[string]aircraftEnrichment) {

	watches := watchCache.enabled(pg)
	now := time.Now()

	hexes := make([]string, 0, len(aircraft))
	for _, a := range aircraft {
		hexes = append(hexes, a.Hex)
	}

	// known_aircraft must be maintained even with no watches configured, so
	// first_seen_ever is correct for whatever the user creates later.
	brandNew := markKnownAircraft(pg, hexes)
	firstSeenNow := firstSeen.update(hexes, brandNew, now, watchMatchGrace)

	if len(watches) == 0 {
		return
	}

	subjects := make(map[string]watchSubject, len(aircraft))
	for _, a := range aircraft {
		hasPosition := a.Lat != 0 || a.Lon != 0
		distance := 0.0
		if hasPosition {
			distance = *getDistance([]float64{a.Lon, a.Lat})
		}
		subjects[a.Hex] = buildWatchSubject(a, enrichment[a.Hex], distance, hasPosition, firstSeenNow[a.Hex])
	}

	current := map[watchKey]bool{}
	for _, w := range watches {
		for hex, subject := range subjects {
			if matchWatch(w, subject) {
				current[watchKey{WatchID: w.ID, Hex: hex}] = true
			}
		}
	}

	previous := activeMatchCache.begin()
	started, ended := diffMatches(current, previous, now, watchMatchGrace)

	startedSet := make(map[watchKey]bool, len(started))
	for _, key := range started {
		startedSet[key] = true
	}

	// The cache must only ever reflect what Postgres actually holds, so it is
	// built up from confirmed writes below rather than from `current`
	// directly. Continuing matches (in current but not newly started) already
	// have their row in Postgres from an earlier tick, so they are seeded in
	// unconditionally; their timestamp is simply refreshed by apply().
	//
	// A started match is added only once persistStartedMatches has confirmed
	// its INSERT, and only then is the notification fired (or, past the per-tick
	// cap, its history row written). If the INSERT fails, the key is left
	// out of both the cache and the notification for this tick: the next tick
	// still finds it missing from the cache, so diffMatches reports it as
	// started again and retries the INSERT. That is the only way to avoid
	// notifying for a match Postgres never durably recorded, while never
	// leaving a real match un-notified for good.
	//
	// Symmetrically, an ended match is only dropped from the cache once its
	// DELETE has succeeded; a failed DELETE leaves the key exactly where it
	// was, so the row (and the cache) still agree and the next tick retries.
	persisted := make(map[watchKey]bool, len(current))
	for key := range current {
		if !startedSet[key] {
			persisted[key] = true
		}
	}

	watchByID := make(map[int]Watch, len(watches))
	names := make(map[int]string, len(watches))
	for _, w := range watches {
		watchByID[w.ID] = w
		names[w.ID] = w.Name
	}

	insertable := make([]watchKey, 0, len(started))
	for _, key := range started {
		if _, ok := watchByID[key.WatchID]; ok {
			insertable = append(insertable, key)
		}
	}

	confirmed := persistStartedMatches(pg, insertable, now)

	// The notification config is read once per tick and handed to every send.
	// Loading it inside each NotifyWatch would be 15 QueryRow calls per started
	// match — 2400 of them on a pool of four connections when a broad watch
	// starts matching every aircraft in range at once. It is also needed when
	// nothing started but the queue still holds entries that may release now.
	var cfg NotificationConfig
	if notifier != nil && (len(confirmed) > 0 || pendingWatchNotifications.len() > 0) {
		cfg = notifier.loadConfig()
	}

	// A started match is queued, not notified. Its watch_active_matches row is
	// already written, so diffMatches can never report it again — from here the
	// queue is the only thing that will ever notify for it. A full queue fails
	// open: the entry is released on the spot rather than lost.
	delay := time.Duration(cfg.DelaySeconds) * time.Second
	var released []pendingWatchNotification
	for _, key := range confirmed {
		persisted[key] = true
		log.Info().Msgf("Watch %q matched %s", names[key.WatchID], key.Hex)

		if !pendingWatchNotifications.enqueue(key, subjects[key.Hex], now, delay) {
			log.Warn().Msgf("Pending notification queue is full, sending for %s without waiting", key.Hex)
			released = append(released, pendingWatchNotification{
				Key:      key,
				Subject:  subjects[key.Hex],
				QueuedAt: now,
			})
		}
	}

	released = append(released, pendingWatchNotifications.refresh(subjects, now)...)

	if notifier != nil && len(released) > 0 {
		sendableEntries := releasableEntries(released, watchByID)

		// The cap now bounds what is actually pushed on a tick rather than what
		// matched on it. A broad watch that starts matching 200 aircraft queues
		// all 200 and is trimmed here when they release — the same outcome as
		// before, moved to the moment it describes.
		keys := make([]watchKey, 0, len(sendableEntries))
		for _, e := range sendableEntries {
			keys = append(keys, e.Key)
		}
		sendable, capWarning := planWatchSends(keys, names, watchNotifyCap)
		if capWarning != "" {
			log.Warn().Msg(capWarning)
		}

		for _, e := range sendableEntries {
			log.Info().Msgf("Watch %q releasing notification for %s after %.1fs (callsign=%q route=%t gone=%t)",
				names[e.Key.WatchID], e.Key.Hex, now.Sub(e.QueuedAt).Seconds(),
				strings.TrimSpace(e.Subject.Callsign), hasRoute(e.Subject), e.LeftCoverage)

			go notifier.NotifyWatch(cfg, watchByID[e.Key.WatchID], e.Subject, sendable[e.Key], e.LeftCoverage)
		}
	}

	removed := make([]watchKey, 0, len(ended))
	for _, key := range ended {
		_, err := pg.db.Exec(context.Background(), `
			DELETE FROM watch_active_matches WHERE watch_id = $1 AND hex = $2`, key.WatchID, key.Hex)
		if err != nil {
			log.Error().Err(err).Msgf("evaluateWatches() - unable to clear match for watch %d / %s", key.WatchID, key.Hex)
			continue
		}
		removed = append(removed, key)
	}

	watchTickCount++
	if watchTickCount%watchMatchRefreshTicks == 0 {
		refreshMatchTimestamps(pg, persisted, now)
	}

	activeMatchCache.apply(persisted, removed, now)
}
