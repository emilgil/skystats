<script>
    import { IconEyeOff } from '@tabler/icons-svelte';
    import { hiddenCards } from '../stores/hiddenCards';

    export let id;
    export let title;

    function hide() {
        hiddenCards.hide(id);
    }
</script>

{#if !$hiddenCards.includes(id)}
    <div class="relative group">
        <button
            type="button"
            class="btn btn-ghost btn-xs btn-circle absolute right-2 top-2 z-10 opacity-40 group-hover:opacity-100 transition-opacity"
            title="Hide {title}"
            aria-label="Hide {title}"
            on:click={hide}
        >
            <IconEyeOff class="w-4 h-4" />
        </button>
        <slot />
    </div>
{/if}
