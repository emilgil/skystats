<script>
    import { onMount } from 'svelte';
    import { IconAlertTriangle } from '@tabler/icons-svelte';
    import { loadWatchHits } from '../stores/watches';
    import { openAircraftModal } from '../stores/aircraftModal';

    let hits = [];
    let loading = true;
    let error = null;

    onMount(async () => {
        const result = await loadWatchHits();
        hits = result.hits;
        error = result.error;
        loading = false;
    });

    function formatTime(value) {
        return new Date(value).toLocaleString();
    }

    function identity(hit) {
        return hit.registration || hit.flight || hit.hex;
    }

    function detail(hit) {
        const s = hit.snapshot ?? {};
        const parts = [];
        if (s.type_code) parts.push(s.type_code);
        if (s.origin?.length && s.destination?.length) {
            parts.push(`${s.origin[s.origin.length - 1]} → ${s.destination[s.destination.length - 1]}`);
        }
        if (s.altitude_ft) parts.push(`${Math.round(s.altitude_ft)} ft`);
        if (s.distance_km) parts.push(`${Math.round(s.distance_km)} km`);
        return parts.join(' · ');
    }
</script>

{#if loading}
    <div class="flex justify-center py-8"><span class="loading loading-spinner loading-lg"></span></div>
{:else if error}
    <div class="alert alert-error"><span>{error}</span></div>
{:else if hits.length === 0}
    <p class="text-center opacity-60 py-8">No watch hits recorded yet.</p>
{:else}
    <div class="overflow-x-auto">
        <table class="table table-zebra">
            <thead>
                <tr>
                    <th>Time</th>
                    <th>Watch</th>
                    <th>Aircraft</th>
                    <th>Details</th>
                    <th>Notified</th>
                </tr>
            </thead>
            <tbody>
                {#each hits as hit (hit.id)}
                    <tr class="cursor-pointer hover" on:click={() => openAircraftModal(hit.hex)}>
                        <td class="whitespace-nowrap">{formatTime(hit.notified_at)}</td>
                        <td>{hit.watch_name}</td>
                        <td class="font-medium">{identity(hit)}</td>
                        <td class="opacity-70">{detail(hit)}</td>
                        <td>
                            {#if hit.apprise_success}
                                <span class="badge badge-success badge-sm">sent</span>
                            {:else}
                                <span class="badge badge-ghost badge-sm gap-1" title={hit.apprise_error ?? ''}>
                                    <IconAlertTriangle class="h-3 w-3" /> not sent
                                </span>
                            {/if}
                        </td>
                    </tr>
                {/each}
            </tbody>
        </table>
    </div>
{/if}
