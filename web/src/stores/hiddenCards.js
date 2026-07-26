import { derived, get } from 'svelte/store';
import { settings } from './settings';

function parseHiddenCards(settingsObj) {
    const raw = settingsObj?.hidden_cards?.setting_value;
    if (!raw) return [];
    try {
        const parsed = JSON.parse(raw);
        return Array.isArray(parsed) ? parsed : [];
    } catch {
        return [];
    }
}

// Reactive array of currently-hidden card IDs, derived from the settings store.
export const hiddenCardIds = derived(settings, parseHiddenCards);

export const hiddenCards = {
    subscribe: hiddenCardIds.subscribe,

    async hide(id) {
        const current = parseHiddenCards(get(settings));
        if (current.includes(id)) return;
        await settings.save({ hidden_cards: JSON.stringify([...current, id]) });
    },

    async show(id) {
        const current = parseHiddenCards(get(settings));
        await settings.save({ hidden_cards: JSON.stringify(current.filter((c) => c !== id)) });
    }
};
