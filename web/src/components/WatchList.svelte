<script>
    import { onMount } from 'svelte';
    import { IconEdit, IconTrash, IconPlus } from '@tabler/icons-svelte';
    import { watches, loadWatches, removeWatch, toggleWatch } from '../stores/watches';
    import WatchEditorModal from './WatchEditorModal.svelte';

    let editing = null;
    let actionError = null;

    onMount(loadWatches);

    function describe(watch) {
        const count = watch.conditions?.length ?? 0;
        const joiner = watch.combinator === 'OR' ? 'any' : 'all';
        return `${count} condition${count === 1 ? '' : 's'} (${joiner})`;
    }

    async function onToggle(watch) {
        actionError = null;
        const result = await toggleWatch(watch);
        if (!result.ok) {
            // The checkbox already flipped visually (native browser behaviour,
            // ahead of this handler). saveWatch does not call loadWatches() on
            // failure, so without this the store — and therefore the checkbox —
            // would stay out of sync with what the server actually holds.
            // Re-fetching snaps it back to the true state alongside the error.
            await loadWatches();
            actionError = result.error;
        }
    }

    async function onDelete(watch) {
        if (!confirm(`Delete the watch "${watch.name}"? Its hit history is kept.`)) return;
        actionError = null;
        const result = await removeWatch(watch.id);
        if (!result.ok) actionError = result.error;
    }
</script>

<div class="flex justify-end mb-4">
    <button class="btn btn-sm btn-primary" on:click={() => (editing = {})}>
        <IconPlus class="h-4 w-4" /> New watch
    </button>
</div>

{#if actionError}
    <div class="alert alert-error mb-4"><span>{actionError}</span></div>
{/if}

{#if $watches.loading}
    <div class="flex justify-center py-8"><span class="loading loading-spinner loading-lg"></span></div>
{:else if $watches.error}
    <div class="alert alert-error"><span>{$watches.error}</span></div>
{:else if $watches.items.length === 0}
    <p class="text-center opacity-60 py-8">
        No watches yet. Create one to get an Apprise notification when a matching aircraft shows up.
    </p>
{:else}
    <div class="overflow-x-auto">
        <table class="table table-zebra">
            <thead>
                <tr>
                    <th>Name</th>
                    <th>Conditions</th>
                    <th>Enabled</th>
                    <th class="text-right">Actions</th>
                </tr>
            </thead>
            <tbody>
                {#each $watches.items as watch (watch.id)}
                    <tr>
                        <td class="font-medium">{watch.name}</td>
                        <td class="opacity-70">{describe(watch)}</td>
                        <td>
                            <input
                                type="checkbox"
                                class="toggle toggle-sm toggle-primary"
                                checked={watch.enabled}
                                on:change={() => onToggle(watch)}
                            />
                        </td>
                        <td class="text-right whitespace-nowrap">
                            <button
                                class="btn btn-ghost btn-sm btn-square"
                                aria-label="Edit watch"
                                on:click={() => (editing = watch)}
                            >
                                <IconEdit class="h-4 w-4" />
                            </button>
                            <button
                                class="btn btn-ghost btn-sm btn-square"
                                aria-label="Delete watch"
                                on:click={() => onDelete(watch)}
                            >
                                <IconTrash class="h-4 w-4" />
                            </button>
                        </td>
                    </tr>
                {/each}
            </tbody>
        </table>
    </div>
{/if}

<WatchEditorModal watch={editing} onClose={() => (editing = null)} />
