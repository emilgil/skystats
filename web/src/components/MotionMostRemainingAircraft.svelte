<script>
    import MotionStats from './MotionStats.svelte';
    import { IconPlaneArrival } from '@tabler/icons-svelte';

    const columns = [
        { header: 'Reg', field: 'registration', class: 'font-mono whitespace-nowrap' },
        { header: 'Airline', field: 'airline_name', formatter: (value) => value || '-' },
        { header: 'Type', field: 'type' },
        { header: 'To', field: 'destination_iata_code', formatter: (value) => value || '-' },
        {
            header: 'Distance Remaining',
            field: 'distance_remaining',
            formatter: (value) => value ? `${Math.round(value).toLocaleString()} km` : '-'
        },
        {
            header: 'First Seen',
            field: 'first_seen',
            class: 'whitespace-nowrap',
            formatter: (value) => value ? new Date(value).toLocaleString() : '-'
        }
    ];
</script>

<MotionStats
    endpoint="api/stats/motion/most-remaining"
    title="Most Remaining"
    {columns}
    icon={IconPlaneArrival}
/>
