# Design: rekordtabeller "Nearest" & "Furthest" (avstånd till mottagaren)

**Datum:** 2026-08-06
**Status:** Godkänd design, redo för implementationsplan
**Branch:** `feat/nearest-furthest-receiver`
**Bygger på:** period-baserade leaderboards ([[leaderboard-periods-work]]), redan
deployade — `flight_history`/`records`-datamodellen från migration 000012.
**Källspec:** `docs/superpowers/specs/2026-08-05-nearest-furthest-receiver-spec.md`

## Syfte

Två nya rekordkort i Record Holders-fliken, i samma stil som Fastest/Slowest/
Highest/Lowest:

- **Nearest** — kortaste 2D-avstånd ett flygplan någonsin uppmätts på till
  mottagaren.
- **Furthest** — längsta 2D-avstånd ett flygplan någonsin uppmätts på till
  mottagaren (den faktiska uppmätta räckvidden).

Ska kunna rensas på samma sätt som övriga rekordtabeller, och gå igenom samma
periodfiltrering (24h/7d/30d/90d/365d/all-time).

## Steg 0 — arkitekturläge (utforskat, avviker från källspecens antagande)

Källspecen bad om en avstämning mellan ett äldre per-tabell-mönster och ett
nyare enhetligt `records`/`flight_history`-mönster. Det enhetliga mönstret är
sedan länge den enda sanningen i koden (migration 000012 körd, de 7 gamla
tabellerna redan droppade i migration 000016). Den frågan är alltså redan
besvarad.

Däremot gjorde källspecen ett antagande som visade sig **fel**: att det redan
finns kod som avgör när en flygning "räknas som avslutad" och skriver
slutvärden till `flight_history`/`records` vid det tillfället. Genomgång av
hela ingest-kedjan (`records-ingest.go`, `stats-motion.go`,
`stats-distance.go`) visar att så inte är fallet — **alla** sju befintliga
kategorier (fastest/slowest/highest/lowest/furthest_flown/longest_route/
most_remaining) skrivs fortfarande med exakt det snapshot-vid-godtycklig-tick-
mönster källspecen varnar för att kopiera för Nearest/Furthest: en periodisk
ticker (120s resp. 300s) skannar `aircraft_data`-rader med en
`*_processed = false`-flagga, oavsett om flygningen fortfarande pågår.

Den enda "flygning är klar"-signal som redan finns någonstans i kodbasen är
den 10-minutersgräns som avgör om en nyss sedd hex hör till samma flygning
eller är en ny (`recentAircraftCache`, 10 minuters sliding expiry, med
`last_seen_epoch > nowEpoch - 600` som DB-fallback i
`getAircraftsRecentlySeen`, `aircraft.go`). Once en hex inte synts på 10
minuter kommer nästa sighting alltid att skapa en ny `aircraft_data`-rad
(nytt `first_seen`) — den gamla raden uppdateras aldrig mer efter det.

Denna design återanvänder exakt den gränsen som "flygning är klar"-villkor
för finalisering (se avsnitt 3 nedan). Detta är den enda punkten där arbetet
avviker från att vara en ren kopia av ett befintligt mönster, och skälet är
just det källspecen påpekar: en flygnings verkliga när/fjärran-extremvärde
kan inträffa när som helst under hela synlighetsfönstret, inte bara vid det
godtyckliga ögonblick en ticker råkar processa raden.

## Avståndsberäkning

2D great-circle mellan mottagarens `LAT`/`LON` och planets senaste kända
position — samma metrik som `LastSeenDistance` redan är
(`getDistance()`/`getRuler().Distance(...)` i `aircraft.go`, cheap-ruler,
samma bibliotek som `RADIUS`-filtreringen). Ingen ny distansfunktion behövs;
den återanvänds rakt av.

