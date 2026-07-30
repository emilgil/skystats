<script>
    import { onMount } from 'svelte'
    import { refreshInterestingData } from '../stores/settings';
    import { openAircraftModal } from '../stores/aircraftModal';

    export let endpoint;
    export let title;
    export let icon;
    // Still passed by the parent Interesting{Mil,Gov,Pol,Civ}Aircraft components;
    // no longer used internally now that the local dialog is gone. `export const`
    // (rather than `export let`) avoids an "unused export property" build warning
    // without changing the prop contract for those parents.
    export const aircraftType = undefined;

    let data = [];
    let loading = true;
    let error = null;

    async function fetchData() {
        
        try {
            const response = await fetch(endpoint);
            if(!response.ok) {
                throw new Error(`{response.status}`);
            }
            const result = await response.json();
            data = result;
            error = null
        } catch (err) {
            error = err.message;
        } finally {
            loading = false;
        }
    }

    onMount(() => {
        fetchData();
    })

    // Refresh when settings change
    $: if ($refreshInterestingData) {
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
                        <thead class="uppercase tracking-wider">
                            <tr>
                                <th>Reg</th>
                                <th>Operator</th>
                                <th>Type</th>
                                <th>Last Seen</th>
                            </tr>
                        </thead>
                        <tbody>
                            {#each data as aircraft}
                            <tr class="hover:bg-base-300 cursor-pointer" on:click={() => openAircraftModal(aircraft.hex)}>
                                <td class="font-mono whitespace-nowrap">{aircraft.registration}</td>
                                <td>{aircraft.operator}</td>
                                <td>{aircraft.type}</td>
                                <td class="whitespace-nowrap">{aircraft.seen ? new Date(aircraft.seen).toLocaleString() : '-'}</td>
                            </tr>
                            {/each}
                        </tbody>
                    </table>
                {/if}
            </div>
        </div>
    </div>
</div>
