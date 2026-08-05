# Route on sight — hämta färdplanen när callsignet dyker upp

**Datum:** 2026-08-05
**Status:** Godkänd design, redo för implementationsplan

## Problemet

Färdplaner visas ofta inte medan planet är synligt, trots att rutten finns hos källan och till slut hamnar i databasen.

Uppmätt live på 192.168.1.251 (SAS1456 / SE-RZZ, hex `4acb5a`):

| Händelse | Tid | Δ från första observation |
|---|---|---|
| Planet dyker upp | 11:19:18 | — |
| Planet försvinner ur Current Sightings | 11:22:26 | +3 min 08 s |
| Rutten skrivs till `route_data` | 11:24:19 | +5 min 01 s |
| `flight_history` backfillas → modalen | 11:29:27 | +10 min 09 s |

Rutten kom alltså 1 min 53 s efter att planet redan lämnat listan.

### Orsak

`updateRoutesTicker` är 300 s med fri fas (`core.go:117`), helt frikopplad från när ett plan dyker upp. Ett plan som kommer in strax efter ett tick väntar nästan fem minuter.

Det biter hårt eftersom planen sällan är kvar så länge:

- **71 %** av flygningarna (514 av 720 över tre dygn) syns kortare än 5 minuter
- Av 75 unika callsign under 12 timmar hann **33 %** aldrig få sin rutt visad innan planet försvann
- Genomsnittlig fördröjning första observation → rutt: **166 sekunder**

Var tredje flygning missar alltså sitt eget tidsfönster.

### Avgränsning

Detta är ett *latensproblem* och ska inte förväxlas med *täckning*. Callsign som källan inte känner till (t.ex. CAT250, ad hoc-flyg utanför de schemalagda databaserna) får aldrig någon rutt hur snabbt vi än frågar. Se `route-matching-work`-anteckningen. Den här designen löser bara latensen.

## Mål

Rutten ska finnas i `route_data` inom ett par sekunder efter att ett callsign först syns, så att Current Sightings hinner visa den medan planet är kvar.

**Utanför scope:** modalens ögonblicksbild i `flight_history`. Den skrivs av distansjobbet på en egen 300 s-tickare och förbättras automatiskt från ~10 min till ~2,5 min i snitt som följd av den här ändringen, eftersom distansjobbet hittar rutten redan vid sitt nästa tick. Att göra även modalen omedelbar kräver ändringar i observationsfrågan och riskerar felattribuering, eftersom `route_data` är nycklat på callsign och skrivs över dagligen — vilket är just därför ögonblicksbilden finns.

## Design

Ny fil `core/routes-onsight.go` med ett enda ansvar: *"det här callsignet saknar rutt — hämta den nu"*. Den skriver enbart till `route_data`.

### Komponent

En `routeOnSight`-struct som speglar `photoLookup` i `photos.go`:

```go
mu       sync.Mutex
pending  map[string]bool       // uppslag just nu i luften
cooldown map[string]time.Time  // callsign → tidigast nästa försök
```

`pending` behövs eftersom ett anrop får ta upp till 5 s medan ticken går var 2:a sekund — utan den skulle samma callsign frågas två–tre gånger om.

Cooldown-tiderna ligger som **fält på structen**, inte som direktanvända konstanter, så testerna kan sätta dem negativa för att framtvinga utgång. Samma grepp som `hitTTL`/`missTTL` i `photos.go` och `photos_test.go:161`.

### Dataflöde per tick

1. `updateAircraftDatabase` har redan `enrichment` i handen (`aircraft.go:58`).
2. Plocka ut plan med `Flight != ""` vars enrichment saknar både `OriginIcao` och `DestinationIcao`.
3. Filtrera genom `claim()` — hoppar över callsign som är pending eller i cooldown, och avduplicerar inom ticken.
4. Finns kandidater: **en** goroutine med **en** batchad POST via befintliga `getRoutes()`, därefter `insertRoutes()`.
5. `insertRoutes` returnerar redan `map[string]bool` över matchade callsign. Matchat → släpp ur `pending`. Omatchat → cooldown.

**Trådordning, viktig för korrektheten:** steg 2–3 körs *synkront* i ticken, så `pending` är uppdaterad innan ticken återvänder. Annars hinner nästa tick (2 s senare) reservera samma callsign igen medan anropet fortfarande är i luften. Steg 4–5 — själva HTTP-anropet, insättningen och den efterföljande släpp/cooldown-uppdateringen — körs på goroutinen. Låset tas alltså kort i ticken och kort igen när svaret kommer, aldrig under nätverksanropet.

Nästa tick ser rutten via enrichment och Current Sightings visar den.

