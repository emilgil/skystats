<script>
// @ts-nocheck
    import { selectedHex, closeAircraftModal } from '../stores/aircraftModal';
    import { settings } from '../stores/settings';

    let data = null;
    let loading = false;
    let error = null;
    let planespotters = null; // client-side fallback photo
    let requestSeq = 0;

    $: disableTags = $settings['disable_planealertdb_tags']?.setting_value === 'true';

    async function load(hex) {
        const seq = ++requestSeq;
        loading = true;
        error = null;
        data = null;
        planespotters = null;
        const dialog = document.getElementById('aircraft-modal');
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
            if (!hasInterestingImages && !data.photo) {
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
        loading = false;
    }

    // Open + fetch whenever a hex is selected.
    $: if ($selectedHex) {
        load($selectedHex);
    }
</script>

<dialog id="aircraft-modal" class="modal" on:close={onClose}>
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
                <p class="text-sm text-gray-600 mb-4">{data.operator}</p>
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

            {#if data.interesting?.images?.length}
                <div class="grid grid-cols-1 sm:grid-cols-3 gap-4">
                    {#each data.interesting.images as img}
                        <img src={img} alt="{data.registration} photo" class="w-full h-auto rounded-lg" />
                    {/each}
                </div>
            {:else if data.photo}
                <img src={data.photo.url || data.photo.thumbnail} alt="{data.registration} photo" class="w-full h-auto rounded-lg" />
            {:else if planespotters}
                <div>
                    <img src={planespotters.url} alt="{data.registration} photo" class="w-full h-auto rounded-lg" />
                    {#if planespotters.photographer}
                        <p class="text-xs text-gray-500 mt-1">
                            &copy; {planespotters.photographer}
                            {#if planespotters.link} &middot; <a class="link" href={planespotters.link} target="_blank" rel="noopener noreferrer">planespotters.net</a>{/if}
                        </p>
                    {/if}
                </div>
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
