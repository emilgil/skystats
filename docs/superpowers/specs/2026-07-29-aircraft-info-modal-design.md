# Design: informationsmodal per flygplan (Aircraft info modal)

**Datum:** 2026-07-29
**Status:** Godkänd design, redo för implementationsplan
**Branch:** `feat/aircraft-info-modal`
**Källa:** `2026-07-28-aircraft-info-modal-brief.md`

## Syfte

Klick på en tabellrad (Record Holders eller Interesting Aircraft) ska öppna en
informationsmodal om just det planet på raden, med tre delar:

1. **Live-status** om planet syns av mottagaren just nu (höjd, fart, kurs,
   avstånd/bäring, position) — annars ett tydligt "syns inte just nu"-läge.
2. **Historik**: antal gånger planet setts totalt samt när det senast sågs.
3. **Ett foto** på planet.

Gäller alla 7 Record Holders-kort (Fastest, Slowest, Highest, Lowest,
Furthest Flown, Longest Route, Most Remaining) och alla 4 Interesting-kort
(Military, Government, Police, Civilian).

## Nuvarande läge (utforskat)

Mycket finns redan — briefens antaganden om att bygga nytt visade sig till stor
del onödiga:

- **Två modaler finns.** `InterestingAircraft.svelte` har en DaisyUI-`<dialog>`
  som renderas ur raddata (reg, tags, operator, upp till 3 bilder från
  plane-alert-db, `image_link_1..3`). `AboveTimeline.svelte` har en modal som
  visar live-position **och** gör redan en klientside-hämtning mot planespotters
  per hex (`getImage`, `https://api.planespotters.net/pub/photos/hex/{hex}`,
  returnerar `thumbnail_large.src`, `photographer`, `link`).
- **Live-status finns i `aircraft_data`** per hex: `alt_baro`, `gs`, `track`,
  `last_seen_lat/lon`, `last_seen_distance`, `last_seen_bearing`, `last_seen`.
  Mönstret "syns just nu" används redan av `getAboveStats`
  (`WHERE last_seen >= NOW() - INTERVAL '60 seconds'`).
- **Foton finns redan lagrade.** `registration_data.url_photo` /
  `url_photo_thumbnail` fylls av `registrations.go` från adsbdb (30s-jobbet) för
  *alla* berikade plan — inte bara interesting. Plane-alert-db-bilder finns för
  interesting; planespotters finns klientsidan.
- **`hex` skickas redan** till frontend i både records-raderna (`getRecords`)
  och interesting-raderna (`getRecentInterestingAircraft`). Ingen ny
  rad-payload-plumbing behövs.
- **Historik** kan härledas ur `flight_history` (`UNIQUE(hex, first_seen)` = en
  rad per besök) och `aircraft_data.last_seen`.

## Beslut (från brainstorming)

- **Fotokälla:** återbruka befintligt — lagrad adsbdb-bild först, annars
  klientside-planespotters-fallback per hex. **Ingen ny backend-fotopipeline.**
- **Live-status:** endast textfält (ingen karta — inget kartbibliotek finns och
  det vore ett stort nytt beroende). "Syns just nu" = `last_seen` inom ~60s.
- **Klickyta:** hela raden är klickbar, på både Record Holders och Interesting
  (konsekvent med hur Interesting redan fungerar).
- **Arkitektur:** ny samlad backend-endpoint + en delad modal-komponent
  (Approach A). Servern sätter ihop data vid klick så live-status blir färsk vid
  öppning; befintliga listors payloads lämnas orörda.

## Arkitektur

`GET /api/stats/aircraft/:hex` → ny handler `getAircraftDetail` som frågar
databasen på `hex` och returnerar ett samlat objekt. En delad
`AircraftModal.svelte` monteras **en gång** i `App.svelte` och styrs av en liten
store `aircraftModal` (`open(hex)`). Vilken rad som helst anropar
`aircraftModal.open(aircraft.hex)`; modalen hämtar endpointen vid öppning.

Inga schemaändringar, inga migrationer.

## Backend

**Rutt:** registreras i `core/api.go` bredvid övriga `/stats/`-rutter.
**Handler:** ny fil `core/aircraft-detail.go` (håller det stora `api.go`
fokuserat).

**Datakällor (allt på `hex`):**

| Del | Källa | Logik |
|---|---|---|
| Identitet | `registration_data` (`mode_s=hex`) | reg, type, `registered_owner` |
| Airline/operator | `route_data` via senaste `flight` → `airline_name`, annars `registered_owner` | |
| Live-status | senaste `aircraft_data`-rad för hex (`ORDER BY last_seen DESC LIMIT 1`) | `visible = last_seen >= NOW() - 60s`. Om synlig: `alt_baro`, `gs`, `track`, `last_seen_distance`, `last_seen_bearing`, `last_seen_lat/lon`. Annars `live: null` |
| Historik | `flight_history` + `aircraft_data` | `times_seen = COUNT(*) FROM flight_history WHERE hex=$1`; `last_seen = MAX(aircraft_data.last_seen)` för hex (färskast) |
| Foto | `registration_data.url_photo` / `url_photo_thumbnail` (adsbdb) | Kan vara `null` |
| Interesting | senaste `interesting_aircraft_seen`-rad för hex | Om träff: `group`, `operator`, `tags` (tag1–3, tomma filtreras bort) och `images` (`image_link_1..3`). Annars `interesting: null` |

