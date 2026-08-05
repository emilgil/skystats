# Sökfunktion för flygningar — Spec till Claude Code

> **För Claude Code:** Detta är en spec, inte en färdig implementationsplan. Börja med att utforska nuvarande kodbas (särskilt `core/api.go`, migrations-mappen, `registrations.go`/`routes.go`, `data/`-paketet, och `web/src/components/`) för att bekräfta exakta tabellnamn, kolumner och mönster innan du skriver kod. Stäm av din plan med mig innan du implementerar. Detta bygger vidare på leaderboard-arbetet i `claude_code_brief.md` (migration `000012_add_period_records`) — återanvänd `flight_history`-tabellen och samma period-konventioner där det går.

## Mål

En sökflik där man kan filtrera fram flygningar ur historiken på valfri kombination av kriterier: tidsperiod, tillverkare, modell, registreringsland, avgångs-/ankomstflygplats, höjd- och hastighetsgränser, m.fl. Resultatet visas som en sorterbar, paginerad tabell.

## Beslut som redan är tagna (från klargörande frågor)

| Fråga | Beslut |
|---|---|
| Datakälla | `flight_history` (avslutade flygningar), inte live-listan |
| Placering | Ny dedikerad "Sök"-flik i huvudnavigeringen |
| Resultatfunktioner | Kolumnsortering + klick för detaljvy + CSV-export |
| "Land" | Registreringsland (inte avgångs-/ankomstland) |
| Hastighets-/höjdmått | `ground_speed` och `barometric_altitude` (samma som Record Holders-fliken använder idag) |
| Gruppering | En rad per flygning (ingen dedupe per flygplan) |
| Fritextsök | Ja — matchar flight/registrering/hex |
| Resultatstorlek | Paginering, 50 träffar/sida |

## Sökparametrar — v1 (det som efterfrågades)

1. **Tidsperiod** — samma preset-konventioner som leaderboard-arbetet: `24h`, `7d`, `30d`, `90d`, `365d`, `all_time`, plus valfritt datumintervall (from/to). Filtrerar på `first_seen`.
2. **Tillverkare** — t.ex. "Boeing", "Airbus".
3. **Modell** — t.ex. "A320", "737 MAX 8".
4. **Registreringsland** — landet flygplanet är registrerat i.
5. **Avgångsflygplats** — matchar `origin`-koden i `flight_history`.
6. **Ankomstflygplats** — matchar `destination`-koden.
7. **Höjd över/under** — mot `barometric_altitude`, med en "över"/"under"-toggle + värde.
8. **Hastighet över/under** — mot `ground_speed`, samma toggle-mönster.
9. **Flygbolag** — mot flightnummer-prefix via `airlines.csv`-lookupen som redan finns i `data/`-paketet (samma mappning som "Top Airlines"-statistiken använder). Dropdown eller fritext med autocomplete.
10. **Interesting-märkta flygplan** — filter på militär/regering/polis/civil, samma klassificering som Interesting Aircraft-fliken använder (matchning mot plane-alert-db). Antingen en enda "endast intressanta"-toggle + kategori-dropdown, eller fyra kryssrutor (en per kategori) — se öppen fråga nedan.
11. **Ruttstatus (avgång/ankomst saknas)** — två fristående väljare, ett för avgångsflygplats och ett för ankomstflygplats, var och en med tre lägen: *Alla* (ingen filtrering, default) / *Endast med känd* (`origin`/`destination` är satt) / *Endast utan känd* (`origin`/`destination` är NULL eller tom sträng). Fristående från punkt 5–6 ovan (som filtrerar på en specifik flygplatskod) — dessa två väljare används för att hitta flygningar som helt saknar ruttberikning, t.ex. sätt båda till "Endast utan känd" för att hitta flygningar utan någon känd rutt alls.

### ⚠️ Öppen fråga för Claude Code att lösa under utforskning

`flight_history` har enligt `claude_code_brief.md` kolumnen `type` (ICAO-typkod, t.ex. `A320`) men **ingen egen kolumn för tillverkare, modell eller registreringsland**. Innan filtren för tillverkare/modell/land byggs, undersök:

- Hur `/api/stats/types` idag mappar typkoder till tillverkare/modell (finns troligen redan en lookup/tabell — återanvänd den, skapa inte en ny parallell mappning).
- Var registreringsland kommer ifrån. `registrations.go` berikar registreringar från adsbdb var 30:e sekund — leta reda på vilken tabell det skriver till (troligen en `aircraft`/`registrations`-tabell separat från `flight_history`, kopplad via `hex` eller `registration`). Sökningen mot tillverkare/modell/land blir sannolikt en JOIN mellan `flight_history` och den tabellen.
- Om ingen sådan mappning finns för land: registreringsprefix (t.ex. `G-` → Storbritannien) kan behöva härledas separat — flagga detta till mig innan du bygger en sådan mappning, det kan finnas enklare lösningar (t.ex. redan tillgängligt fält från adsbdb).
- **Interesting-klassificering:** enligt `CLAUDE.md` beräknas "interesting seen" (militär/regering/polis/civil) i ett separat 120s-jobb (`stats-interesting.go`), matchat mot en lokal kopia av plane-alert-db. Det är oklart om denna klassificering redan skrivs in i `flight_history` (t.ex. som en kategori-kolumn) eller bara finns i en separat tabell/cache kopplad via `hex`/`registration`. Undersök detta — om klassificeringen inte redan finns tillgänglig för historiska flygningar kan sökfiltret behöva en JOIN mot plane-alert-db-referensdatan (samma matchning som `stats-interesting.go` gör), eller så behöver `flight_history` utökas med en kategori-kolumn som sätts vid ingest. Stäm av vilket alternativ som är rimligast innan du bygger.

