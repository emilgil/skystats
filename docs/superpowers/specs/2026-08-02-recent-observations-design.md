# Design: globalt kort för de senaste observationerna i Record Holders

**Datum:** 2026-08-02
**Status:** Godkänd design, redo för implementationsplan
**Branch (föreslagen):** `feat/recent-observations`
**Bygger på:** period-baserade leaderboards ([[leaderboard-periods-work]]), redan
deployade — `flight_history`/`records`-datamodellen från migration 000012.

## Syfte

Record Holders-fliken (`TabMotionStats.svelte`) visar idag bara de 7
rekord-kategorierna (fastest, slowest, highest, lowest, furthest_flown,
longest_route, most_remaining) — dvs. bara flygningar som toppar något. Vanlig
trafik som aldrig slår ett rekord syns aldrig. Vi vill lägga till **ett globalt
kort** som visar de N senast observerade flygningarna, oavsett kategori.

## Nuvarande läge (utforskat)

- `flight_history` (migration 000012) är ett permanent arkiv över **alla**
  utvärderade flygningar (unik `hex, first_seen`), oavsett om de blev rekord.
  Index finns redan på `first_seen`. Matar redan dagens per-plan-historik i
  aircraft-modalen (`core/aircraft-detail.go:201`,
  `ORDER BY first_seen DESC LIMIT 10`).
- `core/api.go` (`getRecords`, rad ~510) läser istället `records`
  (topp-100-tabellen), delad handler för de 7 befintliga
  `/api/stats/motion/*`-rutterna. Period-validering (`isValidPeriodType`,
  `periodWindow`) och kategori→metrik-kartan ligger i `core/records-meta.go`.
- Frontend: `TabMotionStats.svelte` har en global periodväljare
  (24h/7d/30d/90d/365d/all-time) som styr alla kort. Varje kort är en tunn
  `Motion*.svelte`-wrapper runt gemensamma `MotionStats.svelte` (fetch +
  tabell), listad i `web/src/lib/dashboardCards.js` under `tab: 'motion-stat'`.

## Beslut (från brainstorming)

- **Scope:** en (1) global lista, inte en tilläggslista per kategori.
- **Datakälla:** `flight_history`, en rad = en utvärderad flygning. Sorterad
  `first_seen DESC` (samma ordning som aircraft-modalens observationslista).
- **Placering:** nytt kort i `TabMotionStats.svelte`, **först** i listan (högst
  upp till vänster i grid:en), inte en egen flik.
- **Periodkoppling:** kortet lyder samma globala periodväljare som de andra 7 —
  `?period=` filtrerar `flight_history.first_seen` mot samma fönster som
  `records`-buckets använder. Kan visa färre än N rader vid kort period/låg
  trafik — förväntat.
- **Gräns (N):** återanvänder befintlig setting `record_holder_table_limit`
  (samma cap ≤100 som redan styr de 7 andra korten). Ingen ny setting.
- **Ingen schemaändring, ingen migration** — ren tilläggs-läsfunktion.

## Backend

Ny route `GET /api/stats/motion/recent` i `core/api.go`, samma grupp som de 7
befintliga `/motion/*`-rutterna men en **egen handler** `getRecentObservations`
(inte `getRecords`/`recordCategories`) eftersom listan inte är rankad på en
enskild metrik och inte har `metric_value`/`details`:

```go
stats.GET("/motion/recent", s.getRecentObservations)
```

```go
func (s *APIServer) getRecentObservations(c *gin.Context) {
    period := c.DefaultQuery("period", "all_time")
    if !isValidPeriodType(period) {
        c.JSON(http.StatusBadRequest, gin.H{"error": "invalid period"})
        return
    }

    limit := s.getLimit("record_holder_table_limit")
    if limit > 100 {
        limit = 100
    }

    query := `
        SELECT fh.hex, fh.flight, fh.registration, fh.type, fh.first_seen, fh.last_seen,
               fh.origin_iata_code, fh.destination_iata_code, rt.airline_name
        FROM flight_history fh
        LEFT JOIN route_data rt ON fh.flight = rt.route_callsign
        WHERE ($1::text = 'all_time' OR fh.first_seen >= now() - $2::interval)
        ORDER BY fh.first_seen DESC
        LIMIT $3`
    // $2 byggs från periodWindow(period) i Go (samma helper som getRecords
    // hade behövt om den filtrerade flight_history direkt); all_time skickar
    // ett dummy-intervall som aldrig används pga $1-villkoret.
    ...
}
```

Motsvarar samma valideringar och JOIN-mönster (`route_data` för
`airline_name`) som `getRecords`, men läser `flight_history` istället för
`records` och saknar metrik/rank-fälten. Svarsformen matchar övriga
motion-endpoints (platt array av objekt), så `MotionStats.svelte` kan
återanvändas oförändrad.

## Frontend

- Ny komponent `web/src/components/MotionRecentObservations.svelte`, byggd på
  befintliga `MotionStats.svelte` (ingen ny fetch-logik):

  ```svelte
  <MotionStats
      endpoint="api/stats/motion/recent"
      title="Recent Observations"
      columns={columns}
      icon={IconHistory}
  />
  ```

- Kolumner (samma konvention som `MotionFurthestFlownAircraft.svelte`): **Reg,
  Airline, Type, From, To, First Seen.** Speed/altitude utelämnas medvetet —
  det här är ett allmänt aktivitetsflöde, inte en rankad lista, och
  From/To + First Seen räcker för att identifiera flygningen.
- Klick på rad öppnar samma aircraft-modal som övriga kort
  (`openAircraftModal(hex)`, redan inbyggt i `MotionStats.svelte`).
- Registreras i `web/src/lib/dashboardCards.js` under `tab: 'motion-stat'`,
  **som första posten** (före `motion_fastest`) så den hamnar högst upp till
  vänster i den 2-kolumners grid:en.

## Filer som berörs

- `core/api.go` — ny route + ny handler `getRecentObservations`.
- `web/src/components/MotionRecentObservations.svelte` — ny.
- `web/src/lib/dashboardCards.js` — en ny rad, placerad först i
  `motion-stat`-gruppen.
- Ingen migration, ingen schemaändring, inga nya settings.

## Verifiering

Repot har inga (motsvarande) tester för detta lager. Backend: `go build ./...`
+ `go vet ./...`. Frontend: `npm run build`. Runtime: curl
`/api/stats/motion/recent?period=all_time` (data), `?period=24h` (färre/inga
rader beroende på trafik), ogiltig period → 400. Browser: kortet visas först i
Record Holders-fliken, växlar med periodväljaren, radklick öppnar
aircraft-modalen.

## Utanför scope (YAGNI)

- Egen periodväljare eller egen radgräns för kortet — återanvänder den globala
  väljaren och `record_holder_table_limit`.
- Speed/altitude-kolumner — utelämnade, se ovan.
- Att göra "recent" till en 8:e kategori i `recordCategories`/`records`-tabellen
  — onödigt, `flight_history` är redan hela den kronologiska sanningen och
  kräver ingen trimning/ingest-skrivning för det här syftet.
- Custom/valfritt datumintervall — samma söm som lämnats öppen i
  [[leaderboard-periods-work]], inte del av denna spec.
