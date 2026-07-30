import { writable } from 'svelte/store';

// Holds the hex of the aircraft whose detail modal is open, or null when closed.
export const selectedHex = writable(null);

export function openAircraftModal(hex) {
    if (!hex) return;
    selectedHex.set(hex);
}

export function closeAircraftModal() {
    selectedHex.set(null);
}
