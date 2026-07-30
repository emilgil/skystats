# Design: rikare aircraft info-modal (detaljer)

**Datum:** 2026-07-30
**Status:** Godkänd design (väntar spec-granskning), redo för implementationsplan
**Branch:** `feat/aircraft-modal-details`
**Bygger på:** aircraft info-modal ([[aircraft-info-modal-work]]), redan deployad.

## Syfte

Modalen visar idag mest reg/typ, operator, live-status (eller "not visible"),
antal sedda, senast sedd och ett foto. För plan som inte syns just nu blir det
tunt. Vi vill visa **mer**: tillverkare/typ, vilka rekord planet håller, rutter,
tidpunkter och antal observationer.

## Nuvarande läge (utforskat mot 251)

- `registration_data` har `manufacturer`, `type`, `icao_type`, `registered_owner`
  (ex. `c023aa` = Boeing · 777 233LR · B77L · Air Canada).
- `records` (per hex): ett plan kan toppa en eller flera kategorier, ofta i flera
  perioder med samma värde (ex. `c023aa` toppar *fastest* 566 kt i 7d/30d/90d/365d/
  all_time). Kategori→metrik: fastest/slowest→`ground_speed` (kt),
  highest/lowest→`barometric_altitude` (ft), furthest_flown→`distance_flown` (km),
  longest_route→`route_distance` (km), most_remaining→`distance_remaining` (km).
  `recordCategories` i `core/records-meta.go` har `KeepMax` (true = större bäst).
- `flight_history` (en rad per besök) har `first_seen`, `last_seen`,
  `origin_iata_code`, `destination_iata_code`, `ground_speed`,
  `barometric_altitude`. **~72 %** av raderna har origin/dest ifyllt (733 av 1023);
  resten okänd rutt. Många plan har bara **1** observation.

## Beslut (från brainstorming)

- **Observationslista: max 10** senaste besök; om fler, visa "…och X till".
- **Rekord:** en rad per kategori planet toppar, med bästa värdet (MAX/MIN enligt
  `KeepMax`) — dedupe över perioder.
- **Rutter:** visas per observation (origin → destination), "—" när okänd.
- Fotohantering är redan fixad separat (planespotters-först) — ingår inte här.
- Ingen schemaändring, ingen migration, inga nya beroenden.

## Backend

Utöka den befintliga handlern `core/aircraft-detail.go` (`getAircraftDetail`):

1. **Tillverkare:** lägg `manufacturer` i `registration_data`-frågan → `resp["manufacturer"]`.
2. **Rekord planet håller:** ny fråga
   `SELECT category, period_type, metric_name, metric_value::float8 FROM records WHERE hex=$1`.
   Dedupe i Go till en post per `category` och behåll bästa värdet enligt
   `recordCategories[category].KeepMax` (true→störst, false→minst). Returnera
   `records: [{category, metric_name, value}]`.
3. **Observationer (max 10):** ny fråga
   `SELECT first_seen, last_seen, origin_iata_code, destination_iata_code,
   ground_speed::float8, barometric_altitude FROM flight_history WHERE hex=$1
   ORDER BY first_seen DESC LIMIT 10`. Returnera
   `observations: [{first_seen, last_seen, origin, destination, ground_speed, altitude}]`.
   Totalantalet finns redan som `history.times_seen`.

**Nya fält i svaret** (utöver befintliga hex/registration/type/operator/live/history/
photo/interesting):
```json
{
  "manufacturer": "Boeing",
  "records": [ {"category":"fastest","metric_name":"ground_speed","value":566.0} ],
  "observations": [
    {"first_seen":"2026-07-28T07:49:41Z","last_seen":"2026-07-28T07:50:55Z",
     "origin":"YYZ","destination":"LHR","ground_speed":566.0,"altitude":37000}
  ]
}
```
`records` och `observations` är alltid arrayer (tomma `[]` när inget finns).

## Frontend

I `web/src/components/AircraftModal.svelte`, lägg till sektioner (mellan header och
foto):

- **Flygplan:** `manufacturer · type` (+ `icao_type` inom parentes) · registrerad
  ägare. (Använd befintliga `registration`/`type`/`operator`.)
- **Rekord:** en badge/rad per post i `records`, formaterad via en kategori→
  etikett+enhet-map i komponenten:
  fastest→"Fastest" (kt), slowest→"Slowest" (kt), highest→"Highest" (ft),
  lowest→"Lowest" (ft), furthest_flown→"Furthest flown" (km),
  longest_route→"Longest route" (km), most_remaining→"Most remaining" (km).
  Ex: "Fastest — 566 kt". Dölj sektionen om `records` är tom.
- **Observationer (`history.times_seen` totalt):** kompakt tabell över
  `observations` (max 10 rader): tidpunkt (`first_seen`, `toLocaleString`), rutt
  (`origin → destination`, annars "—"), fart (`ground_speed` kt) och höjd
  (`altitude` ft). Om `times_seen > observations.length`, visa
  "…och {times_seen - observations.length} till". Dölj sektionen om inga
  observationer.

Behåll live-blocket och foto som idag.

## Filer som berörs

- `core/aircraft-detail.go` (utöka handlern; ev. bryt ut hjälpfunktioner om den
  växer).
- `web/src/components/AircraftModal.svelte` (nya sektioner + kategori→etikett-map).
- Ingen migration, ingen schemaändring.

## Verifiering

Repot har inga tester. Backend: `go build ./...` + `go vet ./...`. Frontend:
`npm run build`. Runtime: curl `/api/stats/aircraft/:hex` för ett rekord-plan
(c023aa → manufacturer Boeing, records fastest 566, 1 observation) och ett med
flera besök/rutt; browser-klick verifieras vid deploy.

## Utanför scope (YAGNI)

Ingen bildväxling/karusell (avfärdad). Ingen ny fotopipeline. Ingen aggregering av
distinkta rutter utöver per-observation-listan. Inga schemaändringar.
