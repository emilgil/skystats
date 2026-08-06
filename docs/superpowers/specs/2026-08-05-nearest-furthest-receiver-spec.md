# Spec: Rekordtabeller "Närmast" & "Längst bort" (avstånd till mottagaren)

> **För Claude Code:** Detta är en spec, inte en färdig steg-för-steg-plan. Börja med att utforska nuvarande kodbas (se "Steg 0" nedan) innan du skriver någon kod, och stäm av din implementationsplan med användaren innan du börjar bygga. Skystats-repot: `github.com/emilgil/skystats`.

## Mål

Två nya rekordtabeller på dashboarden, i samma stil som befintliga Fastest/Slowest/Highest/Lowest:

- **Närmast** — det kortaste 2D-avstånd ett flygplan någonsin uppmätts på till mottagaren.
- **Längst bort** — det längsta 2D-avstånd ett flygplan någonsin uppmätts på till mottagaren (dvs. din faktiska uppmätta räckvidd).

Dessa ska kunna rensas på precis samma sätt som övriga rekordtabeller redan kan idag.

---

## Steg 0: Utforska nuvarande arkitektur (gör detta först)

Det finns två tidigare specar/briefs i projektets kunskapskälla som är relevanta men vi vet inte vilken som faktiskt är den nuvarande sanningen i koden:

1. **Äldre mönster** ("Distance Leaderboards"-planen): en tabell per stat (`fastest_aircraft`, `highest_aircraft` osv), en `*_processed`-flagga per stat på `aircraft_data`, en ticker som periodvis scannar oprocessade rader, beräknar en metrik, upsertar till "top 50"-tabellen och trimmar.
2. **Nyare mönster** (odaterad brief om periodfiltrerade leaderboards): ett enhetligt system med `flight_history` (permanent arkiv per avslutad flygning) + `records` (en enda leaderboard-tabell med kolumnerna `category`, `period_type`, `metric_name`, `metric_value` osv, filtrerad per tidsperiod 24h/7d/30d/90d/365d/all-time, topp 100 per kategori+period). De 7 gamla tabellerna skulle döpas om till `*_deprecated` som ett mellansteg.

Vi vet inte om ombyggnaden till det nyare mönstret är påbörjad, klar, eller inte alls påbörjad. **Kontrollera detta i den faktiska koden** (finns `records`- och `flight_history`-tabellerna? Finns `*_deprecated`-tabeller? Vad läser API:et från idag?) innan du bestämmer hur "Närmast"/"Längst bort" ska byggas in:

- Om det nya enhetliga systemet är live → bygg in de här två som nya `category`-värden i `records`/`flight_history` (se namnförslag nedan), med samma periodfiltrering som övriga kategorier.
- Om det gamla per-tabell-mönstret fortfarande gäller → följ samma mönster som `furthest_flown_aircraft` (egna tabeller, egna `*_processed`-flaggor, egen ticker-funktion), men **läs "Mätmetod" nedan noga** — det finns en viktig skillnad mot hur de befintliga tabellerna fungerar (se nedan).

---

## Avståndsberäkning: 2D, inte 3D

Avståndet ska räknas som **2D-avstånd (marktäckning)** — great-circle-avstånd mellan mottagarens position (`LAT`/`LON`) och flygplanets senaste kända position, **utan** hänsyn till flyghöjd.

Varför 2D och inte 3D (slant range):
- Konsekvent med all annan avståndsberäkning i appen idag (`LastSeenDistance`, `RADIUS`-filtreringen, `DestinationDistance`) som redan är 2D via cheap-ruler.
- Om avståndet räknades i 3D skulle ett flygplan kunna visa ett värde som överstiger den konfigurerade `RADIUS`-inställningen (eftersom 3D-avstånd = sqrt(2D² + höjd²) alltid är ≥ 2D-avståndet) — förvirrande för användaren.

Undersök om `LastSeenDistance` (eller motsvarande fält) redan är exakt denna 2D-metrik — om så, återanvänd samma beräkningsfunktion/logik istället för att duplicera den. Om `LastSeenDistance` visar sig vara något annat, avgör om en ny gemensam funktion behövs.

---

## Mätmetod: löpande min/max under hela flygningen, inte en engångs-snapshot

**Detta är den viktigaste designskillnaden mot befintliga rekordtabeller, och skälet till att detta inte kan implementeras som ett rakt kopiera-klistra av `fastest_aircraft`-mönstret.**

Befintliga tabeller (Fastest/Highest/Lowest osv) sparar en engångs-"snapshot" — värdet vid det godtyckliga ögonblick raden råkar processas av tickern, inte ett sant löpande min/max under hela tiden planet är synligt. Det är en känd, accepterad begränsning för de statsen.

