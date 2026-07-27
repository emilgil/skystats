import { writable } from 'svelte/store';

// Current leaderboard period. A preset string for now; the custom-range seam
// will later allow an object descriptor ({ from, to }).
export const recordPeriod = writable('all_time');

export const RECORD_PERIODS = [
    { value: '24h', label: '24h' },
    { value: '7d', label: '7d' },
    { value: '30d', label: '30d' },
    { value: '90d', label: '90d' },
    { value: '365d', label: '365d' },
    { value: 'all_time', label: 'All-time' }
];

// Single place that turns (endpoint, period) into a URL. Later: if `period` is
// an object { from, to }, append &from=&to= here instead.
export function buildRecordUrl(endpoint, period) {
    const sep = endpoint.includes('?') ? '&' : '?';
    return `${endpoint}${sep}period=${encodeURIComponent(period)}`;
}
