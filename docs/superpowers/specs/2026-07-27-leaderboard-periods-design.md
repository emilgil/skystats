# Design: period-baserade leaderboards (Record Holders)

**Datum:** 2026-07-27
**Status:** Godkänd design, redo för implementationsplan
**Branch (föreslagen):** `leaderboard-periods`

## Syfte

Bygga om leaderboard/rekord-systemet så att varje kategori kan visas per
tidsperiod (24h, 7d, 30d, 90d, 365d, all-time) och toppas vid **100 poster per
kategori och period**. Migration `000012_add_period_records` är skriven men
**ännu inte körd**; den inför datamodellen som den här designen bygger vidare på
i applikationskoden.

## Nuvarande läge (utforskat)

- **Ingest:** `core/stats-motion.go` (fastest/slowest/highest/lowest) och
  `core/stats-distance.go` (furthest_flown/most_remaining/longest_route). Körs
  på 120s- respektive 300s-tick i `core.go`. Läser `aircraft_data`-rader där
  respektive `*_processed = false`, jämför mot nuvarande **topp-50**-tröskel
  (`stats-motion-helpers.go`), infogar kvalificerande rader, trimmar med
  `DeleteExcessRows(..., 50)` (`db-utils.go`), sätter processed-flaggan med
  `MarkProcessed`.
- **Utvärderings-semantik:** det finns **ingen** "flight finished"-händelse. En
  flygning (rad i `aircraft_data`, unik `hex + first_seen`) utvärderas **en
  gång** — UPDATE-vägen i `aircraft.go` (`updateExistingAircrafts`) nollställer
  inte processed-flaggorna. Nya rader får `false` och plockas av nästa tick.
- **Läsning:** 7 endpoints under `/api/stats/motion/*` i `core/api.go`
  (~rad 502–810), var och en med hårdkodad `ORDER BY` + `LIMIT` från settingen
  `record_holder_table_limit`.
- **Frontend:** `TabMotionStats.svelte` renderar 7 kort; varje `Motion*.svelte`
  → `MotionStats.svelte` som hämtar från sin endpoint och laddar om när storen
  `refreshRecordHolderData` ändras. Ingen filter-UI finns idag.

## Vägval (beslutade)

1. **Ingest-trigger:** behåll processed-flagg-sweepen. Byt bara skrivmål från de
   7 gamla tabellerna till `flight_history` + `records`. Ingen ny
   flight-completion-detektion.
2. **Filter-UI:** en **global** periodväljare för hela Record Holders-fliken
   (styr alla 7 kort samtidigt).
3. **Custom-range:** **skjuts upp** — men designen lämnar sömmar så att den
   senare blir 1 query-gren + 1 UI-kontroll, utan att röra ingest, sweep,
   retention, schema eller kortens kod.
4. **Ingest-kvalificering:** variant **A (insert-then-trim)** för v1. Variant B
   (tröskel-grind per bucket) sparas som framtida optimering om churn blir ett
   problem.
5. **Defaults:** default-period = **all-time**; radgräns = fortsatt
   `record_holder_table_limit` (≤100); endpoint-sökvägar kvar under `/motion/`.

## Datamodell (från migration 000012 — ingen ny schemaändring)

- `flight_history` — permanent arkiv över flygningar. Unik `(hex, first_seen)`.
  Alla metrik-kolumner (ground_speed, ias, tas, barometric/geometric altitude,
  distance_flown, route_distance, distance_remaining) + origin/dest-koder.
  Partiella index per metrik.
- `records` — enhetlig leaderboard-tabell:
  `(category, period_type, hex, flight, registration, type, first_seen,
  last_seen, metric_name, metric_value, details jsonb)`. Unik
  `(category, period_type, hex, first_seen)`. Index på
  `(category, period_type, metric_value)` och `(hex, first_seen)`.
  - `category` ∈ fastest, slowest, highest, lowest, furthest_flown,
    longest_route, most_remaining
  - `period_type` ∈ 24h, 7d, 30d, 90d, 365d, all_time
- Nya settings: `leaderboard_sweep_interval_minutes` (60),
  `history_retention_days` (730).