**Bearing** (riktning från mottagaren, krävs av källspecen som kontext-fält)
beräknas själv via `cheap-ruler`s inbyggda `Bearing()`-metod, med samma
`CheapRuler`-instans som redan används för avstånd — inte via readsb:s eget
`r_dir`-fält. Skälet: koden litar redan inte på readsb:s motsvarande
`r_dst`-fält för avstånd (den räknar själv istället), och `r_dir` lagras
idag men uppdateras aldrig efter insert av någon annan kod. Att räkna själv
ger samma konsekvens och pålitlighet som redan gäller för avstånd.
`Bearing()` returnerar grader i intervallet (-180, 180]; normaliseras till
[0, 360) grader vid lagring.

## Mätmetod: löpande min/max, inte en engångs-snapshot

**Hot path (var 2:a sekund, `updateExistingAircrafts` i `aircraft.go`).**
Direkt efter raden som redan beräknar `lastSeenDistance := getDistance(...)`
läggs en bearing-beräkning och en jämförelse mot det hittills bästa
värdet, i exakt samma "jämför i Go, skriv alltid tillbaka hela värdet"-stil
som redan används några rader ovanför för `AltBaro`/`Gs` (session-max):

```go
bearing := getBearing([]float64{aircraft.Lon, aircraft.Lat})

if !existingAircraft.MinDistanceReceiver.Valid || lastSeenDistance < existingAircraft.MinDistanceReceiver.Float64 {
    existingAircraft.MinDistanceReceiver = sql.NullFloat64{Float64: lastSeenDistance, Valid: true}
    existingAircraft.MinDistanceReceiverAltitude = sql.NullInt64{Int64: int64(aircraft.AltBaro), Valid: true}
    existingAircraft.MinDistanceReceiverBearing = sql.NullFloat64{Float64: bearing, Valid: true}
}
if !existingAircraft.MaxDistanceReceiver.Valid || lastSeenDistance > existingAircraft.MaxDistanceReceiver.Float64 {
    existingAircraft.MaxDistanceReceiver = sql.NullFloat64{Float64: lastSeenDistance, Valid: true}
    existingAircraft.MaxDistanceReceiverAltitude = sql.NullInt64{Int64: int64(aircraft.AltBaro), Valid: true}
    existingAircraft.MaxDistanceReceiverBearing = sql.NullFloat64{Float64: bearing, Valid: true}
}
```

(Field types sketched above match the existing `sql.NullFloat64`/nullable
convention already used for `LastSeenDistance` etc. in `models.go` — the
implementation plan should confirm exact Go types against the final column
types chosen in the migration.)

`getBearing()` blir en liten syskonfunktion till befintliga `getDistance()`
(samma `getRuler()`-instans). `UPDATE`- och `INSERT`-statements i
`aircraft.go` utökas med de sex nya kolumnerna; vid `INSERT` (ny flygning)
sätts min = max = första observerade värdet.

**Finalisering (ny 120s-ticker, ny fil `stats-receiver-distance.go`).**
Speglar `updateLowestAircraft`/`updateFastestAircraft`
(`stats-motion.go`) rad för rad — samma `upsertFlightHistory`/
`recordCandidate`/`writeRecords`/`MarkProcessed`-anrop — men frågan som
väljer rader att processa har ett extra villkor utöver
`nearest_processed = false OR furthest_processed = false`:
`last_seen < now() - interval '10 minutes'`. Det extra villkoret är hela
skillnaden mot de sju befintliga kategoriernas frågor, och det är just det
som gör att värdet som skrivs är det fullt ackumulerade min/max för hela
flygningen snarare än ett mellanvärde som fortfarande kan ändras. Körs på
samma 120s-ticker som `updateMeasurementStatistics` i `core.go` — ingen ny
`time.Ticker`.

Ett flygplan som aldrig tystnar i 10+ minuter (t.ex. kontinuerlig lokal
trafik) dyker inte upp i Nearest/Furthest-leaderboarden förrän det gör det.
Samma eftersläpning gäller redan alla andra kategorier, men är mer synlig
här eftersom Nearest/Furthest inte visar *något* värde alls förrän
flygningen är klar — till skillnad från t.ex. Fastest som visar ett värde
direkt (om än potentiellt inte det slutgiltiga). Ett medvetet, accepterat
avsteg — inget att bygga runt.

## Databasschema (migration `000018_add_receiver_distance_records`)