Detektionen är gratis: `fetchAircraftEnrichment` (`current-sightings.go:162`) gör redan en DB-runda per tick med hela snapshoten och LEFT JOIN:ar `route_data` via `unnest`, så varje hex får alltid en rad. Inga nya frågor tillkommer.

### Gränsdragning mot den befintliga stegen

On-sight-vägen rör **aldrig** `route_processed` eller `route_attempts`. Den 300 s-långa stegen (`classifyRouteAttempt`, max 5 försök) äger den state-maskinen och förblir orörd. Ingen delad muterbar state, alltså inga race conditions mellan vägarna.

Det ger en bonus: `checkRouteExists` letar efter `route_data`-rader uppdaterade senaste timmen, så när stegen sedan når planet ser den rutten redan finnas och markerar det klart **utan** API-anrop. Vi byter alltså inte till fler anrop — vi flyttar dem tidigare.

### Adapter

`buildRouteApiRequestBody` läser `LastSeenLat`/`LastSeenLon` (`sql.NullFloat64`), medan snapshoten har råa `Lat`/`Lon` (`float64`). Kandidaterna paketeras om innan de skickas in.

## Anropsvolym

Nya callsign uppträder i låg takt — uppmätt 8–13 per timme över åtta timmar. Det ger ~10 extra anrop i timmen, jämförbart med eller färre än dagens 288 per dygn, och en del av dem försvinner igen tack vare `checkRouteExists`-bonusen ovan. Ingen risk att överbelasta en gratistjänst.

Vid uppstart kan första ticken se 10–20 synliga plan utan rutt. Det blir **en** batchad POST med 10–20 callsign; befintlig kod skickar redan upp till 100 per anrop.

## Cooldown-värden

| Konstant | Värde | Motiv |
|---|---|---|
| `routeUnknownCooldown` | 30 min | Plan syns typiskt under 5 min, så vi frågar på sin höjd en gång per besök. Stegen sköter omförsöken. |
| `routeErrorCooldown` | 2 min | Ett nätverksfel säger ingenting om callsignet. En kort blipp ska inte kosta oss flygningen. |

Att skilja dem åt är poängen: `unknown` är ett svar om callsignet, `error` är ett svar om nätverket. Samma cooldown för båda vore fel.

## Felhantering

- **`getRoutes` fallerar** (timeout, nätverk, trasig JSON) → logga på Warn med antal callsign, sätt `routeErrorCooldown` på de reserverade, släpp `pending`. Felet propagerar aldrig — ticken ska inte bry sig.
- **DB-fel i `insertRoutes`** loggas redan per callsign där inne. De uteblir ur returmappen, hamnar i cooldown och fångas av stegen. Rätt fallback utan ny kod.
- **`pending` släpps med `defer`**, så ingen tidig retur kan lämna ett callsign permanent låst.
- **Inga kandidater** → ingen goroutine, ingen allokering. Noll kostnad på de allra flesta tick.
- **Ingen `recover()`** — befintliga `go notifier.*`-anrop (`records-ingest.go:135`, `stats-interesting.go:204`, `watches-engine.go:390`) har ingen heller. Kodbasens stil följs hellre än att införa ett avvikande mönster.

## Minnestillväxt

`cooldown` växer med ett fält per omatchat callsign. Vid ~240 flygningar per dygn är det lågt, men obegränsat över månader. `claim()` rensar därför utgångna poster när mappen passerar 500 poster — billigt, självbegränsande, kräver varken timer eller extra goroutine.

## Testning

`core/routes-onsight_test.go`, ren logik utan I/O enligt CLAUDE.md. Kandidaturvalet bryts ut som en ren funktion `(snapshot []Aircraft, enrichment map[string]aircraftEnrichment) → []Aircraft` så det kan testas utan databas.

1. `claim` ger true första gången, false medan pending, false under cooldown, true efter utgång
2. Matchat släpper ur `pending`; omatchat får `unknown`-cooldown; fel får `error`-cooldown
3. Rensningen tar bort utgångna poster över tröskeln men behåller levande
4. Urvalet hoppar över tomt `Flight`, hoppar över plan som redan har rutt, och avduplicerar samma callsign inom en tick

HTTP- och DB-lagren testas medvetet inte — de har ingen harness i repot, och `getRoutes`/`insertRoutes` återanvänds oförändrade.

## Verifiering efter deploy

Mät om på samma sätt som problemet mättes: för flygningar med unikt callsign, andelen där `route_data.last_updated <= aircraft_data.last_seen`. Baslinjen är **66,7 %** (50 av 75 över 12 timmar). Målet är att den går mot ~100 % för callsign som källan över huvud taget känner till.
