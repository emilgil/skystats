<script>
    import HideableCard from './HideableCard.svelte';
    import { dashboardCards } from '../lib/dashboardCards';
    import { recordPeriod, RECORD_PERIODS } from '../stores/recordPeriod';

    const cards = dashboardCards.filter((c) => c.tab === 'motion-stat');
</script>

<div class="flex justify-center mt-10">
    <div class="join">
        {#each RECORD_PERIODS as p}
            <button
                type="button"
                class="join-item btn btn-sm {$recordPeriod === p.value ? 'btn-active btn-primary' : ''}"
                on:click={() => recordPeriod.set(p.value)}
            >
                {p.label}
            </button>
        {/each}
    </div>
</div>

<div class="grid grid-cols-1 lg:grid-cols-2 mt-6 gap-6">
    {#each cards as card (card.id)}
        <HideableCard id={card.id} title={card.title}>
            <svelte:component this={card.component} {...(card.props || {})} />
        </HideableCard>
    {/each}
</div>
