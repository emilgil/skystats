import { writable } from 'svelte/store';

export const watches = writable({ items: [], loading: true, error: null });
export const watchFields = writable({ fields: [], operators: {} });

export async function loadWatches() {
    watches.update((s) => ({ ...s, loading: true, error: null }));
    try {
        const response = await fetch('/api/watches');
        if (!response.ok) throw new Error('Failed to load watches');
        const items = await response.json();
        watches.set({ items: items ?? [], loading: false, error: null });
    } catch (error) {
        console.error('Failed to load watches:', error);
        watches.set({ items: [], loading: false, error: 'Could not load watches' });
    }
}

export async function loadWatchFields() {
    try {
        const response = await fetch('/api/watches/fields');
        if (!response.ok) throw new Error('Failed to load watch fields');
        const data = await response.json();
        watchFields.set({ fields: data.fields ?? [], operators: data.operators ?? {} });
    } catch (error) {
        console.error('Failed to load watch fields:', error);
    }
}

// errorFrom pulls the server's message out of a failed response so validation
// errors surface in the form instead of a generic failure.
async function errorFrom(response, fallback) {
    const data = await response.json().catch(() => ({}));
    return data.error || fallback;
}

export async function saveWatch(watch) {
    const isUpdate = Boolean(watch.id);
    const body = {
        name: watch.name,
        enabled: watch.enabled,
        combinator: watch.combinator,
        apprise_key: watch.apprise_key ?? '',
        conditions: (watch.conditions ?? []).map((c) => ({
            field: c.field,
            operator: c.operator,
            value: c.value ?? ''
        }))
    };

    try {
        const response = await fetch(isUpdate ? `/api/watches/${watch.id}` : '/api/watches', {
            method: isUpdate ? 'PUT' : 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(body)
        });
        if (!response.ok) {
            return { ok: false, error: await errorFrom(response, 'Could not save the watch') };
        }
        await loadWatches();
        return { ok: true, error: null };
    } catch (error) {
        console.error('Failed to save watch:', error);
        return { ok: false, error: 'Could not save the watch' };
    }
}

export async function removeWatch(id) {
    try {
        const response = await fetch(`/api/watches/${id}`, { method: 'DELETE' });
        if (!response.ok) {
            return { ok: false, error: await errorFrom(response, 'Could not delete the watch') };
        }
        await loadWatches();
        return { ok: true, error: null };
    } catch (error) {
        console.error('Failed to delete watch:', error);
        return { ok: false, error: 'Could not delete the watch' };
    }
}

export async function toggleWatch(watch) {
    return saveWatch({ ...watch, enabled: !watch.enabled });
}

export async function loadWatchHits(watchId) {
    const query = watchId ? `?watch_id=${watchId}&limit=50` : '?limit=50';
    try {
        const response = await fetch(`/api/watches/hits${query}`);
        if (!response.ok) throw new Error('Failed to load watch hits');
        const data = await response.json();
        return data.hits ?? [];
    } catch (error) {
        console.error('Failed to load watch hits:', error);
        return [];
    }
}
