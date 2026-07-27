<script>
// @ts-nocheck
    import { refreshRecordHolderData } from '../stores/settings';
    import { recordPeriod, buildRecordUrl } from '../stores/recordPeriod';

    export let endpoint;
    export let title;
    export let columns = [];
    export let icon = null;

    let data = [];
    let loading = true;
    let error = null;

    let requestSeq = 0;

    async function fetchData() {
        const seq = ++requestSeq;
        loading = true;
        try {
            const response = await fetch(buildRecordUrl(endpoint, $recordPeriod));
            if (!response.ok) {
                throw new Error(`${response.status}`);
            }
            const result = await response.json();
            if (seq !== requestSeq) return; // superseded by a newer request
            data = result;
            error = null;
        } catch (err) {
            if (seq !== requestSeq) return;
            error = err.message;
        } finally {
            if (seq === requestSeq) {
                loading = false;
            }
        }
    }

    // Fetch on mount and whenever the selected period changes.
    $: if (endpoint) {
        $recordPeriod;
        fetchData();
    }

    // Refresh when settings change
    $: if ($refreshRecordHolderData) {
        fetchData();
    }
</script>

<div>
    <div class="card bg-base-100 mb-4 w96 shadow-sm rounded hover:shadow-md transition-all duration-200">
        <div class="card-body">
            <div class="overflow-x-auto">
                {#if loading}
                    <div class="flex justify-center py-8">
                        <span class="loading loading-ring loading-lg"></span>
                    </div>
                {:else if error}
                    <div class="flex alert alert-error">
                        <svg xmlns="http://www.w3.org/2000/svg" class="stroke-current shrink-0 h-6 w-6" fill="none" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M10 14l2-2m0 0l2-2m-2 2l-2-2m2 2l2 2m7-2a9 9 0 11-18 0 9 9 0 0118 0z" /></svg>
                        <span>Something went wrong: {error}</span>
                    </div>
                {:else if data.length === 0}
                    <div class="alert alert-info">
                        <svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" class="stroke-current shrink-0 w-6 h-6"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M13 16h-1v-4h-1m1-4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z"></path></svg>
                        <span>No data available</span>
                    </div>
                {:else}
                    <!-- table header-->
                    <div class="flex items-center gap-2 mb-5">
                    {#if icon}
                        <div class="w-8 h-8 rounded-lg flex items-center justify-center">
                            <svelte:component this={icon} class="w-6 h-6 text-primary" />
                        </div> 
                    {/if}
                    <h2 class="text-2xl font-extralight tracking-wider">{title}</h2>
                    </div>
                    <!-- table-->
                    <table class="table">
                        <thead>
                            <tr class="uppercase tracking-wider">
                                {#each columns as column}
                                    <th>{column.header}</th>
                                {/each}
                            </tr>
                        </thead>
                        <tbody>
                            {#each data as aircraft}
                            <tr>
                                {#each columns as column}
                                    <td class={column.class || ''}>
                                        {#if column.formatter}
                                            {@html column.formatter(aircraft[column.field])}
                                        {:else}
                                            {aircraft[column.field] || '-'}
                                        {/if}
                                    </td>
                                {/each}
                            </tr>
                            {/each}
                        </tbody>
                    </table>
                {/if}
            </div>
        </div>
    </div>
</div>