## Komponenter

### 1. Central kategori→metrik-karta (`core/records-meta.go`, ny)

Kärnan i designen. En tabell i Go, en post per kategori, som styr ingest,
trim, tröskel, läs-query och JSON-utvidgning. Både preset-läsningen (mot
`records`) och den framtida custom-range-läsningen (mot `flight_history`)
använder samma karta.

| category | metrik-kolumn | behåll | giltighet | detaljfält (jsonb) |
|---|---|---|---|---|
| fastest | ground_speed | MAX | > 0 | indicated_air_speed, true_air_speed |
| slowest | ground_speed | MIN | ≥ 1 | indicated_air_speed, true_air_speed |
| highest | barometric_altitude | MAX | > 0 | geometric_altitude |
| lowest | barometric_altitude | MIN | ≥ 1 | geometric_altitude |
| furthest_flown | distance_flown | MAX | finns | origin_*_code, destination_*_code |
| longest_route | route_distance | MAX | finns | origin_*_code, destination_*_code |
| most_remaining | distance_remaining | MAX | finns | destination_*_code |

"behåll MAX/MIN" avgör sorteringsriktning i alla lägen: MAX → `ORDER BY
metric_value DESC` (trim raderar lägsta), MIN → `ORDER BY metric_value ASC`
(trim raderar högsta).

### 2. Ingest-omskrivning (`stats-motion.go` + `stats-distance.go`)

Triggern oförändrad: samma 120s/300s-tick, samma `*_processed`-flaggor, samma
`MarkProcessed`. Per utvärderad flygning:

1. **Upsert i `flight_history`** — `ON CONFLICT (hex, first_seen) DO UPDATE` med
   `COALESCE` per kolumn. Motion- och distance-passen skriver vid olika
   tillfällen, så coalesce-mergen bevarar den andra passets kolumner.
2. **Skriv till `records` per period-bucket** (variant A, insert-then-trim):
   - Kandidat i varje `period_type` vars **fönster innehåller `first_seen`**
     (`first_seen >= now() - fönster`); `all_time` alltid. En nyss utvärderad
     flygning har normalt färskt `first_seen` → alla 6 buckets. Gränsvillkoret
     spelar roll om utvärderingen sker med gammalt `first_seen` (t.ex. efter ett
     driftstopp med oprocessade `aircraft_data`-rader) — då ska den inte hamna i
     kortare fönster den redan fallit ur.
   - Infoga kandidaterna i respektive bucket
     (`ON CONFLICT (category, period_type, hex, first_seen) DO UPDATE` av
     metric_value/last_seen/details).
   - Trimma varje berörd `(category, period_type)`-bucket till 100 rader —
     generalisera `DeleteExcessRows` till att ta emot `category` + `period_type`
     som WHERE och `maxRows = 100`.

Giltighetsfiltret per kategori (från kartan) gäller före insert (t.ex. slowest
kräver ground_speed ≥ 1).

### 3. Sweep-jobb (nytt, i `core.go`)

Egen ticker. **Intervallet läses i toppen av varje tick** ur
`leaderboard_sweep_interval_minutes`; om värdet ändrats sedan förra cykeln körs
`ticker.Reset(nyttIntervall)` så en ändrad inställning slår igenom utan omstart
(konsekvent med retention-jobbet). Varje körning, för varje `period_type`
**utom `all_time`**:

```
DELETE FROM records
WHERE period_type = $1
  AND first_seen < now() - <fönster för period_type>
```

Fönster: 24h, 7d, 30d, 90d, 365d. `all_time` är undantaget — poster där faller
bara ur via trim-till-100 i ingest.

Läsning filtrerar inte om på `first_seen` vid query-tid; en bucket kan alltså
innehålla poster som hunnit åldras förbi sitt fönster i upp till ett
sweep-intervall (default 60 min). Det är försumbart mot fönsterlängderna och en
medveten förenkling för v1.

### 4. Retention-jobb (nytt, i `core.go`)

