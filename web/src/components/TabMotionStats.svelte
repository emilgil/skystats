<script>
    import MotionFastestAircraft from './MotionFastestAircraft.svelte';
    import MotionSlowestAircraft from './MotionSlowestAircraft.svelte';
    import MotionHighestAircraft from './MotionHighestAircraft.svelte';
    import MotionLowestAircraft from './MotionLowestAircraft.svelte';
    import MotionFurthestFlownAircraft from './MotionFurthestFlownAircraft.svelte';
    import MotionMostRemainingAircraft from './MotionMostRemainingAircraft.svelte';
    import MotionLongestRouteAircraft from './MotionLongestRouteAircraft.svelte';
</script>

<div class="grid grid-cols-1 lg:grid-cols-2 mt-10 gap-6">
    <MotionFastestAircraft />
    <MotionSlowestAircraft />
    <MotionHighestAircraft />
    <MotionLowestAircraft />
    <MotionFurthestFlownAircraft />
    <MotionMostRemainingAircraft />
    <MotionLongestRouteAircraft />
</div>
