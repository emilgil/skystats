# Design: Current sightings-flik

**Datum:** 2026-07-30
**Status:** Godkänd design, redo för implementationsplan
**Branch:** `feat/current-sightings` (eget worktree i `../skystats-current-sightings`)
**Källa:** `2026-07-28-current-sightings-brief.md`

## Syfte

En ny flik som visar **alla** flygplan mottagaren ser just nu inom `RADIUS`,
sorterade efter avstånd, utan radtak. Skiljer sig från befintliga "Above Me" som
bara visar de 5 närmaste inom `ABOVE_RADIUS` (20 km).

## Nuvarande läge (utforskat)

- **`getAboveStats` (`core/api.go:297`) är i praktiken 90 % av frågan.** Den
  joinar `aircraft_data` mot `registration_data` (`hex = mode_s`) och
  `route_data` (`flight = route_callsign`), filtrerar på
  `last_seen >= NOW() - INTERVAL '60 seconds'` och sorterar på
  `last_seen_distance`.
- **Airline finns redan i databasen.** `route_data.airline_name` /
  `airline_icao` fylls av `routes.go:220` via `data.LookupAirline`. Ingen ny
  lookup mot det embeddade `airlines.csv` behövs.
- **Intressant-flaggan** finns i `interesting_aircraft."group"` med värdena
  `Mil`/`Gov`/`Pol`/`Civ`, matchad på versalt hex (`icao = UPPER(hex)`).
- **Hide/show-cards finns redan** (`web/src/lib/dashboardCards.js`,
  `HideableCard.svelte`, `stores/hiddenCards.js`).

### Fallgropen som styr designen

`aircraft_data.alt_baro`, `gs`, `ias` och `tas` är **inte aktuella värden**.
`updateExistingAircrafts` (`core/aircraft.go:341-357`) skriver bara upp dem när
det nya värdet är *högre* — de är alltså "högsta observerade under passagen".
`last_seen_lat/lon/distance` och `track` uppdateras däremot varje 2 s-tick.

En rak DB-fråga skulle därför visa fel altitud och fart i en flik som heter
"Current sightings". Live-värdena måste komma från readsb-snapshoten.

### Verifierat mot mottagaren (192.168.1.89, 2026-07-30 10:11)

- Hela `aircraft.json` innehöll **2 flygplan**. Volymen är låg; 2 s-polling är
  väl tilltaget, inte på gränsen.
- Ett av dem (`SE-LKV`, `type: mode_s`) saknade `lat`/`lon` helt. Plan utan
  position finns på riktigt och faller bort i radiusfiltret (lat/lon 0,0 hamnar
  utanför `RADIUS`).
- Feeden innehåller `"desc": "BOEING 737 MAX 8"` — ett fält som `Aircraft` i
  `core/models.go:14-53` inte plockar upp. Gratis läsbar typbeskrivning.
- `r_dst` är i **nautiska mil** (12.295 för ett plan på 22,8 km), inte km. Får
  inte förväxlas med en färdig avståndskolumn; cheap-ruler-beräkningen står fast.

## Beslut (från brainstorming)

| Fråga | Beslut |
|---|---|
| Källa för altitud/fart | Live-snapshot i minnet, byggd av 2 s-tickern |
| Uppdateringsintervall | 2 s, som `AboveTimeline.svelte` |
| Payload-strategi | Byggs en gång per tick i daemonen, delad store |
| Radklick | Öppnar den delade modalen från `feat/aircraft-info-modal` |
| Deploy | **Spärrad** tills användaren gett klartecken (se nedan) |

## Beroende på `feat/aircraft-info-modal`

Radklicket anropar `openAircraftModal(hex)` från
`web/src/stores/aircraftModal.js`, som skapas av grenen
`feat/aircraft-info-modal`. Den grenen måste vara mergad till `main` innan den
här grenen kan byggas färdigt; rebasa på `main` efter att den landat.

API:t är verifierat mot den grenens pågående arbete
(`MotionStats.svelte`): namngiven export `openAircraftModal`, anropas med hex —
**inte** `aircraftModal.open(hex)`.

## Arkitektur

`GET /api/stats/current` serverar en payload som daemonen bygger en gång per
2 s-tick. Kostnaden är konstant oavsett antal öppna webbläsarflikar, och
klientens pollningstakt matchar exakt den takt datan faktiskt ändras i.

Inga schemaändringar, inga migrationer.

### Backend

**Ny fil `core/current-sightings.go`** — håller `api.go` (39 kB) fokuserad, samma
resonemang som `aircraft-detail.go` på den andra grenen.

**Delat tillstånd**, paketnivå-singleton (allt ligger i samma `main`-paket, så
varken ticker eller API-goroutine behöver en genomtrådad pekare):