**`aircraft_data`** — nya kolumner, uppdaterade löpande i hot path:
- `min_distance_receiver NUMERIC`
- `min_distance_receiver_altitude INTEGER`
- `min_distance_receiver_bearing NUMERIC`
- `max_distance_receiver NUMERIC`
- `max_distance_receiver_altitude INTEGER`
- `max_distance_receiver_bearing NUMERIC`
- `nearest_processed BOOLEAN NOT NULL DEFAULT false`
- `furthest_processed BOOLEAN NOT NULL DEFAULT false`

**`flight_history`** — samma sex värdekolumner som ovan (utan
processed-flaggor; arkivet behöver inte dem).

**`records.category`** CHECK-constraint utökas med `'nearest'` och
`'furthest_range'` (drop/add constraint, samma mönster som tidigare
kategoritillägg). Namnen är valda för att inte krocka med `furthest_flown`
(ett annat mått: avstånd från startflygplats, inte från mottagaren) — redan
verifierat fria i koden.

## Kategori-metadata & notiser

- `records-meta.go`, `recordCategories`-map:
  `"nearest": {MetricName: "min_distance_receiver", KeepMax: false}`,
  `"furthest_range": {MetricName: "max_distance_receiver", KeepMax: true}`
- `notifications.go`, `recordDisplay`-map: två rader, t.ex.
  `{"Nearest", "Distance", "km"}` / `{"Furthest", "Distance", "km"}`
- `notifications.go`, notifikations-configmapen: `notify_record_nearest`,
  `notify_record_furthest_range` (samma default-`true`-mönster som övriga)

Ingen ny kod behövs i `writeRecords`, `clearRecords`, `getRecords`,
`allTimeBest` eller notifikationslogiken i övrigt — allt är redan generiskt
över `category`.

## API

Två nya rutter i `api.go`, ren återanvändning av befintlig handler:

```go
stats.GET("/motion/nearest", func(c *gin.Context) { s.getRecords(c, "nearest") })
stats.GET("/motion/furthest", func(c *gin.Context) { s.getRecords(c, "furthest_range") })
```

## Frontend

Två nya komponenter, kopior av `MotionFurthestFlownAircraft.svelte`-mönstret:
`MotionNearestAircraft.svelte` (endpoint `api/stats/motion/nearest`) och
`MotionFurthestRangeAircraft.svelte` (endpoint `api/stats/motion/furthest`).
Kolumner: Reg, Typ, Avstånd, Höjd (vid mätpunkten), Riktning (formaterad som
kompassgrader, t.ex. "270°"), Först/Sist sedd.

- `dashboardCards.js`: två nya entries, `tab: 'motion-stat'`, titlar
  "Nearest" / "Furthest".
- `Settings.svelte`: två nya rader i `recordCategories` (Danger Zone-listan
  för clear) och två nya notifikations-toggles (samma mönster som
  `notifyRecordFurthestFlown` osv).

## Rensning

Ingen ny kod. `clearRecords(pg, categories)` (`records-jobs.go`) och
Danger Zone-UI:t (`Settings.svelte`) är redan helt generiska över
`category` — de nya kategorinamnen fungerar automatiskt så snart de finns i
`recordCategories`-mapen och i frontendens `recordCategories`-array.

## Testning

Ren Go-logik testas fristående, i linje med hur `haversine_test.go` redan
testar `haversineDistanceKm`:
- Bearing-normalisering till [0, 360) grader (inklusive gränsfallen 0° och
  negativa värden från `cheap-ruler`s `Bearing()`).
- Min/max-jämförelselogiken (första observationen sätter både min och max;
  efterföljande observationer uppdaterar bara vid ett strikt bättre värde).

Ingen testinfrastruktur finns för DB/HTTP/Svelte-lagren (se CLAUDE.md) — de
verifieras genom att köra stacken.

## Explicit utanför scope

- Ingen räknare för "hur ofta flyger plan rakt över mig".
- 3D/slant range-avstånd.
- Ingen retroaktiv beräkning för flygningar som redan pågår när migrationen
  körs — de börjar ackumulera min/max från och med första tick efter
  deploy, precis som varje annan ny kolumn.
