<script>
// @ts-nocheck
    import { selectedHex, closeAircraftModal } from '../stores/aircraftModal';
    import { settings } from '../stores/settings';

    let data = null;
    let loading = false;
    let error = null;
    let planespotters = null; // client-side photo (preferred over adsbdb)
    let adsbdbImgFailed = false; // adsbdb photo URL failed to load (frequent 404s)
    let requestSeq = 0;

    $: disableTags = $settings['disable_planealertdb_tags']?.setting_value === 'true';

    const RECORD_LABELS = {
        fastest: { label: 'Fastest', unit: 'kt' },
        slowest: { label: 'Slowest', unit: 'kt' },
        highest: { label: 'Highest', unit: 'ft' },
        lowest: { label: 'Lowest', unit: 'ft' },
        furthest_flown: { label: 'Furthest flown', unit: 'km' },
        longest_route: { label: 'Longest route', unit: 'km' },
        most_remaining: { label: 'Most remaining', unit: 'km' },
    };

    async function load(hex) {
        const seq = ++requestSeq;
        loading = true;
        error = null;
        data = null;
        planespotters = null;
        adsbdbImgFailed = false;
        const dialog = document.getElementById('aircraft-detail-modal');
        if (dialog && !dialog.open) {
            dialog.showModal();
        }
        try {
            const res = await fetch('/api/stats/aircraft/' + hex);
            if (!res.ok) throw new Error(`${res.status}`);
            const result = await res.json();
            if (seq !== requestSeq) return; // superseded by a newer open
            data = result;
            const hasInterestingImages = data.interesting?.images?.length > 0;
            // Prefer planespotters over the frequently-dead adsbdb (airport-data.com) photo.
            if (!hasInterestingImages) {
                const ps = await fetchPlanespotters(hex);
                if (seq !== requestSeq) return;
                planespotters = ps;
            }
        } catch (err) {
            if (seq !== requestSeq) return;
            error = err.message;
        } finally {
            if (seq === requestSeq) loading = false;
        }
    }

    async function fetchPlanespotters(hex) {
        try {
            const res = await fetch(`https://api.planespotters.net/pub/photos/hex/${hex}`);
            if (!res.ok) return null;
            const result = await res.json();
            const photo = result.photos?.[0];
            if (!photo) return null;
            return {
                url: photo.thumbnail_large?.src,
                photographer: photo.photographer,
                link: photo.link
            };
        } catch {
            return null;
        }
    }

    function onClose() {
        closeAircraftModal();
        data = null;
        error = null;
        planespotters = null;
        adsbdbImgFailed = false;
        loading = false;
    }

    // Open + fetch whenever a hex is selected.
    $: if ($selectedHex) {
        load($selectedHex);
    }
</script>

