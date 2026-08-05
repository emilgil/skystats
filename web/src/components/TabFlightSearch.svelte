<script>
    import SearchFilterForm from './SearchFilterForm.svelte';
    import SearchResultsTable from './SearchResultsTable.svelte';
    import SearchPagination from './SearchPagination.svelte';
    import FlightDetailModal from './FlightDetailModal.svelte';
    import { openFlightModal } from '../stores/flightDetailModal';
    import { buildFlightSearchUrl, buildFlightSearchExportUrl } from '../lib/flightSearchUrl';

    let filters = null;
    let results = [];
    let totalCount = 0;
    let page = 1;
    const pageSize = 50;
    let sort = 'first_seen';
    let dir = 'desc';
    let loading = false;
    let error = null;
    let searched = false;
    let exporting = false;
    let exportMessage = null;

    let requestSeq = 0;

    async function runSearch(resetPage) {
        if (!filters) return;
        if (resetPage) page = 1;
        const seq = ++requestSeq;
        loading = true;
        error = null;
        exportMessage = null;
        searched = true;
        try {
            const response = await fetch(buildFlightSearchUrl(filters, sort, dir, page, pageSize));
            if (!response.ok) {
                const body = await response.json().catch(() => ({}));
                throw new Error(body.error || `${response.status}`);
            }
            const result = await response.json();
            if (seq !== requestSeq) return;
            results = result.results;
            totalCount = result.total_count;
        } catch (err) {
            if (seq !== requestSeq) return;
            error = err.message;
            results = [];
            totalCount = 0;
        } finally {
            if (seq === requestSeq) loading = false;
        }
    }

    function handleSearchSubmit(event) {
        filters = event.detail.filters;
        runSearch(true);
    }

    function handleSort(column) {
        if (sort === column) {
            dir = dir === 'asc' ? 'desc' : 'asc';
        } else {
            sort = column;
            dir = 'desc';
        }
        runSearch(true);
    }

    function handlePageChange(newPage) {
        page = newPage;
        runSearch(false);
    }

    async function handleExport() {
        if (!filters) return;
        exporting = true;
        exportMessage = null;
        try {
            const response = await fetch(buildFlightSearchExportUrl(filters, sort, dir));
            if (!response.ok) {
                const body = await response.json().catch(() => ({}));
                throw new Error(body.error || `${response.status}`);
            }
            if (response.headers.get('X-Search-Truncated') === 'true') {
                exportMessage = 'Exporten begränsades till 10 000 rader. Förfina sökningen för att få med allt.';
            }
            const blob = await response.blob();
            const url = URL.createObjectURL(blob);
            const a = document.createElement('a');
            a.href = url;
            a.download = 'flight-search.csv';
            document.body.appendChild(a);
            a.click();
            a.remove();
            URL.revokeObjectURL(url);
        } catch (err) {
            exportMessage = `Export misslyckades: ${err.message}`;
        } finally {
            exporting = false;
        }
    }
</script>

<div>
    <SearchFilterForm on:search={handleSearchSubmit} />

    {#if exportMessage}
        <div class="alert alert-warning mt-4">
            <span>{exportMessage}</span>
        </div>
    {/if}

    {#if searched}
        <div class="flex justify-end mt-4">
            <button class="btn btn-sm btn-outline" on:click={handleExport} disabled={exporting}>
                {exporting ? 'Exporterar...' : 'Exportera CSV'}
            </button>
        </div>

        <SearchResultsTable
            {results}
            {loading}
            {error}
            {sort}
            {dir}
            onSort={handleSort}
            onRowClick={(row) => openFlightModal(row)}
        />

        <SearchPagination
            {page}
            {pageSize}
            {totalCount}
            onPageChange={handlePageChange}
        />
    {/if}
</div>

<FlightDetailModal />