```go
type currentSightingsStore struct {
    mu          sync.RWMutex
    aircraft    []CurrentSighting
    generatedAt time.Time
}
```

**Inkopplingspunkt:** `updateAircraftDatabase` (`core/aircraft.go:20`) har redan
filtrerat fram `aircraftsInRange` när den anropar `pg.updateDatabase`. Lägg
`refreshCurrentSightings(pg, response.Now, aircraftsInRange)` direkt efter — då
finns nyupptäckta plan redan i `aircraft_data`.

**Berikning i en fråga**, med hex- och flight-arrayerna från snapshoten:

```sql
SELECT s.hex, s.flight, reg.registration, reg.icao_type, reg.manufacturer,
       reg.registered_owner, rt.airline_name, rt.airline_icao,
       rt.origin_iata_code, rt.origin_name, rt.destination_iata_code,
       rt.destination_name, ia."group"
FROM unnest($1::text[], $2::text[]) AS s(hex, flight)
LEFT JOIN registration_data reg ON reg.mode_s = s.hex
LEFT JOIN LATERAL (SELECT * FROM route_data
                   WHERE route_callsign = s.flight LIMIT 1) rt ON true
LEFT JOIN LATERAL (SELECT "group" FROM interesting_aircraft
                   WHERE icao = UPPER(s.hex) LIMIT 1) ia ON true
```

`LATERAL ... LIMIT 1` i stället för rak `LEFT JOIN` för route och interesting:
dagens `getAboveStats` gör en rak join mot `route_data`, vilket tyst dubblerar
raden om samma callsign finns flera gånger. Med `LIMIT 5` märks det inte; i en
tabell utan radtak skulle det ge synliga dubbletter.

**Avstånd** beräknas med samma `getDistance()` som ingesten, så kolumnen är
konsekvent med `last_seen_distance` i övriga vyer. Listan sorteras stigande på
avstånd i Go innan den läggs i storen.

**Feed-avbrott:** om `Fetch()` misslyckas returnerar `updateAircraftDatabase`
tidigt (`core/aircraft.go:24-27`) och storen behåller sitt gamla innehåll —
fliken skulle visa plan som sedan länge flugit vidare, utan att något ser trasigt
ut. Därför bär storen `generatedAt`, som följer med i svaret.

### API-kontrakt

`GET /api/stats/current`, inga query-parametrar:

```json
{
  "generated_at": "2026-07-30T10:10:41+02:00",
  "aircraft": [{
    "hex": "4aca8d", "flight": "NOZ1101",
    "registration": "SE-RTM", "type": "B38M",
    "type_description": "BOEING 737 MAX 8",
    "airline": "Norwegian", "operator": "Norwegian Air Sweden AOC AB",
    "altitude": 38000, "ground_speed": 458.2, "track": 343.78,
    "distance_km": 22.79,
    "origin_iata": "ARN", "origin_name": "Stockholm Arlanda",
    "destination_iata": "CPH", "destination_name": "Copenhagen Kastrup",
    "interesting_group": null,
    "last_seen": "2026-07-30T10:10:40+02:00"
  }]
}
```

Objekt i stället för naken array (som `/above` returnerar) enbart för
`generated_at` — det är vad som låter fliken upptäcka att readsb slutat svara.

| Briefens kolumn | Fält | Härledning |
|---|---|---|
| Flight | `flight` | snapshot, trimmad av `TrimFlightStrings` |
| Registrering | `registration` | snapshot `r`, annars `registration_data.registration` |
| Typ | `type` + `type_description` | snapshot `t` + `desc`, annars `reg.icao_type` |
| Airline | `airline`, `operator` | `route_data.airline_name`; `registered_owner` separat |
| Altitud | `altitude` | `alt_baro` |
| Fart | `ground_speed` | `gs` |
| Avstånd | `distance_km` | cheap-ruler mot `LAT`/`LON` |
| Ursprung/destination | `origin_*`, `destination_*` | `route_data` |
| Intressant | `interesting_group` | `Mil`/`Gov`/`Pol`/`Civ` eller `null` |
| Senast sedd | `last_seen` | `now - seen` ur snapshoten |

`airline` och `operator` hålls isär i stället för att slås ihop. De flesta plan
här är inte linjefart, och att fylla en kolumn som heter Airline med en
privatägares namn vore att låta API:t ljuga. Frontend visar `airline`, faller
tillbaka på `operator`, annars tankstreck.

`Aircraft`-structen i `core/models.go` utökas med ett `Desc`-fält mappat mot
feedens `desc`. `type_description` blir `null` när feeden saknar fältet (t.ex.
för `mode_s`-plan utan typuppslag).

### Frontend

