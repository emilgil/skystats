# Bevakningar & notiser — spec för Claude Code

> **För Claude Code:** Detta är en spec, inte en färdig implementationsplan — repot är inte tillgängligt i den session där detta skrevs, så exakta filnamn/kolumnnamn/funktionsnamn nedan är **antaganden baserade på tidigare specs och CLAUDE.md**, inte bekräftade mot koden. Börja med att utforska kodbasen (särskilt punkterna i "Att utforska innan du börjar" nedan), skriv en konkret task-by-task-plan (gärna med `writing-plans`-mönstret som i tidigare planer i det här repot), och stäm av den med användaren innan du börjar implementera.

## Mål

Låta användaren skapa godtyckligt många **bevakningar** ("watches") — regler som matchar mot flygplan som Skystats redan spårar — och få en **notis via Apprise** (befintlig integration, se nedan) första gången ett flygplan matchar en bevakning, tills det slutar matcha igen.

## Bakgrund — vad som redan finns

Skystats har enligt användaren redan en fungerande Apprise-integration som idag används för att skicka notis vid "Interesting aircraft"-träffar (militär/regering/polis/civil, matchat mot plane-alert-db). Apprise-URL(er) konfigureras via Settings-UI:t (sparas i `user_settings`, samma mönster som andra inställningar, se `core/settings.go` / `web/src/stores/settings.js`).

**Innan du bygger något nytt:** hitta och läs den befintliga Apprise-koden (sannolikt ett anrop i eller nära `core/stats-interesting.go` eller en egen fil, typ `core/apprise.go`/`core/notifications.go`). Den här funktionen ska **återanvändas** för bevakningsnotiser, inte byggas om från grunden. Ta reda på:
- Vilken funktion/signatur skickar notisen idag (t.ex. `sendAppriseNotification(title, body string) error`)?
- Skickas den till en global Apprise-URL, eller stöds flera URL:er/tjänster?
- Finns det redan ett meddelandeformat/mall att utgå från?

Om den befintliga integrationen är begränsad (t.ex. hårdkodad för "interesting"-flödet) — beskriv det du hittar och föreslå en minimal generalisering, men avstäm med användaren innan du refaktorerar något som redan fungerar.

## Datamodell

Föreslagen struktur (justera efter vad som faktiskt finns i schemat — verifiera `aircraft_data`- och `route_data`-kolumner innan du skriver migrationen):

### `watches`
En rad per bevakning.

| Kolumn | Typ | Beskrivning |
|---|---|---|
| `id` | SERIAL PK | |
| `name` | TEXT | Användarens eget namn på bevakningen, t.ex. "Boeing 747 inom 50km" |
| `enabled` | BOOLEAN, default true | Går att pausa utan att radera |
| `combinator` | TEXT ('AND'/'OR') | Hur `watch_conditions` för denna bevakning kombineras |
| `created_at` | TIMESTAMPTZ | |
| `updated_at` | TIMESTAMPTZ | |

### `watch_conditions`
En eller flera rader per bevakning — de faktiska kriterierna. Platt lista (inga nästlade grupper — se "Utanför scope" längst ner).

| Kolumn | Typ | Beskrivning |
|---|---|---|
| `id` | SERIAL PK | |
| `watch_id` | FK → `watches.id` (ON DELETE CASCADE) | |
| `field` | TEXT | Se tabellen "Matchningskriterier" nedan för giltiga värden |
| `operator` | TEXT | `equals`, `contains`, `starts_with`, `over`, `under`, `in_list` — beroende på fält, se tabellen |
| `value` | TEXT | Värdet att matcha mot. Numeriska fält (höjd/hastighet/avstånd/vertikalhastighet) lagras som text men parsas till nummer. |

### `watch_active_matches`
Håller reda på vilka flygplan som **just nu** matchar vilken bevakning, så att en notis bara skickas när ett flygplan **börjar** matcha (en notis per "sighting", enligt beslut nedan) — inte varje gång matchningen utvärderas (var 2:e sekund, samma cadence som `updateAircraftDataTicker`).

