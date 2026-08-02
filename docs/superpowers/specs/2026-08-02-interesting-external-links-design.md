# Design: External info links in the aircraft detail modal

**Date:** 2026-08-02
**Status:** Approved (pending spec review)
**Scope:** Frontend only — `web/src/components/AircraftModal.svelte`

## Problem

The "interesting aircraft" lists (Civ/Gov/Pol/Mil) let a user click a row to open
the aircraft detail modal, but neither the list nor the modal offers a link out to
deeper third-party history/photo sources. A user who spots something interesting
(e.g. OO-ACF that flew OSL→TLV) has no one-click way to jump to that aircraft's
page on Flightradar24, Planespotters, etc.

## Goal

Add a compact row of external links to the top of the aircraft detail modal so the
user can open the aircraft's page on Flightradar24, Planespotters, adsbdb, and
JetPhotos in a new tab.

## Non-goals

- No deep-link to the specific flight that passed the receiver. Flightradar24's
  per-flight URL fragment (e.g. `#40f555b8`) is FR24's internal flight ID; it is
  not derivable from ADS-B data and is not stored. The general aircraft URL is
  itself the flight-history page, so the relevant passage appears at the top of
  that list anyway.
- No backend, API, or database changes. The modal already has `data.registration`
  and `data.hex`; all URLs are built client-side.
- No changes to the list rows themselves (links live only in the modal).

## Design

### Placement

A single row placed near the **top** of the modal, directly under the
operator / manufacturer-type line (before the records/live-position blocks), so
it is visible without scrolling. Label: `Mer info:` (or `More info:` to match the
otherwise-English modal copy — see Open questions).

### Markup / styling

- One `<a>` per service, styled as DaisyUI `btn btn-xs btn-outline`.
- All links open in a new tab: `target="_blank" rel="noopener noreferrer"`.
- Wrapped in a flex row that wraps on narrow screens (`flex flex-wrap gap-2`).

### Links and URL construction

| Service       | URL template                                              | Key          | Notes |
|---------------|-----------------------------------------------------------|--------------|-------|
| Flightradar24 | `https://www.flightradar24.com/data/aircraft/{reg}`       | registration | `reg` lower-cased, hyphen kept (e.g. `oo-acf`). Landing page IS the flight-history list. |
| Planespotters | `https://www.planespotters.net/hex/{hex}`                 | hex          | Always available. |
| adsbdb        | `https://api.adsbdb.com/v0/aircraft/{hex}`                | hex          | JSON API — same source/version already used for enrichment (`registrations.go`). For verification/debugging rather than pretty history. |
| JetPhotos     | `https://www.jetphotos.com/registration/{reg}`            | registration | `reg` as-is (upper-case with hyphen, e.g. `OO-ACF`). |

### Conditional rendering

- **Flightradar24** and **JetPhotos** require a registration; render only when
  `data.registration` is truthy. (Many military/interesting aircraft have no
  registration — better to hide the button than link to a dead page.)
- **Planespotters** and **adsbdb** are keyed on `data.hex`, which is always
  present (the modal is opened by hex), so they always render.
- The whole row renders only when `data` is loaded (inside the existing
  `{:else if data}` block).

### Helper

Introduce small pure helpers in the component's `<script>` for URL building, e.g.:

```js
const fr24Url        = (reg) => `https://www.flightradar24.com/data/aircraft/${reg.toLowerCase()}`;
const planespotterUrl = (hex) => `https://www.planespotters.net/hex/${hex}`;
const adsbdbUrl      = (hex) => `https://api.adsbdb.com/v0/aircraft/${hex}`;
const jetphotosUrl   = (reg) => `https://www.jetphotos.com/registration/${reg}`;
```

(Exact factoring is an implementation detail; inline expressions in the template
are acceptable if cleaner.)

## Testing / verification

There are no automated tests in this repo. Verify manually:

1. `cd web && npm run build` succeeds (no Svelte/TS errors).
2. In the running app, open an interesting aircraft **with** a registration:
   all four buttons appear; each opens the correct third-party page in a new tab.
   Spot-check FR24 lands on the flight-history list and JetPhotos on the reg page.
3. Open an interesting aircraft **without** a registration (e.g. a military one):
   only Planespotters and adsbdb appear, and both resolve.

## Open questions

- **Label language:** the modal copy is otherwise English ("Altitude", "Times
  seen", "Recent observations"), so the label should probably be `More info:` for
  consistency, even though the conversation is in Swedish. Default to `More info:`
  unless the user prefers Swedish.
- **JetPhotos path:** `/registration/<reg>` is the expected canonical reg page but
  should be spot-checked against a live example during implementation.