| Fil | Ändring |
|---|---|
| `web/src/components/CurrentSightings.svelte` | ny — kortet med tabellen |
| `web/src/components/TabCurrentSightings.svelte` | ny — `dashboardCards.filter(c => c.tab === 'current-stat')` |
| `web/src/lib/dashboardCards.js` | ny post `{ id: 'current_sightings', title: 'Current Sightings', tab: 'current-stat' }` |
| `web/src/App.svelte` | ny post i `tabs`-arrayen, först; default-flik flyttas dit |
| `web/src/components/Settings.svelte` | `cardTabLabels` + `cardTabOrder` (`Settings.svelte:7-14`) |

Tabbnyckeln `current-stat` följer namngivningen från `route-stat`/`motion-stat`.
Utan Settings-ändringen saknas fliken i "Visible Cards".

**Pollning måste pausas när fliken inte syns.** Flikinnehållet renderas inte
villkorligt — `{#each tabs}` monterar *alla* flikkomponenter och döljer de
inaktiva med `class="hidden"` (`App.svelte:103-107`). Övriga flikar hämtar en
gång i `onMount`, så det märks inte. En 2 s-poller skulle annars fortsätta i
bakgrunden även när man tittar på en annan flik eller minimerat webbläsaren.

Lösningen ligger **inuti** den nya komponenten: en `IntersectionObserver` på
kortets element plus en lyssnare på `document.visibilitychange`. Pollningen
startar när kortet är synligt och stoppas annars. Inga ändringar i övriga
flikkomponenter, och ingen `active`-prop genom `<svelte:component>`.

**Tabellen:** DaisyUI `table table-sm table-pin-rows` i `overflow-x-auto` med
`max-h-[70vh] overflow-y-auto` — inget radtak, listan scrollar, rubrikraden
fastnar. Avstånd med en decimal + km, altitud tusentalsavgränsad + ft, fart
avrundad + kt. Intressant-flaggan som `badge badge-accent` med gruppen.
Radklick → `openAircraftModal(aircraft.hex)`.

**Tillstånd — skiljer sig från befintliga kort som hämtar en gång:**

- *Första laddningen:* `loading loading-ring loading-lg`, som övriga kort.
- *Efterföljande hämtningar:* rör **inte** `loading`. Sätter man `loading = true`
  vid varje hämtning, som de befintliga komponenterna gör, blinkar tabellen bort
  varannan sekund.
- *Fel:* behåll senaste lyckade data, visa en diskret varning ovanför tabellen i
  stället för att ersätta innehållet med `alert alert-error`. Ett enstaka tappat
  anrop ska inte tömma vyn.
- *Tomt:* `alert alert-info` "No aircraft currently visible" — normalläge här,
  inte ett fel. Mottagaren hade ett enda positionerat plan kl. 10:11.
- *Inaktuellt:* `generated_at` äldre än 30 s → `alert alert-warning` med
  tidsstämpeln. Fallet där readsb slutat svara men daemonen lever.

## Verifiering

Repot har inga tester.

- **Backend:** `go build` med `~/.local/go/bin/go`, sedan `curl` mot
  `/api/stats/current` för tre fall: plan synligt, inga plan synliga, readsb
  nedsläckt (kontrollera att `generated_at` åldras).
- **Frontend:** `npm run build`, sedan `npm run dev -- --host` mot en körande
  daemon. Kontrollera sortering, scroll, radklick, och att pollningen tystnar när
  man byter flik (nätverksfliken i devtools).

## Deploy-spärr

Grenen deployas **inte** till 192.168.1.251 förrän användaren uttryckligen gett
klartecken att aircraft-info-modalen är verifierad. Ordningen är:

1. `feat/aircraft-info-modal` mergas till `main`
2. Användaren bekräftar att modalen fungerar
3. Den här grenen rebasas på `main` och byggs färdig
4. Gemensam genomgång av båda innan deploy

## Flaggat men utanför scope

`alt_baro` rapporteras av readsb som strängen `"ground"` för plan på marken,
medan `Aircraft.AltBaro` är `int` — och `json.Unmarshal`-felet i
`core/aircraft.go:30` kastas bort. Markplan får därför altitud 0. Det är
kosmetiskt här, men det påverkar också `lowest_aircraft`-leaderboarden, som
rimligen fylls med 0-fots "rekord". Befintlig bugg som förtjänar en egen gren.
Verifiera beteendet vid implementation.

## Utanför scope (YAGNI)

Ingen karta, inga sorterbara kolumnrubriker, ingen filtrering eller sökning,
inga schemaändringar, ingen fix av `alt_baro: "ground"`, ingen städning av
`AboveTimeline`s egen inline-modal (som blir redundant när `AircraftModal`
finns, men som ingen av de två briefarna täcker).
