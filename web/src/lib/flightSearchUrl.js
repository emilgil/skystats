// Turns a SearchFilterForm filters object plus sort/pagination state into a
// URLSearchParams for /api/search/flights. Centralized here (mirroring
// stores/recordPeriod.js's buildRecordUrl) so the JSON search call and the
// CSV export call can never drift apart on which params they send.
export function buildFlightSearchParams(filters, sort, dir, page, pageSize) {
    const params = new URLSearchParams();

    if (filters.useCustomRange && filters.from && filters.to) {
        params.set('from', filters.from);
        params.set('to', filters.to);
    } else {
        params.set('period', filters.period);
    }

    if (filters.manufacturer) params.set('manufacturer', filters.manufacturer);
    if (filters.model) params.set('model', filters.model);
    if (filters.country) params.set('country', filters.country);
    if (filters.origin) params.set('origin', filters.origin);
    if (filters.destination) params.set('destination', filters.destination);
    if (filters.altitudeValue !== '') {
        params.set('altitude_op', filters.altitudeOp);
        params.set('altitude_value', filters.altitudeValue);
    }
    if (filters.speedValue !== '') {
        params.set('speed_op', filters.speedOp);
        params.set('speed_value', filters.speedValue);
    }
    if (filters.airline) params.set('airline', filters.airline);
    if (filters.interesting) params.set('interesting', filters.interesting);
    if (filters.originStatus && filters.originStatus !== 'any') params.set('origin_status', filters.originStatus);
    if (filters.destinationStatus && filters.destinationStatus !== 'any') params.set('destination_status', filters.destinationStatus);
    if (filters.q) params.set('q', filters.q);

    params.set('sort', sort);
    params.set('dir', dir);
    params.set('page', String(page));
    params.set('page_size', String(pageSize));

    return params;
}

export function buildFlightSearchUrl(filters, sort, dir, page, pageSize) {
    return `/api/search/flights?${buildFlightSearchParams(filters, sort, dir, page, pageSize)}`;
}

export function buildFlightSearchExportUrl(filters, sort, dir) {
    const params = buildFlightSearchParams(filters, sort, dir, 1, 50);
    params.delete('page');
    params.delete('page_size');
    return `/api/search/flights/export?${params}`;
}