**Svarsform:**
```json
{
  "hex": "4ca7b5", "registration": "EI-DYY", "type": "B738",
  "operator": "Ryanair",
  "live": {
    "altitude": 37000, "ground_speed": 456, "track": 270,
    "distance_km": 12.3, "bearing": 210, "lat": 57.1, "lon": 12.2
  },
  "history": { "times_seen": 42, "last_seen": "2026-07-29T08:12:00Z" },
  "photo": { "url": "...", "thumbnail": "...", "source": "adsbdb" },
  "interesting": {
    "group": "Mil", "operator": "RAF", "tags": ["Fighter"],
    "images": ["...", "...", "..."]
  }
}
```
`live`, `photo` och `interesting` är `null` när de inte gäller.

**Edge cases:** okänd/oberikad hex → 200 med best-effort (`live:null`,
`photo:null`, `times_seen` kan bli 0); DB-fel → 500 med `error` (som övriga
handlers). Tom `:hex` → 404 (ingen rutt matchar). En DB-fråga per öppning,
on-demand.

## Frontend

**`web/src/stores/aircraftModal.js` (ny):** en Svelte-store som håller vald hex
och öppet-läge, med `open(hex)` / `close()`.

**`web/src/components/AircraftModal.svelte` (ny):** monteras en gång i
`App.svelte`. Vid öppning `fetch('/api/stats/aircraft/' + hex)`.
- **Laddning:** `loading loading-ring`-skeleton (som befintliga kort).
- **Fel:** `alert alert-error` (samma mönster som Interesting-modalen).
- **Innehåll:** rubrik `reg – type` + interesting-tags (om `interesting`),
  operator/airline-rad, **live-block** (höjd/fart/kurs/avstånd/bäring/lat-lon)
  *eller* "Syns inte just nu", **historik** (antal sedda + senast sedd), och
  **foto**.

**Foto-logik (bevarar nuvarande Interesting-upplevelse):**
1. `interesting.images` finns → visa dem (upp till 3, som idag).
2. annars `photo` (adsbdb) → visa den.
3. annars klientside-planespotters per hex (återbruk av `AboveTimeline.getImage`)
   → bild + fotograf-kredit + länk.
4. annars "No photo available".

**Inkoppling:**
- **Record Holders:** alla 7 renderas via `MotionStats.svelte`. Lägg
  `on:click={() => aircraftModal.open(aircraft.hex)}` +
  `cursor-pointer hover:bg-base-300` på `<tr>`. En ändring täcker alla 7.
- **Interesting:** `InterestingAircraft.svelte` har redan hel-rads-klick mot sin
  egen `<dialog>`. Peka om klicket till `aircraftModal.open(aircraft.hex)` och ta
  bort den lokala dialogen. Interesting-korten får då live-status + historik
  utöver sina bilder.

**Uppdatering:** en hämtning vid öppning (snapshot). Ingen live-polling i modalen
(YAGNI) — kan läggas till senare med samma intervall som `AboveTimeline`.

## Filer som berörs

- *Backend:* `core/api.go` (registrera rutten), ny `core/aircraft-detail.go`
  (handlern).
- *Frontend:* ny `web/src/stores/aircraftModal.js`, ny
  `web/src/components/AircraftModal.svelte`, `web/src/App.svelte` (montera
  modalen), `web/src/components/MotionStats.svelte` (rad-klick),
  `web/src/components/InterestingAircraft.svelte` (peka om klick, ta bort inline-
  dialog).
- Ingen migration, ingen schemaändring.

## Verifiering

Repot har inga automattester.
- **Backend:** `go build` (lokalt nedladdad Go 1.25.3) + `curl` mot endpointen
  för fyra fall: synligt plan, ej synligt, interesting, okänd hex — kontrollera
  JSON-formen.
- **Frontend:** `npm run build`, sedan `npm run dev -- --host` och klicka rader i
  både Record Holders och Interesting; verifiera live/historik/foto, hela
  fallback-kedjan och hel-rads-klick.
- **Deploy:** till 251 enligt samma flöde som senast (git archive → ssh tar →
  `docker compose up -d --build`; `docker builder prune -f` om disken är trång).

## Utanför scope (YAGNI)

Ingen karta, ingen ny backend-fotopipeline, ingen live-polling i modalen, inga
schemaändringar/migrationer.

## Öppen punkt (implementationsvalidering)

`flight_history` fylls av record-sweepen — för ett plan som setts men ännu inte
svepts kan `times_seen` släpa ett besök. Rekommendation: använd `flight_history`
(tydlig grain) och verifiera täckningen vid implementation; alternativ är att
räkna `aircraft_data`-rader per hex direkt om täckningen visar sig otillräcklig.
