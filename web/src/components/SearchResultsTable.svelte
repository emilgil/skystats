<script>
    export let results = [];
    export let loading = false;
    export let error = null;
    export let sort = 'first_seen';
    export let dir = 'desc';
    export let onSort = () => {};
    export let onRowClick = () => {};

    const columns = [
        { key: 'first_seen', label: 'Sedd' },
        { key: null, label: 'Flight' },
        { key: null, label: 'Reg' },
        { key: null, label: 'Typ' },
        { key: null, label: 'Flygbolag' },
        { key: 'barometric_altitude', label: 'Höjd' },
        { key: 'ground_speed', label: 'Hastighet' },
        { key: 'distance_flown', label: 'Distans' },
        { key: null, label: 'Rutt' }
    ];

    function formatValue(v, suffix = '') {
        if (v === null || v === undefined || v === '') return '-';
        return suffix ? `${v} ${suffix}` : v;
    }

    function formatDate(v) {
        if (!v) return '-';
        return new Date(v).toLocaleString();
    }
</script>

<div class="overflow-x-auto mt-4">
    {#if loading}
        <div class="flex justify-center py-8">
            <span class="loading loading-ring loading-lg"></span>
        </div>
    {:else if error}
        <div class="alert alert-error">
            <span>Något gick fel: {error}</span>
        </div>
    {:else if results.length === 0}
        <div class="alert alert-info">
            <span>Inga träffar</span>
        </div>
    {:else}
        <table class="table">
            <thead>
                <tr>
                    {#each columns as col}
                        <th>
                            {#if col.key}
                                <button type="button" class="flex items-center gap-1" on:click={() => onSort(col.key)}>
                                    {col.label}
                                    {#if sort === col.key}
                                        <span>{dir === 'asc' ? '▲' : '▼'}</span>
                                    {/if}
                                </button>
                            {:else}
                                {col.label}
                            {/if}
                        </th>
                    {/each}
                </tr>
            </thead>
            <tbody>
                {#each results as row (row.hex + row.first_seen)}
                    <tr class="cursor-pointer hover:bg-base-300" on:click={() => onRowClick(row)}>
                        <td>{formatDate(row.first_seen)}</td>
                        <td>{formatValue(row.flight)}</td>
                        <td>{formatValue(row.registration)}</td>
                        <td>{formatValue(row.type)}</td>
                        <td>{formatValue(row.airline_name)}</td>
                        <td>{formatValue(row.barometric_altitude, 'ft')}</td>
                        <td>{formatValue(row.ground_speed, 'kt')}</td>
                        <td>{formatValue(row.distance_flown, 'km')}</td>
                        <td>{formatValue(row.origin_iata_code, '')} &rarr; {formatValue(row.destination_iata_code, '')}</td>
                    </tr>
                {/each}
            </tbody>
        </table>
    {/if}
</div>
