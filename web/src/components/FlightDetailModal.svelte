<script>
    import { selectedFlight, closeFlightModal } from '../stores/flightDetailModal';

    let dialogEl;

    $: if ($selectedFlight && dialogEl) {
        dialogEl.showModal();
    }

    function onClose() {
        closeFlightModal();
    }

    function formatValue(v, suffix = '') {
        if (v === null || v === undefined || v === '') return '-';
        return suffix ? `${v} ${suffix}` : v;
    }

    function formatDate(v) {
        if (!v) return '-';
        return new Date(v).toLocaleString();
    }
</script>

<dialog bind:this={dialogEl} class="modal" on:close={onClose}>
    <div class="modal-box max-w-2xl">
        {#if $selectedFlight}
            <h3 class="text-lg font-bold mb-4">
                {formatValue($selectedFlight.flight)} &mdash; {formatValue($selectedFlight.registration)}
            </h3>
            <div class="grid grid-cols-2 gap-x-4 gap-y-2 text-sm">
                <div><span class="font-semibold">Hex:</span> {$selectedFlight.hex}</div>
                <div><span class="font-semibold">Typ:</span> {formatValue($selectedFlight.type)}</div>
                <div><span class="font-semibold">Tillverkare:</span> {formatValue($selectedFlight.manufacturer)}</div>
                <div><span class="font-semibold">Modell:</span> {formatValue($selectedFlight.model)}</div>
                <div><span class="font-semibold">Registreringsland:</span> {formatValue($selectedFlight.registered_owner_country_name)}</div>
                <div><span class="font-semibold">Flygbolag:</span> {formatValue($selectedFlight.airline_name)}</div>
                <div><span class="font-semibold">Sedd första gång:</span> {formatDate($selectedFlight.first_seen)}</div>
                <div><span class="font-semibold">Sedd sista gången:</span> {formatDate($selectedFlight.last_seen)}</div>
                <div><span class="font-semibold">Avgång:</span> {formatValue($selectedFlight.origin_iata_code)} ({formatValue($selectedFlight.origin_icao_code)})</div>
                <div><span class="font-semibold">Ankomst:</span> {formatValue($selectedFlight.destination_iata_code)} ({formatValue($selectedFlight.destination_icao_code)})</div>
                <div><span class="font-semibold">Ground speed:</span> {formatValue($selectedFlight.ground_speed, 'kt')}</div>
                <div><span class="font-semibold">Indicated air speed:</span> {formatValue($selectedFlight.indicated_air_speed, 'kt')}</div>
                <div><span class="font-semibold">True air speed:</span> {formatValue($selectedFlight.true_air_speed, 'kt')}</div>
                <div><span class="font-semibold">Barometrisk höjd:</span> {formatValue($selectedFlight.barometric_altitude, 'ft')}</div>
                <div><span class="font-semibold">Geometrisk höjd:</span> {formatValue($selectedFlight.geometric_altitude, 'ft')}</div>
                <div><span class="font-semibold">Flugen distans:</span> {formatValue($selectedFlight.distance_flown, 'km')}</div>
                <div><span class="font-semibold">Ruttavstånd:</span> {formatValue($selectedFlight.route_distance, 'km')}</div>
                <div><span class="font-semibold">Kvarvarande distans:</span> {formatValue($selectedFlight.distance_remaining, 'km')}</div>
                <div><span class="font-semibold">Interesting:</span> {formatValue($selectedFlight.interesting_group)}</div>
            </div>
        {/if}
    </div>
    <form method="dialog" class="modal-backdrop">
        <button>close</button>
    </form>
</dialog>
