<script>
    import MotionStats from './MotionStats.svelte';
    import { IconHistory } from '@tabler/icons-svelte';

    const columns = [
        { header: 'Reg', field: 'registration', class: 'font-mono whitespace-nowrap' },
        { header: 'Airline', field: 'airline_name', formatter: (value) => value || '-' },
        { header: 'Type', field: 'type' },
        { header: 'From', field: 'origin_iata_code', formatter: (value) => value || '-' },
        { header: 'To', field: 'destination_iata_code', formatter: (value) => value || '-' },
        {
            header: 'First Seen',
            field: 'first_seen',
            class: 'whitespace-nowrap',
            formatter: (value) => value ? new Date(value).toLocaleString() : '-'
        }
    ];
</script>

<MotionStats
    endpoint="api/stats/motion/recent"
    title="Recent Observations"
    {columns}
    icon={IconHistory}
/>