För avstånd-till-mottagare fungerar inte samma mönster: ett flygplan som kommer in i räckviddscirkeln och sedan flyger ut åt ett annat håll kan mycket väl ha ett större avstånd vid utflygning än vid inflygning (eller tvärtom, beroende på flygbanans vinkel). En enda snapshot vid ett godtyckligt bearbetningstillfälle skulle missa det verkliga extremvärdet.

**Krav:** avståndet till mottagaren ska jämföras mot ett sparat min- och max-värde **för varje ny position** medan flygplanet är synligt (dvs. i samma hot path som redan uppdaterar aircraft-positioner var 2:a sekund, se `updateAircraftDatabase`/`aircraft.go`), inte bara vid ett enda bearbetningstillfälle senare. Praktiskt:

- Två nya nullable kolumner per flygning (t.ex. `min_distance_receiver`, `max_distance_receiver`, plus motsvarande höjd/bearing-kolumner för den punkten — se "Extra kontext" nedan) som uppdateras med `LEAST()`/`GREATEST()`-logik vid varje positionsuppdatering.
- Det slutgiltiga värdet skrivs till rekordtabellen/`records` när flygningen räknas som avslutad — enligt den odaterade briefen finns redan kod som "utvärderar en flygning som färdig" och skriver till `flight_history`/de gamla tabellerna vid det tillfället. Hitta den koden och hantera de två nya statsen på samma ställe, inte via en separat periodisk ticker som bara tar en snapshot.

Om steg 0 visar att det gamla per-tabell-mönstret fortfarande gäller (ingen "flygning avslutad"-logik finns än), fråga användaren hur "flygning avslutad" bäst avgörs i nuvarande kod (t.ex. ett tidsfönster sedan `last_seen`) innan du implementerar en engångs-ticker-variant — bygg inte en ren snapshot-ticker som bara kopierar `fastest_aircraft`-mönstret, det ger fel resultat enligt ovan.

---

## Tabellinnehåll (kolumner)

Utöver de kolumner övriga rekordtabeller redan visar (Reg, Typ, Avstånd, Först/Sist sedd):

- **Höjd** vid mätpunkten (dvs. flyghöjden när närmaste/längsta avståndet uppmättes) — ger sammanhang, t.ex. skiljer på "långt bort men på hög höjd" (väntat) vs "långt bort och lågt" (mer imponerande räckviddsmässigt).
- **Riktning/bearing** (kompassriktning) från mottagaren vid mätpunkten — visar åt vilket håll rekordet sattes.

Namn i UI: **"Nearest"** och **"Furthest"**, som två nya kort i samma Record Holders-flik (`TabMotionStats.svelte` eller motsvarande), med samma generiska tabellkomponent (`MotionStats.svelte`) som övriga kort återanvänder.

---

## Namngivning (undvik krock)

Om det enhetliga `records`-systemet är live: välj `category`-värden som inte krockar med de befintliga (`fastest`, `slowest`, `highest`, `lowest`, `furthest_flown`, `longest_route`, `most_remaining`). Förslag: `nearest` och `furthest_range` (medvetet olikt `furthest_flown`, som är ett annat mått — avstånd från startflygplats, inte från mottagaren). Verifiera att inget annat i koden redan använder de namnen innan du låser dem.

Om det gamla per-tabell-mönstret gäller: t.ex. `nearest_aircraft` / `furthest_range_aircraft` som tabellnamn, `nearest_processed` / `furthest_range_processed` som flaggor — men se "Mätmetod" ovan, snapshot-tickerns processed-flagga-mönster passar inte rakt av här.

---

## Rensning (viktigt, glöm inte)

De två nya tabellerna/kategorierna **måste** kunna rensas på exakt samma sätt som övriga rekordtabeller redan kan idag. Hitta den befintliga rensningsmekanismen i koden (troligen någon admin-/settings-funktion som tömmer en eller flera rekordtabeller — leta i `Settings.svelte` och motsvarande backend-endpoint) och koppla in Nearest/Furthest på samma sätt, oavsett vilket av de två arkitekturmönstren från Steg 0 som gäller.

---

## Explicit utanför scope

- Ingen räknare för "hur ofta flyger plan rakt över mig" — användaren har bekräftat att det inte behövs nu. Endast de två avståndstabellerna (Nearest, Furthest) ingår i denna spec.
- 3D/slant range-avstånd ingår inte.

---

## Innan du börjar koda

Stäm av med användaren:
1. Vilket av de två arkitekturmönstren (Steg 0) som faktiskt gäller i koden just nu.
2. Var/hur "flygning avslutad" bäst avgörs i nuvarande kod, om det inte redan finns en tydlig triggerpunkt.
3. Var den befintliga rensningsmekaniken för rekordtabeller finns, så den kan återanvändas konsekvent.