| Kolumn | Typ | Beskrivning |
|---|---|---|
| `watch_id` | FK → `watches.id` (ON DELETE CASCADE) | |
| `hex` | TEXT | ICAO 24-bit hex för planet |
| `matched_at` | TIMESTAMPTZ | När matchningen startade |

Unik på (`watch_id`, `hex`). Rad skapas när ett plan börjar matcha (→ trigger notis), rad tas bort när planet **slutar** matcha ELLER när planet försvinner helt (inte längre inom `RADIUS` / inte setts på X minuter — samma "försvunnet"-logik som redan finns för att avgöra att en flygning är avslutad, t.ex. i `core/stats-motion.go` eller motsvarande). Om planet dyker upp igen och matchar på nytt → ny rad → ny notis.

### `watch_notifications` (historik/logg)
En rad per skickad notis, oavsett kanal. Används för en "Träffhistorik"-vy i UI:t (se nedan) och för felsökning.

| Kolumn | Typ | Beskrivning |
|---|---|---|
| `id` | SERIAL PK | |
| `watch_id` | FK → `watches.id` (ON DELETE CASCADE) | |
| `hex` | TEXT | |
| `flight` | TEXT | Callsign vid matchningstillfället |
| `registration` | TEXT | |
| `snapshot` | JSONB | Ögonblicksbild av relevanta flygplansdata vid matchningstillfället (höjd, hastighet, avstånd, position, etc.) — för att kunna visa/felsöka en historisk träff även efter att planet försvunnit ur `aircraft_data` |
| `notified_at` | TIMESTAMPTZ | |
| `apprise_success` | BOOLEAN | Om Apprise-anropet lyckades |

### Ny/separat tabell för "första gången sedd" (behöver verifieras)
Om `aircraft_data` raderar eller skriver över gamla rader (retention), räcker inte `aircraft_data.first_seen` för att avgöra om ett plan verkligen aldrig setts förut — det säger bara "första gången i denna session/period". Kolla om det finns någon `flight_history`-liknande tabell (nämns i `docs/claude_code_brief.md` — `flight_history` med unik `(hex, first_seen)`) som redan fungerar som ett permanent arkiv. Om inte: skapa en minimal `known_aircraft (hex TEXT PRIMARY KEY, first_seen_at TIMESTAMPTZ)` som **aldrig rensas** och bara används för att avgöra "har detta hex någonsin setts förut" — oberoende av eventuell retention på andra tabeller.

## Matchningskriterier

Alla fält nedan ska gå att välja som `field` i en `watch_condition`. Kolumn-referenserna är bästa gissning utifrån CLAUDE.md och tidigare planer i repot — **verifiera exakta kolumnnamn i `aircraft_data`/`route_data` och i enrichment-koden (`core/registrations.go`, `core/routes.go`) innan implementation.**

