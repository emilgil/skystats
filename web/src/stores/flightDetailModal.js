import { writable } from 'svelte/store';

// Holds the full flight-search result row whose detail modal is open, or
// null when closed. Unlike aircraftModal.js's selectedHex, this needs no
// fetch-by-key — the row already carries every field the modal displays.
export const selectedFlight = writable(null);

export function openFlightModal(flight) {
    if (!flight) return;
    selectedFlight.set(flight);
}

export function closeFlightModal() {
    selectedFlight.set(null);
}
