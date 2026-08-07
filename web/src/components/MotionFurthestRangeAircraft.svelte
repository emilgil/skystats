<script>
    import MotionStats from './MotionStats.svelte';
    import { IconTelescope } from '@tabler/icons-svelte';

    const columns = [
        { header: 'Reg', field: 'registration', class: 'font-mono whitespace-nowrap' },
        { header: 'Type', field: 'type' },
        {
            header: 'Distance',
            field: 'max_distance_receiver',
            formatter: (value) => value != null ? `${Math.round(value).toLocaleString()} km` : '-'
        },
        {
            header: 'Altitude',
            field: 'max_distance_receiver_altitude',
            formatter: (value) => value != null ? `${Math.round(value).toLocaleString()} ft` : '-'
        },
        {
            header: 'Bearing',
            field: 'max_distance_receiver_bearing',
            formatter: (value) => value != null ? `${Math.round(value)}°` : '-'
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
    endpoint="api/stats/motion/furthest"
    title="Furthest"
    {columns}
    icon={IconTelescope}
/>
