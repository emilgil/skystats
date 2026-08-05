<script>
    export let page = 1;
    export let pageSize = 50;
    export let totalCount = 0;
    export let onPageChange = () => {};

    $: totalPages = Math.max(1, Math.ceil(totalCount / pageSize));
</script>

{#if totalCount > 0}
    <div class="flex justify-center items-center gap-4 mt-4">
        <button class="btn btn-sm" disabled={page <= 1} on:click={() => onPageChange(page - 1)}>Föregående</button>
        <span>Sida {page} av {totalPages} ({totalCount} träffar)</span>
        <button class="btn btn-sm" disabled={page >= totalPages} on:click={() => onPageChange(page + 1)}>Nästa</button>
    </div>
{/if}
