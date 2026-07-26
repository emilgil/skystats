<script>
    import MotionStats from './MotionStats.svelte';
    import { IconRoute } from '@tabler/icons-svelte';

    const columns = [
        { header: 'Reg', field: 'registration', class: 'font-mono whitespace-nowrap' },
        { header: 'Type', field: 'type' },
        { header: 'From', field: 'origin_iata_code', formatter: (value) => value || '-' },
        { header: 'To', field: 'destination_iata_code', formatter: (value) => value || '-' },
        {
            header: 'Route Distance',
            field: 'route_distance',
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
    endpoint="api/stats/motion/longest-route"
    title="Longest Route"
    {columns}
    icon={IconRoute}
/>
