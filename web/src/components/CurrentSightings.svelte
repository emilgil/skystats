<script>
    import { onMount, onDestroy } from 'svelte';
    import { IconPlane } from '@tabler/icons-svelte';

    const endpoint = 'api/stats/current';
    const refreshRate = 2000;
    const staleAfterMs = 30000;

    let data = [];
    let generatedAt = null;
    let loading = true;
    let error = null;
    let interval = null;
    let observer = null;
    let onScreen = false;
    let cardElement;
    let now = Date.now();

    async function fetchData() {
        try {
            const response = await fetch(endpoint);
            if (!response.ok) {
                throw new Error(`${response.status}`);
            }
            const result = await response.json();
            data = result.aircraft || [];
            generatedAt = result.generated_at;
            error = null;
        } catch (err) {
            // Keep the last good table on screen — one dropped poll out of
            // thirty per minute should not blank the view.
            error = err.message;
        } finally {
            // Never set loading back to true here: the skeleton would flash
            // over the table every two seconds.
            loading = false;
            now = Date.now();
        }
    }

    function startPolling() {
        if (interval) return;
        fetchData();
        interval = setInterval(fetchData, refreshRate);
    }

    function stopPolling() {
        if (!interval) return;
        clearInterval(interval);
        interval = null;
    }

    function syncPolling() {
        if (onScreen && !document.hidden) {
            startPolling();
        } else {
            stopPolling();
        }
    }

    onMount(() => {
        observer = new IntersectionObserver((entries) => {
            onScreen = entries[0].isIntersecting;
            syncPolling();
        });
        observer.observe(cardElement);
        document.addEventListener('visibilitychange', syncPolling);
    });

    onDestroy(() => {
        stopPolling();
        if (observer) {
            observer.disconnect();
        }
        document.removeEventListener('visibilitychange', syncPolling);
    });

    $: stale = generatedAt !== null && now - new Date(generatedAt).getTime() > staleAfterMs;

    function formatAltitude(feet) {
        return feet ? `${feet.toLocaleString('en-US')} ft` : '—';
    }

    function formatSpeed(knots) {
        return knots ? `${Math.round(knots)} kt` : '—';
    }

    function formatRoute(aircraft) {
        if (!aircraft.origin_iata && !aircraft.destination_iata) {
            return '—';
        }
        return `${aircraft.origin_iata || '???'} → ${aircraft.destination_iata || '???'}`;
    }

    function formatOperator(aircraft) {
        return aircraft.airline || aircraft.operator || '—';
    }
</script>

<div bind:this={cardElement} class="card bg-base-100 mb-4 shadow-sm rounded hover:shadow-md transition-all duration-200">
    <div class="card-body">
        <div class="flex items-center gap-2 mb-5">
            <div class="w-8 h-8 rounded-lg flex items-center justify-center">
                <IconPlane class="w-6 h-6 text-primary" />
            </div>
            <h2 class="text-2xl font-extralight tracking-wider">Current Sightings</h2>
        </div>

        {#if loading}
            <div class="flex justify-center py-8">
                <span class="loading loading-ring loading-lg"></span>
            </div>
        {:else}
            {#if error}
                <div class="alert alert-warning mb-4 py-2 text-sm">
                    <span>Live update failed ({error}) — showing last known data</span>
                </div>
            {:else if stale}
                <div class="alert alert-warning mb-4 py-2 text-sm">
                    <span>Feed has not updated since {new Date(generatedAt).toLocaleTimeString()}</span>
                </div>
            {/if}

            {#if data.length === 0}
                <div class="alert alert-info">
                    <span>No aircraft currently visible</span>
                </div>
            {:else}
                <div class="overflow-x-auto max-h-[70vh] overflow-y-auto">
                    <table class="table table-sm table-pin-rows">
                        <thead class="uppercase tracking-wider">
                            <tr>
                                <th>Flight</th>
                                <th>Reg</th>
                                <th>Type</th>
                                <th>Airline</th>
                                <th>Altitude</th>
                                <th>Speed</th>
                                <th>Distance</th>
                                <th>Route</th>
                                <th></th>
                                <th>Last Seen</th>
                            </tr>
                        </thead>
                        <tbody>
                            {#each data as aircraft (aircraft.hex)}
                                <tr>
                                    <td class="font-mono whitespace-nowrap">{aircraft.flight || '—'}</td>
                                    <td class="font-mono whitespace-nowrap">{aircraft.registration || '—'}</td>
                                    <td class="whitespace-nowrap" title={aircraft.type_description || ''}>
                                        {aircraft.type || '—'}
                                    </td>
                                    <td>{formatOperator(aircraft)}</td>
                                    <td class="whitespace-nowrap">{formatAltitude(aircraft.altitude)}</td>
                                    <td class="whitespace-nowrap">{formatSpeed(aircraft.ground_speed)}</td>
                                    <td class="whitespace-nowrap">{aircraft.distance_km.toFixed(1)} km</td>
                                    <td class="font-mono whitespace-nowrap">{formatRoute(aircraft)}</td>
                                    <td>
                                        {#if aircraft.interesting_group}
                                            <div class="badge badge-accent text-white">{aircraft.interesting_group}</div>
                                        {/if}
                                    </td>
                                    <td class="whitespace-nowrap">
                                        {aircraft.last_seen ? new Date(aircraft.last_seen).toLocaleTimeString() : '—'}
                                    </td>
                                </tr>
                            {/each}
                        </tbody>
                    </table>
                </div>
            {/if}
        {/if}
    </div>
</div>
