<script>
    import { onMount } from 'svelte';
    import { IconTrash, IconPlus, IconAlertTriangle } from '@tabler/icons-svelte';
    import { watchFields, loadWatchFields, saveWatch } from '../stores/watches';

    export let watch = null;
    export let onClose = () => {};

    let name = '';
    let enabled = true;
    let combinator = 'AND';
    let appriseKey = '';
    let conditions = [];
    let error = null;
    let isSaving = false;
    let loadedId = undefined;

    onMount(loadWatchFields);

    // Reset the form whenever a different watch is opened. Tracking the id (and
    // undefined for "no watch open") keeps typing from being clobbered by
    // reactive re-runs while the modal stays open.
    $: if (watch && loadedId !== (watch.id ?? 'new')) {
        loadedId = watch.id ?? 'new';
        name = watch.name ?? '';
        enabled = watch.enabled ?? true;
        combinator = watch.combinator ?? 'AND';
        appriseKey = watch.apprise_key ?? '';
        conditions = (watch.conditions ?? []).map((c) => ({ ...c }));
        if (conditions.length === 0) addCondition();
        error = null;
    } else if (!watch) {
        loadedId = undefined;
    }

    $: fields = $watchFields.fields;
    $: operatorLabels = $watchFields.operators;

    function fieldFor(key) {
        return fields.find((f) => f.key === key);
    }

    function addCondition() {
        const first = fields[0];
        conditions = [
            ...conditions,
            { field: first?.key ?? 'callsign', operator: first?.operators?.[0] ?? 'contains', value: '' }
        ];
    }

    function removeCondition(index) {
        conditions = conditions.filter((_, i) => i !== index);
    }

    // Changing the field may invalidate the selected operator, so snap it back
    // to the first one the new field allows.
    function onFieldChange(index) {
        const field = fieldFor(conditions[index].field);
        if (field && !field.operators.includes(conditions[index].operator)) {
            conditions[index].operator = field.operators[0];
        }
        if (field?.kind === 'flag') {
            conditions[index].value = '';
        }
        conditions = conditions;
    }

    function useEmergencySquawks(index) {
        conditions[index].field = 'squawk';
        conditions[index].operator = 'in_list';
        conditions[index].value = '7500,7600,7700';
        conditions = conditions;
    }

    async function save() {
        isSaving = true;
        error = null;
        const result = await saveWatch({
            id: watch.id,
            name,
            enabled,
            combinator,
            apprise_key: appriseKey,
            conditions
        });
        isSaving = false;
        if (result.ok) {
            onClose();
        } else {
            error = result.error;
        }
    }
</script>

{#if watch}
    <dialog class="modal modal-open">
        <div class="modal-box max-w-3xl">
            <h3 class="text-lg font-bold mb-4">{watch.id ? 'Edit watch' : 'New watch'}</h3>

            {#if $watchFields.loading}
                <div class="flex justify-center py-8">
                    <span class="loading loading-ring loading-lg"></span>
                </div>
            {:else if $watchFields.error}
                <div class="alert alert-error mb-4">
                    <IconAlertTriangle class="h-5 w-5" />
                    <span>{$watchFields.error}</span>
                </div>
            {:else}
                <label class="form-control w-full mb-4">
                    <div class="label"><span class="label-text">Name</span></div>
                    <input
                        type="text"
                        class="input input-bordered w-full"
                        placeholder="e.g. Boeing 747 within 50 km"
                        maxlength="200"
                        bind:value={name}
                    />
                </label>

                <div class="flex flex-wrap items-center gap-4 mb-4">
                    <label class="label cursor-pointer gap-2">
                        <input type="checkbox" class="toggle toggle-primary" bind:checked={enabled} />
                        <span class="label-text">Enabled</span>
                    </label>

                    <label class="form-control">
                        <div class="label"><span class="label-text">Match</span></div>
                        <select class="select select-bordered" bind:value={combinator}>
                            <option value="AND">all conditions</option>
                            <option value="OR">any condition</option>
                        </select>
                    </label>
                </div>

                <div class="divider">Conditions</div>

                {#each conditions as condition, index (index)}
                    <div class="flex flex-wrap items-end gap-2 mb-3">
                        <select
                            class="select select-bordered select-sm grow"
                            bind:value={condition.field}
                            on:change={() => onFieldChange(index)}
                        >
                            {#each fields as field (field.key)}
                                <option value={field.key}>{field.label}</option>
                            {/each}
                        </select>

                        <select class="select select-bordered select-sm" bind:value={condition.operator}>
                            {#each fieldFor(condition.field)?.operators ?? [] as operator}
                                <option value={operator}>{operatorLabels[operator] ?? operator}</option>
                            {/each}
                        </select>

                        {#if fieldFor(condition.field)?.kind !== 'flag'}
                            <input
                                type="text"
                                class="input input-bordered input-sm grow"
                                placeholder={fieldFor(condition.field)?.kind === 'number'
                                    ? fieldFor(condition.field)?.unit ?? 'value'
                                    : 'value'}
                                bind:value={condition.value}
                            />
                        {/if}

                        {#if condition.field === 'squawk'}
                            <button class="btn btn-sm btn-outline" on:click={() => useEmergencySquawks(index)}>
                                Emergency
                            </button>
                        {/if}

                        <button
                            class="btn btn-sm btn-ghost btn-square"
                            aria-label="Remove condition"
                            on:click={() => removeCondition(index)}
                        >
                            <IconTrash class="h-4 w-4" />
                        </button>
                    </div>

                    {#if fieldFor(condition.field)?.hint}
                        <p class="text-xs opacity-60 mb-3 -mt-2">{fieldFor(condition.field).hint}</p>
                    {/if}
                {/each}

                <button class="btn btn-sm btn-outline mt-2" on:click={addCondition}>
                    <IconPlus class="h-4 w-4" /> Add condition
                </button>

                <label class="form-control w-full mt-6">
                    <div class="label">
                        <span class="label-text">Apprise key (optional)</span>
                    </div>
                    <input
                        type="text"
                        class="input input-bordered w-full"
                        placeholder="Leave blank to use the key from Settings"
                        bind:value={appriseKey}
                    />
                </label>

                {#if error}
                    <div class="alert alert-error mt-4">
                        <IconAlertTriangle class="h-5 w-5" />
                        <span>{error}</span>
                    </div>
                {/if}
            {/if}

            <div class="modal-action">
                <button class="btn" on:click={onClose} disabled={isSaving}>Cancel</button>
                <button
                    class="btn btn-primary"
                    on:click={save}
                    disabled={isSaving || $watchFields.loading || !!$watchFields.error}
                >
                    {isSaving ? 'Saving…' : 'Save'}
                </button>
            </div>
        </div>
        <form method="dialog" class="modal-backdrop">
            <button on:click={onClose}>close</button>
        </form>
    </dialog>
{/if}