## Förslag på fler sökparametrar (v2 / nice-to-have)

Utöver det du bad om, här är fler filter som passar naturligt in i samma sökfunktion, baserat på vad som redan finns i datamodellen:

- **Flygnummer / callsign, registrering, hex** — redan täckt av fritextfältet, men skulle också kunna vara egna strukturerade filter om fritext känns för trubbigt.
- **Flugen distans** (`distance_flown`) över/under — hur långt planet faktiskt flög.
- **Ruttavstånd** (`route_distance`) över/under — det teoretiska avståndet mellan flygplatserna.
- **Kvarvarande distans vid sista observation** (`distance_remaining`) — användbart för att hitta flygningar som "försvann" tidigt ur räckvidden.
- **Flygningens varaktighet** (`last_seen − first_seen`) över/under.
- **Inhemsk vs internationell** — filtrera på om avgång/ankomst matchar `DOMESTIC_COUNTRY_ISO` eller ej (samma logik som redan används för Domestic/International Airports-statistiken).
- **Hastighets-/höjdmått som dropdown** — om `ground_speed`/`barometric_altitude` visar sig vara för begränsande i praktiken kan `true_air_speed`/`indicated_air_speed` och `geometric_altitude` läggas till som valbara mått senare (avvisat för v1 för att hålla UI:t enkelt).

Jag rekommenderar att bygga v1-listan ovan och lägga resten i en backlog snarare än att bygga allt på en gång.

## API

Ny endpoint, t.ex. `GET /api/search/flights`, som accepterar samtliga filter som query-parametrar (alla optional, kombineras med AND):

```
GET /api/search/flights
  ?period=30d              // eller from=YYYY-MM-DD&to=YYYY-MM-DD för valfritt intervall
  &manufacturer=Boeing
  &model=A320
  &country=GB
  &origin=LHR
  &destination=JFK
  &altitude_op=gte|lte
  &altitude_value=35000
  &speed_op=gte|lte
  &speed_value=450
  &airline=Ryanair
  &interesting=military|government|police|civilian   // utelämnad = ingen filtrering på interesting-status
  &origin_status=any|known|unknown    // default any
  &destination_status=any|known|unknown
  &q=free-text              // matchar flight/registration/hex
  &sort=first_seen|ground_speed|barometric_altitude|...  // whitelist, inte fri SQL
  &dir=asc|desc
  &page=1
  &page_size=50
```

Svar: `{ results: [...], total_count, page, page_size }`.

**Säkerhet:** bygg dynamisk WHERE-klausul med parametriserade queries (pgx), inte stränginterpolation. `sort`-fältet måste valideras mot en whitelist av tillåtna kolumnnamn — mappa aldrig query-param direkt till SQL-kolumnnamn.

**CSV-export:** samma filter, men utan paginering. Sortering ärvs från tabellens aktuella `sort`/`dir` (dvs "det du ser är det du får") — om användaren inte har sorterat manuellt, använd `first_seen desc` (nyaste flygningen först) som standard. Sätt ett rimligt tak (förslag: 10 000 rader) så en ospecifik sökning inte drar ner hela `flight_history`. Om taket träffas, visa ett meddelande i UI:t om att förfina sökningen.

## Frontend

- Ny flik "Sök" (samma mönster som `TabActivity`/`TabRouteStats`/etc — Svelte-komponent, DaisyUI-styling).
- Filterformulär överst: fritextfält, tidsperiod-väljare (återanvänd komponenten från leaderboard-datumfiltret om den redan finns), dropdowns för tillverkare/modell/land/flygbolag, flygplats-fält för avgång/ankomst, min/max-fält för höjd och hastighet, samt filter för interesting-kategori (t.ex. fyra kryssrutor: militär/regering/polis/civil, eller "endast intressanta"-toggle om det känns enklare), samt två tre-lägesväljare "Avgångsflygplats" och "Ankomstflygplats" (Alla / Endast med känd / Endast utan känd) för att hitta flygningar utan identifierad rutt.
- Resultattabell under formuläret: sorterbara kolumnrubriker, 50 rader/sida med paginering, klick på rad öppnar en detaljmodal med samtliga fält för den flygningen (inklusive de hastighets-/höjdmått som inte används som filter, om de finns).
- "Exportera CSV"-knapp ovanför tabellen, anropar exportendpointen med aktuella filter.
- Sök körs troligen debounced eller via en explicit "Sök"-knapp (rekommenderar explicit knapp — undviker onödiga anrop mot en potentiellt stor tabell vid varje tangenttryckning).

## Nästa steg för Claude Code

1. Utforska kodbasen enligt punkterna ovan (typmappning, registreringsland, befintligt datumfilter-UI från leaderboard-arbetet).
2. Föreslå exakt tabellstruktur/JOIN för tillverkare/modell/land och stäm av med mig innan du bygger.
3. Skriv en fullständig task-för-task-plan (i samma format som `2026-07-26-hide-show-cards-spec-updated.md`) innan du börjar koda.