<dialog id="aircraft-detail-modal" class="modal" on:close={onClose}>
    <div class="modal-box max-w-3xl">
        {#if loading}
            <div class="flex justify-center py-8">
                <span class="loading loading-ring loading-lg"></span>
            </div>
        {:else if error}
            <div class="flex alert alert-error">
                <span>Something went wrong: {error}</span>
            </div>
        {:else if data}
            <div class="flex items-center justify-between mb-2">
                <h3 class="text-lg font-bold">
                    {data.registration || data.hex}{#if data.type} - {data.type}{/if}
                </h3>
                {#if !disableTags && data.interesting?.tags?.length}
                    <div class="flex gap-2">
                        {#each data.interesting.tags as tag}
                            <div class="badge badge-accent text-white">{tag}</div>
                        {/each}
                    </div>
                {/if}
            </div>
            {#if data.operator}
                <p class="text-sm text-gray-600 mb-1">{data.operator}</p>
            {/if}
            {#if data.manufacturer || data.type}
                <p class="text-sm text-gray-500 mb-3">
                    {[data.manufacturer, data.type].filter(Boolean).join(' · ')}{#if data.icao_type} ({data.icao_type}){/if}
                </p>
            {/if}
            {#if data.records?.length}
                <div class="flex flex-wrap gap-2 mb-4">
                    {#each data.records as rec}
                        <div class="badge badge-primary badge-outline gap-1">
                            {RECORD_LABELS[rec.category]?.label ?? rec.category} — {Math.round(rec.value)} {RECORD_LABELS[rec.category]?.unit ?? ''}
                        </div>
                    {/each}
                </div>
            {/if}

            {#if data.live}
                <div class="grid grid-cols-2 sm:grid-cols-3 gap-2 mb-4">
                    <div><span class="text-xs uppercase text-gray-500">Altitude</span><div>{data.live.altitude ?? '-'} ft</div></div>
                    <div><span class="text-xs uppercase text-gray-500">Speed</span><div>{data.live.ground_speed ?? '-'} kt</div></div>
                    <div><span class="text-xs uppercase text-gray-500">Track</span><div>{data.live.track ?? '-'}&deg;</div></div>
                    <div><span class="text-xs uppercase text-gray-500">Distance</span><div>{data.live.distance_km ?? '-'} km</div></div>
                    <div><span class="text-xs uppercase text-gray-500">Bearing</span><div>{data.live.bearing ?? '-'}&deg;</div></div>
                    <div><span class="text-xs uppercase text-gray-500">Position</span><div>{data.live.lat ?? '-'}, {data.live.lon ?? '-'}</div></div>
                </div>
            {:else}
                <div class="alert alert-info mb-4">
                    <span>Not currently visible to the receiver</span>
                </div>
            {/if}

            <div class="flex gap-6 mb-4 text-sm">
                <div><span class="text-xs uppercase text-gray-500">Times seen</span><div>{data.history?.times_seen ?? 0}</div></div>
                <div><span class="text-xs uppercase text-gray-500">Last seen</span><div>{data.history?.last_seen ? new Date(data.history.last_seen).toLocaleString() : '-'}</div></div>
            </div>

            {#if data.observations?.length}
                <div class="mb-4">
                    <div class="text-xs uppercase text-gray-500 mb-1">Recent observations</div>
                    <div class="overflow-x-auto">
                        <table class="table table-xs">
                            <thead><tr><th>Time</th><th>Route</th><th>Speed</th><th>Alt</th></tr></thead>
                            <tbody>
                                {#each data.observations as obs}
                                    <tr>
                                        <td class="whitespace-nowrap">{obs.first_seen ? new Date(obs.first_seen).toLocaleString() : '-'}</td>
                                        <td class="whitespace-nowrap">{obs.origin || '—'} → {obs.destination || '—'}</td>
                                        <td>{obs.ground_speed != null ? Math.round(obs.ground_speed) + ' kt' : '-'}</td>
                                        <td>{obs.altitude != null ? obs.altitude + ' ft' : '-'}</td>
                                    </tr>
                                {/each}
                            </tbody>
                        </table>
                    </div>
                    {#if (data.history?.times_seen ?? 0) > data.observations.length}
                        <div class="text-xs text-gray-500 mt-1">…and {data.history.times_seen - data.observations.length} more</div>
                    {/if}
                </div>
            {/if}

            {#if data.interesting?.images?.length}
                <div class="grid grid-cols-1 sm:grid-cols-3 gap-4">
                    {#each data.interesting.images as img}
                        <img src={img} alt="{data.registration} photo" class="w-full h-auto rounded-lg" />
                    {/each}
                </div>
            {:else if planespotters}
                <div>
                    <img src={planespotters.url} alt="{data.registration} photo" class="max-w-sm h-auto rounded-lg mx-auto" />
                    {#if planespotters.photographer}
                        <p class="text-xs text-gray-500 mt-1">
                            &copy; {planespotters.photographer}
                            {#if planespotters.link} &middot; <a class="link" href={planespotters.link} target="_blank" rel="noopener noreferrer">planespotters.net</a>{/if}
                        </p>
                    {/if}
                </div>
            {:else if data.photo && !adsbdbImgFailed}
                <img src={data.photo.url || data.photo.thumbnail} alt="{data.registration} photo" class="max-w-sm h-auto rounded-lg mx-auto" on:error={() => adsbdbImgFailed = true} />
            {:else}
                <p class="text-center text-gray-500 py-8">No photo available</p>
            {/if}
        {/if}
        <div class="modal-action">
            <form method="dialog">
                <button class="btn">Close</button>
            </form>
        </div>
    </div>
    <form method="dialog" class="modal-backdrop">
        <button>close</button>
    </form>
</dialog>
