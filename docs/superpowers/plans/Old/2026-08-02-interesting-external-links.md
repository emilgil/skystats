# External Info Links in Aircraft Modal — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a row of external links (Flightradar24, Planespotters, adsbdb, JetPhotos) to the top of the aircraft detail modal so a user can jump to third-party history/photo pages for an interesting aircraft.

**Architecture:** Pure frontend change in a single Svelte component. The modal already loads aircraft detail keyed on `hex` and exposes `data.registration` and `data.hex`. All four URLs are built client-side from those two fields; no backend, API, or database changes.

**Tech Stack:** Svelte 5, Tailwind CSS 4, DaisyUI, Vite.

## Global Constraints

- Change is confined to `web/src/components/AircraftModal.svelte`. No Go, API, DB, or other frontend files.
- Links open in a new tab: every anchor uses `target="_blank" rel="noopener noreferrer"`.
- Link label is `More info:` (English, to match the modal's other copy).
- Flightradar24 and JetPhotos require a registration → render only when `data.registration` is truthy. Planespotters and adsbdb are keyed on `data.hex` (always present).
- Button styling matches DaisyUI usage elsewhere: `btn btn-xs btn-outline`.
- Follow the existing component's syntax style (legacy Svelte `{#if}` / `<a>` markup, `const` helpers in the `<script>` block). Do not rewrite unrelated code.
- adsbdb URL must match the version already used in the backend: `https://api.adsbdb.com/v0/aircraft/<hex>` (`core/registrations.go:118`).

---

### Task 1: Add external info links row to AircraftModal

**Files:**
- Modify: `web/src/components/AircraftModal.svelte` (add helpers after line 23; add markup block between lines 119 and 120)

**Interfaces:**
- Consumes: `data.registration` (string, may be empty) and `data.hex` (string, always present) from the already-loaded modal `data` object.
- Produces: nothing consumed by other tasks — this is the only task.

- [ ] **Step 1: Add URL-builder helpers to the `<script>` block**

Insert immediately after the `RECORD_LABELS` object (currently ends at line 23), before `async function load(hex)`:

```js
    const fr24Url          = (reg) => `https://www.flightradar24.com/data/aircraft/${reg.toLowerCase()}`;
    const planespottersUrl = (hex) => `https://www.planespotters.net/hex/${hex}`;
    const adsbdbUrl        = (hex) => `https://api.adsbdb.com/v0/aircraft/${hex}`;
    const jetphotosUrl     = (reg) => `https://www.jetphotos.com/registration/${reg}`;
```

Note: the name `planespottersUrl` deliberately differs from the existing `planespotters` photo variable to avoid a name collision.

- [ ] **Step 2: Add the external-links markup block**

Insert between the manufacturer/type block (line 119, `{/if}`) and the records block (line 120, `{#if data.records?.length}`):

```svelte
            {#if data.registration || data.hex}
                <div class="flex flex-wrap items-center gap-2 mb-3">
                    <span class="text-xs uppercase text-gray-500">More info:</span>
                    {#if data.registration}
                        <a class="btn btn-xs btn-outline" href={fr24Url(data.registration)} target="_blank" rel="noopener noreferrer">Flightradar24</a>
                    {/if}
                    {#if data.hex}
                        <a class="btn btn-xs btn-outline" href={planespottersUrl(data.hex)} target="_blank" rel="noopener noreferrer">Planespotters</a>
                    {/if}
                    {#if data.hex}
                        <a class="btn btn-xs btn-outline" href={adsbdbUrl(data.hex)} target="_blank" rel="noopener noreferrer">adsbdb</a>
                    {/if}
                    {#if data.registration}
                        <a class="btn btn-xs btn-outline" href={jetphotosUrl(data.registration)} target="_blank" rel="noopener noreferrer">JetPhotos</a>
                    {/if}
                </div>
            {/if}
```

- [ ] **Step 3: Build the frontend to verify it compiles**

Run: `cd web && npm run build`
Expected: build completes with no Svelte/Vite errors (a `dist/` bundle is produced).

- [ ] **Step 4: Manual verification in the running app**

Start the dev server (`cd web && npm run dev -- --host`) against a backend, then:
1. Open an interesting aircraft **with** a registration (e.g. a civilian one). Expected: four buttons — Flightradar24, Planespotters, adsbdb, JetPhotos — appear under the type line. Click each; each opens the correct third-party page in a new tab. FR24 lands on the flight-history list; JetPhotos on the registration page.
2. Open an interesting aircraft **without** a registration (e.g. a military one whose `registration` is empty). Expected: only Planespotters and adsbdb appear, and both resolve.

- [ ] **Step 5: Commit**

```bash
git add web/src/components/AircraftModal.svelte
git commit -m "feat: add external info links (FR24/Planespotters/adsbdb/JetPhotos) to aircraft modal

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Self-Review

**Spec coverage:**
- Placement top-of-modal under manufacturer/type line → Step 2 insertion point. ✓
- Four services with exact URL templates → Step 1 helpers + Step 2 markup. ✓
- Conditional rendering (FR24/JetPhotos need reg; Planespotters/adsbdb always) → Step 2 `{#if}` guards. ✓
- New-tab behavior → `target="_blank" rel="noopener noreferrer"` on every anchor. ✓
- Label "More info:" (English default) → Step 2. ✓
- Frontend-only, no backend → Global Constraints + single modified file. ✓
- Verification (build + manual with/without reg) → Steps 3–4, matching the spec's testing section. ✓

**Placeholder scan:** No TBD/TODO/"handle edge cases"; all code shown in full. ✓

**Type consistency:** Helper names (`fr24Url`, `planespottersUrl`, `adsbdbUrl`, `jetphotosUrl`) are defined in Step 1 and used identically in Step 2. `planespottersUrl` does not collide with the existing `planespotters` photo variable. ✓