| Kriterium | Operatorer | Datakälla (att verifiera) | Anmärkning |
|---|---|---|---|
| Tillverkare (manufacturer) | equals, contains | Enrichment via adsbdb (`core/registrations.go`) — kolla om manufacturer redan lagras eller behöver läggas till | adsbdb:s svar innehåller normalt tillverkare för civila plan; military/state kan sakna detta |
| Modell/typ | equals, contains | `aircraft_data.t` (ICAO-typkod, t.ex. `B738`) och/eller enrichment-fält med fullständigt modellnamn | Fråga: ska matchning ske mot ICAO-typkoden eller mot fritext-modellnamn? Stöd båda om möjligt (typkod exakt, modellnamn `contains`) |
| Land | equals | Sannolikt `registered_owner_country`-liknande fält från adsbdb, ELLER härlett från ICAO hex-allokering (24-bit-intervall per land) om inget sådant fält redan lagras | Notera: kan förväxlas med "till/från"-landet (flygplatsens land) — detta ska vara **flygplanets registreringsland**, inte ruttens |
| Flygbolag | equals, contains | Callsign → ICAO-flygbolagskod → `data/airlines.csv` (samma lookup som redan används för "Top Airlines") | Återanvänd befintlig airline-lookup-funktion |
| Till (destination) | equals | `route_data.destination_icao_code` / `destination_iata_code` | |
| Från (origin) | equals | `route_data.origin_icao_code` / `origin_iata_code` | |
| Registrering | equals, contains | `aircraft_data.r` | |
| ICAO 24-bit (hex) | equals, in_list | `aircraft_data.hex` | `in_list` för att kunna bevaka en lista av specifika plan (kommaseparerat i UI:t) |
| Avstånd till mig | over, under | Beräknat likt `LastSeenDistance` (cheap-ruler, `core/aircraft.go`) | **Begränsning:** plan utanför `RADIUS` spåras inte alls av Skystats idag, så "avstånd över X" är i praktiken begränsat till `RADIUS`. Nämn detta tydligt i UI:t (t.ex. tooltip) |
| Höjd | over, under | `aircraft_data`-höjdkolumn (barometric/geometric altitude — kolla vilken som används i motsvarande highest/lowest-logik i `core/stats-motion.go` och återanvänd samma) | |
| Hastighet | over, under | Ground speed-kolumn, samma som används av fastest/slowest-logiken | |
| Squawk-kod | equals, in_list | `aircraft_data`-squawk-kolumn (om den redan lagras — annars behöver den läggas till från `aircraft.json`) | Ge ett genvägsalternativ i UI:t: "Nödläge (7500/7600/7700)" som förifyller `in_list` med de tre koderna |
| Första gången sedd | (inget värde — kryssruta/flagga) | Se `known_aircraft`-tabellen ovan | Matchar bara den allra första gången ett hex dyker upp, aldrig igen för samma hex |
| Vertikalhastighet | over, under | Vertical rate-kolumn från `aircraft.json` (om den lagras — annars behöver den läggas till) | Ange om over/under gäller absolutbelopp (både brant stigning och sjunkning) eller signerat värde — rekommendation: signerat (positivt = stiger, negativt = sjunker) så användaren kan bevaka specifikt "stiger snabbare än X" |
| Callsign-mönster | contains, starts_with | `aircraft_data.flight` | UI kan erbjuda `*`-wildcard som översätts till `starts_with`/`contains` internt, eller SQL `LIKE` direkt |

## Matchningslogik

- En bevakning matchar ett flygplan om dess `watch_conditions` uppfylls enligt `combinator` (AND = alla villkor sanna, OR = minst ett villkor sant).
- Fält som saknar data för ett givet plan (t.ex. ingen rutt matchad än) räknas som **icke-matchande** för det villkoret (aldrig som automatisk träff eller fel).
- Matchning körs i samma takt som `updateAircraftDataTicker` (2s) — direkt efter att ett plans data uppdaterats i `aircraft_data`, eller som ett separat steg som läser nyligen uppdaterade rader. Undersök var det är minst kostsamt att hooka in detta (troligen i eller strax efter `updateAircraftDatabase()` i `core/aircraft.go`) — bör inte innebära en full tabellskanning varje tick om det går att undvika, återanvänd gärna samma `recentAircraftCache`-mönster som redan finns för att bara utvärdera ändrade plan.

### Notisfrekvens: en notis per "sighting"

Beslutat med användaren: en notis skickas när ett plan **börjar** matcha en bevakning (ny rad i `watch_active_matches` + rad i `watch_notifications` + Apprise-anrop). Så länge planet fortsätter matcha skickas **ingen** ny notis. När planet slutar matcha (villkoren blir falska igen, ELLER planet försvinner helt ur spårning) tas raden i `watch_active_matches` bort — om planet senare börjar matcha igen (samma eller ny "sighting") skickas en ny notis.

## Apprise-integration

Återanvänd befintlig Apprise-funktion (se "Bakgrund" ovan). Föreslaget meddelandeformat — anpassa efter vad som redan finns:

- **Titel:** `Bevakning "{watch.name}" — {registrering eller hex}`
- **Text:** flight/callsign, typ, registrering, från→till (om känt), höjd, hastighet, avstånd, tidpunkt.

Om den befintliga integrationen stöder flera Apprise-mål/kanaler: överväg om varje bevakning ska kunna peka på en specifik kanal (t.ex. olika Discord-kanaler för olika bevakningstyper) eller om alla bevakningar delar samma globala Apprise-mål som "interesting aircraft" redan använder. **Fråga inte användaren om detta i förväg om det är enkelt att stödja per-bevakning — bygg det flexibelt, men avstäm om det visar sig vara en stor omarbetning.**

