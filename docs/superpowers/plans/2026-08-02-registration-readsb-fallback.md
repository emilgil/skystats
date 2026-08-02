# readsb Registration Fallback Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the aircraft detail modal fall back to the readsb `r` registration (already in `aircraft_data`) when adsbdb has none, so registration-keyed external links appear.

**Architecture:** Single backend change in `core/aircraft-detail.go`. The endpoint's existing step-2 query against `aircraft_data` is extended to also select `ad.r`; when adsbdb (step 1) produced no registration, the trimmed readsb value is used. No frontend, DB migration, or new dependency.

**Tech Stack:** Go, pgx, Gin.

## Global Constraints

- Change is confined to `core/aircraft-detail.go`. No other Go file, no SQL migration, no frontend.
- adsbdb registration keeps priority; readsb `r` is used only when the adsbdb value is absent or an empty string.
- readsb `r` is trimmed with `strings.TrimSpace` before use; an empty result is treated as no registration.
- No new external HTTP calls. The value comes from the existing `aircraft_data` row.

---

### Task 1: Add readsb `r` fallback to the aircraft detail endpoint

**Files:**
- Modify: `core/aircraft-detail.go` (add `strings` import; extend step-2 query + scan; add fallback block)

**Interfaces:**
- Consumes: `aircraft_data.r` (text, may be empty) for the hex, via the existing step-2 query that already selects the newest `aircraft_data` row.
- Produces: nothing consumed by other tasks — this is the only task. Externally, `GET /api/stats/aircraft/:hex` now returns a non-null `registration` for aircraft that have a readsb `r` but no adsbdb row.

- [ ] **Step 1: Add the `strings` import**

In `core/aircraft-detail.go`, add `"strings"` to the import block (keep the block gofmt-sorted — it goes after `"sort"`, before `"time"`):

```go
import (
	"context"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)
```

- [ ] **Step 2: Declare the `readsbReg` scan target**

Find the step-2 variable declarations (currently):

```go
	// 2) Live status: newest aircraft_data row for this hex, airline via route_data.
	var flight, airlineName *string
```

Change the first declaration line to also declare `readsbReg`:

```go
	// 2) Live status: newest aircraft_data row for this hex, airline via route_data.
	var flight, airlineName, readsbReg *string
```

- [ ] **Step 3: Select `ad.r` and scan it**

In the same step-2 query, add `, ad.r` to the end of the SELECT list and `&readsbReg` to the end of the `Scan(...)` call.

The SELECT's final line changes from:

```go
		       ad.last_seen, rt.airline_name
```

to:

```go
		       ad.last_seen, rt.airline_name, ad.r
```

The `Scan(...)` call changes from:

```go
		Scan(&flight, &altBaro, &gs, &track, &distance, &bearing, &lat, &lon, &lastSeen, &airlineName)
```

to:

```go
		Scan(&flight, &altBaro, &gs, &track, &distance, &bearing, &lat, &lon, &lastSeen, &airlineName, &readsbReg)
```

- [ ] **Step 4: Add the fallback block after the step-2 `if err == nil { ... }` block**

Immediately after the closing brace of the step-2 `if err == nil { ... }` block (the block that sets `operator`/`live`, ending just before the `// 3) History:` comment), insert:

```go
	// Fallback: adsbdb had no registration — use the readsb `r` from the feed
	// (aircraft_data.r), which covers far more aircraft. adsbdb keeps priority.
	if readsbReg != nil {
		if trimmed := strings.TrimSpace(*readsbReg); trimmed != "" {
			if cur, ok := resp["registration"].(*string); !ok || cur == nil || *cur == "" {
				resp["registration"] = trimmed
			}
		}
	}
```

Note on the type assertion: step 1 sets `resp["registration"]` to a `*string` only when the adsbdb value is non-nil (otherwise it stays the initial `nil`). `resp["registration"].(*string)` therefore yields `ok == false` when it is still the untyped initial `nil`, and a non-nil `*string` when adsbdb set one — both handled by the condition above.

- [ ] **Step 5: Build and vet**

Run: `cd core && go build ./... && go vet ./...`
Expected: both succeed with no output (clean compile, no vet warnings).

- [ ] **Step 6: Commit**

```bash
git add core/aircraft-detail.go
git commit -m "feat: fall back to readsb registration in aircraft modal when adsbdb lacks it

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Self-Review

**Spec coverage:**
- Extend step-2 query with `ad.r` → Steps 2-3. ✓
- Fallback only when adsbdb value absent/empty → Step 4 condition. ✓
- adsbdb keeps priority → Step 4 checks `resp["registration"]` first. ✓
- Trim whitespace, empty = no registration → Step 4 `strings.TrimSpace` + `!= ""`. ✓
- No migration / no frontend / no external call → Global Constraints + single file. ✓
- Verification build + vet (+ curl on deploy per spec) → Step 5. ✓

**Placeholder scan:** No TBD/TODO; all code shown in full. ✓

**Type consistency:** `readsbReg` declared `*string` (Step 2), selected/scanned as `ad.r` (Step 3), dereferenced after nil-check (Step 4). `resp["registration"]` asserted to `*string` matching how step 1 sets it. ✓
