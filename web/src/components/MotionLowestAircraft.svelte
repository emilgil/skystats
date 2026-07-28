<script>
    import MotionStats from './MotionStats.svelte';
    import { IconArrowDownDashed } from '@tabler/icons-svelte';

    const columns = [
        { header: 'Reg', field: 'registration', class: 'font-mono' },
        { header: 'Airline', field: 'airline_name', formatter: (value) => value || '-' },
        { header: 'Model', field: 'type' },
        // { header: 'Flight', field: 'flight' },
        { 
            header: 'Altitude', 
            field: 'barometric_altitude',
            formatter: (value) => value ? `${value.toLocaleString()} ft` : '-'
        },
        { 
            header: 'First Seen', 
            field: 'first_seen',
            formatter: (value) => value ? new Date(value).toLocaleString() : '-'
        }
    ];
</script>

<MotionStats 
    endpoint="api/stats/motion/lowest"
    title="Lowest Aircraft"
    {columns}
    icon={IconArrowDownDashed}
/>