## API

Föreslagna endpoints under `/api/watches` (Gin, samma mönster som `/api/stats/...`):

- `GET /api/watches` — lista alla bevakningar med sina villkor
- `POST /api/watches` — skapa ny bevakning (name, combinator, conditions[])
- `PUT /api/watches/:id` — uppdatera (inkl. `enabled`-togglingen för paus/aktivera)
- `DELETE /api/watches/:id` — radera (cascadar till conditions, active_matches, men **inte** notification-historiken — behåll den för spårbarhet, eller fråga användaren om historiken ska raderas med)
- `GET /api/watches/:id/hits` eller `GET /api/watches/hits` — träffhistorik (paginerad), för historik-vyn i UI:t

## Frontend

Ny flik i huvudnavigeringen: **"Bevakningar"** (samma nivå som Activity/Route Information/Interesting Aircraft/Record Holders).

Innehåll:
1. **Lista över befintliga bevakningar** — namn, status (aktiv/pausad-toggle), antal villkor, redigera/radera-knappar, "skapa ny"-knapp.
2. **Skapa/redigera-formulär** (modal eller egen vy) — namn, AND/OR-väljare, dynamisk lista av villkorsrader (fält-dropdown → operator-dropdown filtrerad efter fältets giltiga operatorer → värde-input, med typanpassat UI: dropdown för fält med kända värden som "land"/"flygbolag" om sådana listor redan finns i appen, fritext annars, kryssruta för "Första gången sedd", genvägsknapp för nödsquawk).
3. **Träffhistorik** — lista över senaste notiser (alla bevakningar, eller filtrerat per bevakning), med tidpunkt, plan-info och länk/highlight till planet om det fortfarande är aktivt (kan återanvända mönster från "Interesting Aircraft"-modalen om ett liknande finns).

## Att utforska innan du börjar

- Den befintliga Apprise-notisfunktionen — exakt fil, funktionssignatur, hur den anropas idag för "interesting aircraft".
- Exakta kolumnnamn i `aircraft_data` för: höjd (barometric vs geometric), hastighet, vertikalhastighet, squawk (finns dessa redan, eller behöver de läggas till från `aircraft.json`-parsningen i `core/readsb.go`?).
- Om manufacturer/land redan lagras via adsbdb-enrichment eller om det behöver läggas till.
- Om `flight_history` (från `claude_code_brief.md`) redan är ett lämpligt permanent arkiv att använda för "första gången sedd", eller om en ny separat tabell behövs.
- Befintlig "försvunnet plan"-logik (för att veta när `watch_active_matches`-rader ska rensas när ett plan lämnar spårningen utan att sluta matcha villkoren).
- Hur nästa migrationsnummer ska väljas (`ls migrations/ | sort | tail -5`, samma rutin som i tidigare planer).

## Beslut redan tagna (behöver inte diskuteras igen)

- Notiskanal: Apprise (befintlig integration återanvänds).
- Villkorslogik: AND/OR väljbart per bevakning (platt lista, inga nästlade grupper).
- Notisfrekvens: en notis per sighting (ny notis när matchning startar, tystnad tills matchningen upphör och ev. återupptas).
- UI-placering: egen flik "Bevakningar".
- Extra kriterier utöver de ursprungligen listade: squawk-kod (inkl. nödlägesgenväg), första gången sedd, vertikalhastighet, callsign-mönster/wildcard.

## Utanför scope (för senare, ta inte upp nu)

- Nästlade AND/OR-grupper (t.ex. `(A AND B) OR C`) — den platta AND/OR-modellen ovan täcker de flesta rimliga fall; om användaren senare vill kombinera flera bevakningar med OR sinsemellan går det redan idag genom att skapa flera separata bevakningar.
- Web push-notiser eller e-post som alternativ/komplement till Apprise.
- Konfigurerbar cooldown-tid per bevakning (utöver "en per sighting").
- Retention/rensning av `watch_notifications`-historiken (växer obegränsat idag — lägg till om det blir ett problem).
