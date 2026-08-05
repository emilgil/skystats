<script>
    import { createEventDispatcher } from 'svelte';

    const dispatch = createEventDispatcher();

    const PERIODS = [
        { value: '24h', label: '24h' },
        { value: '7d', label: '7d' },
        { value: '30d', label: '30d' },
        { value: '90d', label: '90d' },
        { value: '365d', label: '365d' },
        { value: 'all_time', label: 'All-time' }
    ];

    let period = 'all_time';
    let useCustomRange = false;
    let from = '';
    let to = '';
    let manufacturer = '';
    let model = '';
    let country = '';
    let origin = '';
    let destination = '';
    let altitudeOp = 'gte';
    let altitudeValue = '';
    let speedOp = 'gte';
    let speedValue = '';
    let airline = '';
    let interesting = '';
    let originStatus = 'any';
    let destinationStatus = 'any';
    let q = '';

    function selectPreset(value) {
        useCustomRange = false;
        period = value;
    }

    function submit() {
        dispatch('search', {
            filters: {
                period, useCustomRange, from, to,
                manufacturer, model, country, origin, destination,
                altitudeOp, altitudeValue, speedOp, speedValue,
                airline, interesting, originStatus, destinationStatus, q
            }
        });
    }
</script>

<div class="card bg-base-100 shadow-sm rounded p-6">
    <div class="grid grid-cols-1 md:grid-cols-3 gap-4">
        <label class="form-control">
            <div class="label"><span class="label-text">Fritext (flight/reg/hex)</span></div>
            <input type="text" class="input input-bordered" bind:value={q} placeholder="SAS123, SE-ABC, 4d201f" />
        </label>

        <label class="form-control">
            <div class="label"><span class="label-text">Tillverkare</span></div>
            <input type="text" class="input input-bordered" bind:value={manufacturer} placeholder="Boeing" />
            <div class="label"><span class="label-text-alt text-base-content/60">Endast tillgängligt för flygplan med känd registreringsdata (~33 % av flottan).</span></div>
        </label>

        <label class="form-control">
            <div class="label"><span class="label-text">Modell</span></div>
            <input type="text" class="input input-bordered" bind:value={model} placeholder="A320" />
            <div class="label"><span class="label-text-alt text-base-content/60">Endast tillgängligt för flygplan med känd registreringsdata.</span></div>
        </label>

        <label class="form-control">
            <div class="label"><span class="label-text">Registreringsland (ISO-kod)</span></div>
            <input type="text" class="input input-bordered" bind:value={country} placeholder="SE" maxlength="2" />
            <div class="label"><span class="label-text-alt text-base-content/60">Endast tillgängligt för flygplan med känd registreringsdata.</span></div>
        </label>

        <label class="form-control">
            <div class="label"><span class="label-text">Avgångsflygplats (IATA)</span></div>
            <input type="text" class="input input-bordered" bind:value={origin} placeholder="ARN" maxlength="3" />
        </label>

        <label class="form-control">
            <div class="label"><span class="label-text">Ankomstflygplats (IATA)</span></div>
            <input type="text" class="input input-bordered" bind:value={destination} placeholder="LHR" maxlength="3" />
        </label>

        <label class="form-control">
            <div class="label"><span class="label-text">Flygbolag</span></div>
            <input type="text" class="input input-bordered" bind:value={airline} placeholder="Ryanair" />
        </label>

        <label class="form-control">
            <div class="label"><span class="label-text">Höjd (barometrisk, ft)</span></div>
            <div class="join">
                <select class="select select-bordered join-item" bind:value={altitudeOp}>
                    <option value="gte">Över</option>
                    <option value="lte">Under</option>
                </select>
                <input type="number" class="input input-bordered join-item w-full" bind:value={altitudeValue} placeholder="35000" />
            </div>
        </label>

        <label class="form-control">
            <div class="label"><span class="label-text">Hastighet (ground speed, kt)</span></div>
            <div class="join">
                <select class="select select-bordered join-item" bind:value={speedOp}>
                    <option value="gte">Över</option>
                    <option value="lte">Under</option>
                </select>
                <input type="number" class="input input-bordered join-item w-full" bind:value={speedValue} placeholder="450" />
            </div>
        </label>

        <label class="form-control">
            <div class="label"><span class="label-text">Interesting-kategori</span></div>
            <select class="select select-bordered" bind:value={interesting}>
                <option value="">Alla</option>
                <option value="military">Militär</option>
                <option value="government">Regering</option>
                <option value="police">Polis</option>
                <option value="civilian">Civil</option>
            </select>
        </label>

        <label class="form-control">
            <div class="label"><span class="label-text">Avgångsflygplats status</span></div>
            <select class="select select-bordered" bind:value={originStatus}>
                <option value="any">Alla</option>
                <option value="known">Endast med känd</option>
                <option value="unknown">Endast utan känd</option>
            </select>
        </label>

        <label class="form-control">
            <div class="label"><span class="label-text">Ankomstflygplats status</span></div>
            <select class="select select-bordered" bind:value={destinationStatus}>
                <option value="any">Alla</option>
                <option value="known">Endast med känd</option>
                <option value="unknown">Endast utan känd</option>
            </select>
        </label>
    </div>

    <div class="mt-4">
        <div class="label"><span class="label-text">Tidsperiod</span></div>
        <div class="join">
            {#each PERIODS as p}
                <button type="button" class="join-item btn btn-sm {!useCustomRange && period === p.value ? 'btn-active btn-primary' : ''}"
                    on:click={() => selectPreset(p.value)}>
                    {p.label}
                </button>
            {/each}
            <button type="button" class="join-item btn btn-sm {useCustomRange ? 'btn-active btn-primary' : ''}"
                on:click={() => { useCustomRange = true; }}>
                Valfritt intervall
            </button>
        </div>
        {#if useCustomRange}
            <div class="flex gap-2 mt-2 items-center">
                <input type="date" class="input input-bordered" bind:value={from} />
                <span>till</span>
                <input type="date" class="input input-bordered" bind:value={to} />
            </div>
        {/if}
    </div>

    <div class="mt-4 flex justify-center">
        <button type="button" class="btn btn-primary" on:click={submit}>Sök</button>
    </div>
</div>
