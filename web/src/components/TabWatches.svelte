<script>
    import HideableCard from './HideableCard.svelte';
    import { dashboardCards } from '../lib/dashboardCards';

    const cards = dashboardCards.filter((c) => c.tab === 'watches');
</script>

<div class="grid grid-cols-1 mt-6 gap-6">
    {#each cards as card (card.id)}
        <HideableCard id={card.id} title={card.title}>
            <svelte:component this={card.component} {...(card.props || {})} />
        </HideableCard>
    {/each}
</div>