Egen 24h-ticker. Läser `history_retention_days` vid varje körning. Raderar
gammal `flight_history` som inte längre är ett aktivt rekord:

```
DELETE FROM flight_history fh
WHERE fh.first_seen < now() - <retention_days>
  AND NOT EXISTS (
    SELECT 1 FROM records r
    WHERE r.hex = fh.hex AND r.first_seen = fh.first_seen
  )
```

Aktiva rekord skyddas oavsett ålder.

### 5. Läs-/API-lager (`core/api.go`)

- En delad handler `getRecords(c, category)`. De **7 befintliga rutterna**
  (`/api/stats/motion/fastest` …) behålls men binds till den delade handlern via
  closure (samma mönster som `interesting/*`). Noll endpoint-churn i frontend.
- Läser `?period=` (default `all_time`), validerar mot tillåtna värden — ogiltigt
  → HTTP 400.
- Query mot `records`:
  `WHERE category=$1 AND period_type=$2 ORDER BY metric_value <riktning>
  LIMIT min(record_holder_table_limit, 100)`.
- Returnerar **samma JSON-form som idag**: metrik-kartan expanderar `metric_value`
  + `details` tillbaka till de platta fälten (`ground_speed`,
  `indicated_air_speed`, `origin_icao_code`, …), så kortens kolumn-definitioner
  är orörda.
- **Custom-range-söm:** den framtida `?from=&to=`-grenen (läser `flight_history`
  direkt, sorterar per kartans metrik, LIMIT 100) läggs i denna handler. Byggs
  inte i v1.

### 6. Frontend (`TabMotionStats.svelte`, `MotionStats.svelte`, `Motion*.svelte`)

- Global periodväljare högst upp i fliken (DaisyUI join-knappar:
  24h/7d/30d/90d/365d/All-time), default All-time.
- `TabMotionStats` äger `selectedPeriod` och skickar den som prop genom varje
  kort → `MotionStats`, som bygger `${endpoint}?period=${period}` och hämtar om
  reaktivt vid periodbyte (samma mekanism som dagens `refreshRecordHolderData`).
- **Custom-range-söm:** väljaren skickar en ogenomskinlig period-deskriptor och
  URL:en byggs på ett ställe (en URL-byggare). Korten känner aldrig till presets;
  att lägga till ett `Custom…`-val senare blir en lokal ändring i väljaren +
  byggaren.

## Deploy-sekvens (kritiskt)

- Migration 000012 **döper om** de gamla tabellerna till `*_deprecated`. All
  nuvarande kod refererar de gamla namnen, så migrationen får **inte** köras
  ensam — då kraschar dagens daemon. Migrationer körs automatiskt vid start,
  därför måste kodomskrivningen ligga i **samma build/deploy** som första gången
  000012 körs.
- Bootstrappen i 000012 fyller `all_time` + `flight_history` från gammal data →
  all-time-listorna överlever cutovern. Period-buckets (24h…365d) startar tomma
  och fylls på framåt; ingen bakåtfyllnad (det historiska som finns är ändå bara
  de gamla topp-50:orna). Detta är förväntat beteende.
- En **senare migration** droppar `*_deprecated` när allt verifierats i drift.

## Verifiering (repo saknar tester)

- `cd core && go build` måste passera.
- Deploy till 251-testservern (kör 000012 automatiskt), sedan:
  - ren daemon-start i loggarna (inga "missing table"-fel),
  - `/api/stats/motion/*?period=all_time` returnerar data (bootstrappad),
  - period-buckets fylls över tid,
  - sweep- och retention-jobb kör felfritt (bumpa `LOG_LEVEL=DEBUG` tillfälligt),
  - frontend: global väljare växlar alla 7 kort.
- Rollback finns via `000012_add_period_records.down.sql`.

## Utanför scope (v1)

- Custom/valfritt datumintervall (from/till) — sömmar förberedda enligt ovan.
- Ingest-variant B (tröskel-grind) — optimering vid behov.
- Migration som droppar `*_deprecated` — separat, senare.
- Runtime-omläsning av sweep-intervallet är löst; motsvarande för andra
  hårdkodade tickers är inte i scope.
