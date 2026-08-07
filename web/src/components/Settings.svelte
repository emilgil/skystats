<script>
    import { onMount } from 'svelte';
    import { settings, refreshRecordHolderData } from '../stores/settings';
    import { hiddenCards } from '../stores/hiddenCards';
    import { dashboardCards } from '../lib/dashboardCards';

    const cardTabLabels = {
        global: 'Always Visible',
        'current-stat': 'Current Sightings',
        activity: 'Activity',
        'route-stat': 'Route Information',
        'interesting-stat': 'Interesting Aircraft',
        'motion-stat': 'Record Holders',
        watches: 'Watches'
    };
    const cardTabOrder = ['global', 'current-stat', 'activity', 'route-stat', 'interesting-stat', 'motion-stat', 'watches'];

    function toggleCardVisibility(cardId, visible) {
        if (visible) {
            hiddenCards.show(cardId);
        } else {
            hiddenCards.hide(cardId);
        }
    }
    import { IconBrandGithub } from '@tabler/icons-svelte';


    let activeMenuItem = 'display';
    let isSaving = false;

    let routeTableLimit;
    let interestingTableLimit;
    let recordHolderTableLimit;
    let disablePlaneAlertDbTags;
    let settingsChanged = false;
    let version = { version: '...', commit: '...', date: '...' };

    let notificationsEnabled = false;
    let appriseApiUrl = '';
    let appriseConfigKey = '';
    let notifyGroupMil = true, notifyGroupGov = true, notifyGroupPol = true, notifyGroupCiv = true;
    let notifyRecordFastest = true, notifyRecordSlowest = true, notifyRecordHighest = true, notifyRecordLowest = true;
    let notifyRecordFurthestFlown = true, notifyRecordLongestRoute = true, notifyRecordMostRemaining = true;
    let notifyRecordNearest = true, notifyRecordFurthestRange = true;
    let notificationCooldownMinutes = 60;
    let notificationDelaySeconds = 30;
    let notificationsChanged = false;
    let isTestingNotification = false;
    let testResult = null;

    const menuItems = [
        { id: 'display', label: 'Display' },
        { id: 'cards', label: 'Cards' },
        { id: 'notifications', label: 'Notifications' },
        { id: 'about', label: 'About' },
        { id: 'danger-zone', label: 'Danger Zone', danger: true }
    ];

    // Keys must match records.category in the database; the backend rejects anything else.
    const recordCategories = [
        { key: 'fastest', label: 'Fastest' },
        { key: 'slowest', label: 'Slowest' },
        { key: 'highest', label: 'Highest' },
        { key: 'lowest', label: 'Lowest' },
        { key: 'furthest_flown', label: 'Furthest flown' },
        { key: 'longest_route', label: 'Longest route' },
        { key: 'most_remaining', label: 'Most remaining' },
        { key: 'nearest', label: 'Nearest' },
        { key: 'furthest_range', label: 'Furthest' }
    ];

    let selectedCategories = new Set();
    let isClearing = false;
    let clearResult = null;

    $: allCategoriesSelected = recordCategories.every((c) => selectedCategories.has(c.key));

    function toggleCategory(key, checked) {
        if (checked) {
            selectedCategories.add(key);
        } else {
            selectedCategories.delete(key);
        }
        selectedCategories = selectedCategories; // Set mutation is not reactive on its own
        clearResult = null;
    }

    function toggleAllCategories(checked) {
        selectedCategories = checked ? new Set(recordCategories.map((c) => c.key)) : new Set();
        clearResult = null;
    }

    function openClearConfirm() {
        if (selectedCategories.size === 0) return;
        clearResult = null;
        document.getElementById('clear-records-modal')?.showModal();
    }

    async function clearSelectedRecords() {
        document.getElementById('clear-records-modal')?.close();
        isClearing = true;
        try {
            const response = await fetch('/api/records', {
                method: 'DELETE',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ categories: Array.from(selectedCategories) })
            });
            if (response.ok) {
                const data = await response.json();
                const rows = Object.values(data.cleared ?? {}).reduce((sum, n) => sum + n, 0);
                clearResult = { ok: true, message: `Cleared ${rows} record${rows === 1 ? '' : 's'}.` };
                selectedCategories = new Set();
                refreshRecordHolderData.update((n) => n + 1);
            } else {
                const data = await response.json().catch(() => ({}));
                clearResult = { ok: false, message: data.error || 'Failed to clear records' };
            }
        } catch (e) {
            clearResult = { ok: false, message: e.message };
        }
        isClearing = false;
    }

    $: if (!settingsChanged) {
        if ($settings.route_table_limit) {
            routeTableLimit = parseInt($settings.route_table_limit.setting_value);
        }
        if ($settings.interesting_table_limit) {
            interestingTableLimit = parseInt($settings.interesting_table_limit.setting_value);
        }
        if ($settings.record_holder_table_limit) {
            recordHolderTableLimit = parseInt($settings.record_holder_table_limit.setting_value);
        }
        if ($settings.disable_planealertdb_tags) {
            disablePlaneAlertDbTags = $settings.disable_planealertdb_tags.setting_value === 'true';
        }
    }

    $: if (!notificationsChanged) {
        if ($settings.notifications_enabled) notificationsEnabled = $settings.notifications_enabled.setting_value === 'true';
        if ($settings.apprise_api_url) appriseApiUrl = $settings.apprise_api_url.setting_value;
        if ($settings.apprise_config_key) appriseConfigKey = $settings.apprise_config_key.setting_value;
        if ($settings.notify_group_mil) notifyGroupMil = $settings.notify_group_mil.setting_value === 'true';
        if ($settings.notify_group_gov) notifyGroupGov = $settings.notify_group_gov.setting_value === 'true';
        if ($settings.notify_group_pol) notifyGroupPol = $settings.notify_group_pol.setting_value === 'true';
        if ($settings.notify_group_civ) notifyGroupCiv = $settings.notify_group_civ.setting_value === 'true';
        if ($settings.notify_record_fastest) notifyRecordFastest = $settings.notify_record_fastest.setting_value === 'true';
        if ($settings.notify_record_slowest) notifyRecordSlowest = $settings.notify_record_slowest.setting_value === 'true';
        if ($settings.notify_record_highest) notifyRecordHighest = $settings.notify_record_highest.setting_value === 'true';
        if ($settings.notify_record_lowest) notifyRecordLowest = $settings.notify_record_lowest.setting_value === 'true';
        if ($settings.notify_record_furthest_flown) notifyRecordFurthestFlown = $settings.notify_record_furthest_flown.setting_value === 'true';
        if ($settings.notify_record_longest_route) notifyRecordLongestRoute = $settings.notify_record_longest_route.setting_value === 'true';
        if ($settings.notify_record_most_remaining) notifyRecordMostRemaining = $settings.notify_record_most_remaining.setting_value === 'true';
        if ($settings.notify_record_nearest) notifyRecordNearest = $settings.notify_record_nearest.setting_value === 'true';
        if ($settings.notify_record_furthest_range) notifyRecordFurthestRange = $settings.notify_record_furthest_range.setting_value === 'true';
        if ($settings.notification_cooldown_minutes) notificationCooldownMinutes = parseInt($settings.notification_cooldown_minutes.setting_value);
        if ($settings.notification_delay_seconds) notificationDelaySeconds = parseInt($settings.notification_delay_seconds.setting_value);
    }

    function handleSettingChange() {
        settingsChanged = true;
    }

    async function saveSettings() {
        const form = document.getElementById('display-settings-form');
        if (form && !form.checkValidity()) {
            form.reportValidity();
            return;
        }

        isSaving = true;
        const updates = {
            route_table_limit: routeTableLimit.toString(),
            interesting_table_limit: interestingTableLimit.toString(),
            record_holder_table_limit: recordHolderTableLimit.toString(),
            disable_planealertdb_tags: disablePlaneAlertDbTags.toString()
        };

        const success = await settings.save(updates);
        if (success) {
            settingsChanged = false;
            const modal = document.getElementById('settings-modal');
            if (modal) modal.close();
        }
        isSaving = false;
    }

    function handleNotificationChange() {
        notificationsChanged = true;
        testResult = null;
    }

    async function saveNotificationSettings() {
        isSaving = true;
        const updates = {
            notifications_enabled: notificationsEnabled.toString(),
            apprise_api_url: appriseApiUrl ?? '',
            apprise_config_key: appriseConfigKey ?? '',
            notify_group_mil: notifyGroupMil.toString(),
            notify_group_gov: notifyGroupGov.toString(),
            notify_group_pol: notifyGroupPol.toString(),
            notify_group_civ: notifyGroupCiv.toString(),
            notification_cooldown_minutes: (Number.isFinite(notificationCooldownMinutes) && notificationCooldownMinutes >= 1 ? Math.floor(notificationCooldownMinutes) : 60).toString(),
            notification_delay_seconds: (Number.isFinite(notificationDelaySeconds) && notificationDelaySeconds >= 0 ? Math.floor(notificationDelaySeconds) : 30).toString(),
            notify_record_fastest: notifyRecordFastest.toString(),
            notify_record_slowest: notifyRecordSlowest.toString(),
            notify_record_highest: notifyRecordHighest.toString(),
            notify_record_lowest: notifyRecordLowest.toString(),
            notify_record_furthest_flown: notifyRecordFurthestFlown.toString(),
            notify_record_longest_route: notifyRecordLongestRoute.toString(),
            notify_record_most_remaining: notifyRecordMostRemaining.toString(),
            notify_record_nearest: notifyRecordNearest.toString(),
            notify_record_furthest_range: notifyRecordFurthestRange.toString()
        };
        const success = await settings.save(updates);
        if (success) notificationsChanged = false;
        isSaving = false;
    }

    async function testNotification() {
        isTestingNotification = true;
        testResult = null;
        try {
            const response = await fetch('/api/notifications/test', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({
                    apprise_api_url: appriseApiUrl ?? '',
                    apprise_config_key: appriseConfigKey ?? ''
                })
            });
            if (response.ok) {
                testResult = { ok: true, message: 'Test notification sent!' };
            } else {
                const data = await response.json().catch(() => ({}));
                testResult = { ok: false, message: data.error || 'Failed to send test notification' };
            }
        } catch (e) {
            testResult = { ok: false, message: e.message };
        }
        isTestingNotification = false;
    }

    async function fetchVersion() {
        try {
            const response = await fetch('/api/version');
            if (response.ok) {
                version = await response.json();
            }
        } catch (error) {
            console.error('Failed to fetch version:', error);
        }
    }

    onMount(() => {
        settings.load();
        fetchVersion();
    });
</script>

<dialog id="settings-modal" class="modal">
    <div class="modal-box w-11/12 max-w-5xl h-[600px] p-0 relative">
        <form method="dialog" class="absolute right-2 top-2 z-10">
            <button class="btn btn-md btn-circle btn-ghost text-2xl">✕</button>
        </form>
        <div class="flex h-full">
            <!-- Settings Menu -->
            <div class="w-56 bg-base-200 p-4">
                <h3 class="text-xl font-bold mb-6 px-3">Settings</h3>
                <ul class="menu">
                    {#each menuItems as item}
                        <li>
                            <button
                                type="button"
                                class="{activeMenuItem === item.id ? 'active' : ''} {item.danger ? 'text-error' : ''}"
                                on:click={() => activeMenuItem = item.id}
                            >
                                {item.label}
                            </button>
                        </li>
                    {/each}
                </ul>
            </div>

            <!-- Settings -->
            <div class="flex-1 p-6 flex flex-col">
                <div class="flex-1 {activeMenuItem === 'about' ? 'flex items-center justify-center' : ''}">
                    {#if activeMenuItem === 'display'}
                        <h4 class="text-lg font-semibold mb-6">Display Settings</h4>

                        <form id="display-settings-form" class="space-y-6">
                            
                            <!-- Route Table Display Settings -->
                            <div>
                                <p class="text-xl font-extralight tracking-wider mb-4">Route Information</p>
                                <p class="text-m text-base-content/70 mb-2">
                                    Number of rows to display in "Route Information" tables
                                </p>
                                <input
                                    id="route-table-limit"
                                    type="number"
                                    bind:value={routeTableLimit}
                                    on:input={handleSettingChange}
                                    min="1"
                                    max="100"
                                    step="1"
                                    required
                                    class="input w-20"
                                />
                                <span class="ml-2 text-sm text-base-content/70">(1-100)</span>
                            </div>

                            <!-- Interesting Table Display Settings -->

                            <!-- Interesting Table Display Settings: Table Limit -->
                            <div>
                                <p class="text-xl font-extralight tracking-wider mb-4">Interesting Aircraft</p>
                                <p class="text-m text-base-content/70 mb-2">
                                    Number of rows to display in "Interesting Aircraft" tables
                                </p>
                                <input
                                    id="interesting-table-limit"
                                    type="number"
                                    bind:value={interestingTableLimit}
                                    on:input={handleSettingChange}
                                    min="1"
                                    max="100"
                                    step="1"
                                    required
                                    class="input w-20"
                                />
                                <span class="ml-2 text-sm text-base-content/70">(1-100)</span>
                            </div>

                            <!-- Interesting Table Display Settings: Disable Tags -->
                            <div>
                                <!-- <p class="text-xl font-extralight tracking-wider mb-4">PlaneAlertDB Tags</p> -->
                                <label class="flex items-center gap-3">
                                    <input
                                        type="checkbox"
                                        bind:checked={disablePlaneAlertDbTags}
                                        on:change={handleSettingChange}
                                        class="checkbox"
                                    />
                                    <span class="text-m text-base-content/70">
                                        Disable <a href="https://github.com/sdr-enthusiasts/plane-alert-db?tab=readme-ov-file#description-of-categories" target="_blank" rel="noopener noreferrer" class="text-accent hover:text-primary transition-colors">plane-alert-db</a> tags in modal
                                    </span>
                                </label>
                            </div>


                            <!-- Record Holder Display Settings -->
                            <div>
                                <p class="text-xl font-extralight tracking-wider mb-4">Record Holders</p>
                                <p class="text-m text-base-content/70 mb-2">
                                    Number of rows to display in "Record Holders" tables
                                </p>
                                <input
                                    id="record-holder-table-limit"
                                    type="number"
                                    bind:value={recordHolderTableLimit}
                                    on:input={handleSettingChange}
                                    min="1"
                                    max="100"
                                    step="1"
                                    required
                                    class="input w-20"
                                />
                                <span class="ml-2 text-sm text-base-content/70">(1-100)</span>
                            </div>
                        </form>

                    {:else if activeMenuItem === 'cards'}
                        <h4 class="text-lg font-semibold mb-6">Visible Cards</h4>

                        <!-- Card Visibility -->
                        <div class="mb-8">
                            <p class="text-m text-base-content/70 mb-4">
                                Uncheck a card to hide it from the dashboard. Takes effect immediately.
                            </p>
                            {#each cardTabOrder as tab}
                                <div class="mb-4">
                                    <p class="text-xs uppercase tracking-wide opacity-60 mb-2">{cardTabLabels[tab]}</p>
                                    <div class="grid grid-cols-2 gap-2">
                                        {#each dashboardCards.filter((c) => c.tab === tab) as card (card.id)}
                                            <label class="flex items-center gap-2">
                                                <input
                                                    type="checkbox"
                                                    class="checkbox checkbox-sm"
                                                    checked={!$hiddenCards.includes(card.id)}
                                                    on:change={(e) => toggleCardVisibility(card.id, e.target.checked)}
                                                />
                                                <span class="text-sm">{card.title}</span>
                                            </label>
                                        {/each}
                                    </div>
                                </div>
                            {/each}
                        </div>

                    {:else if activeMenuItem === 'notifications'}
                        <h4 class="text-lg font-semibold mb-6">Notification Settings</h4>

                        <form id="notification-settings-form" class="space-y-6">
                            <label class="flex items-center gap-3">
                                <input type="checkbox" bind:checked={notificationsEnabled} on:change={handleNotificationChange} class="checkbox" />
                                <span class="text-m">Enable Apprise notifications</span>
                            </label>

                            <div>
                                <p class="text-xl font-extralight tracking-wider mb-2">Apprise connection</p>
                                <p class="text-m text-base-content/70 mb-2">Apprise API URL</p>
                                <input type="url" bind:value={appriseApiUrl} on:input={handleNotificationChange}
                                    placeholder="http://192.168.1.10:8000" class="input w-full max-w-md" />
                                <p class="text-m text-base-content/70 mt-3 mb-2">Config key</p>
                                <input type="text" bind:value={appriseConfigKey} on:input={handleNotificationChange}
                                    placeholder="skystats" class="input w-full max-w-md" />
                            </div>

                            <div>
                                <p class="text-xl font-extralight tracking-wider mb-2">Interesting categories</p>
                                <div class="grid grid-cols-2 gap-2 max-w-md">
                                    <label class="flex items-center gap-2"><input type="checkbox" class="checkbox checkbox-sm" bind:checked={notifyGroupMil} on:change={handleNotificationChange} /><span class="text-sm">Military</span></label>
                                    <label class="flex items-center gap-2"><input type="checkbox" class="checkbox checkbox-sm" bind:checked={notifyGroupGov} on:change={handleNotificationChange} /><span class="text-sm">Government</span></label>
                                    <label class="flex items-center gap-2"><input type="checkbox" class="checkbox checkbox-sm" bind:checked={notifyGroupPol} on:change={handleNotificationChange} /><span class="text-sm">Police</span></label>
                                    <label class="flex items-center gap-2"><input type="checkbox" class="checkbox checkbox-sm" bind:checked={notifyGroupCiv} on:change={handleNotificationChange} /><span class="text-sm">Civilian</span></label>
                                </div>
                            </div>

                            <div>
                                <p class="text-xl font-extralight tracking-wider mb-2">All-time records</p>
                                <div class="grid grid-cols-2 gap-2 max-w-md">
                                    <label class="flex items-center gap-2"><input type="checkbox" class="checkbox checkbox-sm" bind:checked={notifyRecordFastest} on:change={handleNotificationChange} /><span class="text-sm">Fastest</span></label>
                                    <label class="flex items-center gap-2"><input type="checkbox" class="checkbox checkbox-sm" bind:checked={notifyRecordSlowest} on:change={handleNotificationChange} /><span class="text-sm">Slowest</span></label>
                                    <label class="flex items-center gap-2"><input type="checkbox" class="checkbox checkbox-sm" bind:checked={notifyRecordHighest} on:change={handleNotificationChange} /><span class="text-sm">Highest</span></label>
                                    <label class="flex items-center gap-2"><input type="checkbox" class="checkbox checkbox-sm" bind:checked={notifyRecordLowest} on:change={handleNotificationChange} /><span class="text-sm">Lowest</span></label>
                                    <label class="flex items-center gap-2"><input type="checkbox" class="checkbox checkbox-sm" bind:checked={notifyRecordFurthestFlown} on:change={handleNotificationChange} /><span class="text-sm">Furthest flown</span></label>
                                    <label class="flex items-center gap-2"><input type="checkbox" class="checkbox checkbox-sm" bind:checked={notifyRecordLongestRoute} on:change={handleNotificationChange} /><span class="text-sm">Longest route</span></label>
                                    <label class="flex items-center gap-2"><input type="checkbox" class="checkbox checkbox-sm" bind:checked={notifyRecordMostRemaining} on:change={handleNotificationChange} /><span class="text-sm">Most remaining</span></label>
                                    <label class="flex items-center gap-2"><input type="checkbox" class="checkbox checkbox-sm" bind:checked={notifyRecordNearest} on:change={handleNotificationChange} /><span class="text-sm">Nearest</span></label>
                                    <label class="flex items-center gap-2"><input type="checkbox" class="checkbox checkbox-sm" bind:checked={notifyRecordFurthestRange} on:change={handleNotificationChange} /><span class="text-sm">Furthest</span></label>
                                </div>
                            </div>

                            <div>
                                <p class="text-xl font-extralight tracking-wider mb-2">Cooldown</p>
                                <p class="text-m text-base-content/70 mb-2">Minutes to wait before notifying about the same interesting aircraft again</p>
                                <input type="number" bind:value={notificationCooldownMinutes} on:input={handleNotificationChange} min="1" step="1" class="input w-20" />
                            </div>

                            <div>
                                <p class="text-xl font-extralight tracking-wider mb-2">Delay</p>
                                <p class="text-m text-base-content/70 mb-2">Seconds to hold a notification while the aircraft's callsign and route arrive. 0 sends immediately.</p>
                                <input type="number" bind:value={notificationDelaySeconds} on:input={handleNotificationChange} min="0" step="1" class="input w-20" />
                            </div>

                            <div class="flex items-center gap-3">
                                <button type="button" class="btn btn-outline" on:click={testNotification} disabled={isTestingNotification}>
                                    {isTestingNotification ? 'Sending...' : 'Send test notification'}
                                </button>
                                {#if testResult}
                                    <span class="text-sm {testResult.ok ? 'text-success' : 'text-error'}">{testResult.message}</span>
                                {/if}
                            </div>
                        </form>

                    {:else if activeMenuItem === 'danger-zone'}
                        <h4 class="text-lg font-semibold mb-6 text-error">Danger Zone</h4>

                        <div class="mb-8">
                            <p class="text-xl font-extralight tracking-wider mb-2">Clear record holders</p>
                            <p class="text-m text-base-content/70 mb-4">
                                Empties the selected leaderboards for every period, including all-time.
                                Archived flight history is not deleted, but the leaderboards only rebuild
                                from aircraft seen after clearing. This cannot be undone.
                            </p>

                            <label class="flex items-center gap-2 mb-3 font-medium">
                                <input
                                    type="checkbox"
                                    class="checkbox checkbox-sm"
                                    checked={allCategoriesSelected}
                                    on:change={(e) => toggleAllCategories(e.target.checked)}
                                />
                                <span class="text-sm">Select all</span>
                            </label>

                            <div class="grid grid-cols-2 gap-2 max-w-md mb-6">
                                {#each recordCategories as category (category.key)}
                                    <label class="flex items-center gap-2">
                                        <input
                                            type="checkbox"
                                            class="checkbox checkbox-sm"
                                            checked={selectedCategories.has(category.key)}
                                            on:change={(e) => toggleCategory(category.key, e.target.checked)}
                                        />
                                        <span class="text-sm">{category.label}</span>
                                    </label>
                                {/each}
                            </div>

                            <div class="flex items-center gap-3">
                                <button
                                    type="button"
                                    class="btn btn-error"
                                    on:click={openClearConfirm}
                                    disabled={selectedCategories.size === 0 || isClearing}
                                >
                                    {isClearing ? 'Clearing...' : 'Clear selected'}
                                </button>
                                {#if clearResult}
                                    <span class="text-sm {clearResult.ok ? 'text-success' : 'text-error'}">{clearResult.message}</span>
                                {/if}
                            </div>
                        </div>

                    {:else if activeMenuItem === 'about'}
                        <div class="text-center mx-auto">
                            <div class="flex items-center justify-center gap-6 mb-2">
                                <img src="/logo_icon.png" alt="Skystats Logo" class="w-32 h-32" />
                                <h1 class="text-7xl font-normal text-primary drop-shadow-[0_0_15px_rgba(59,130,246,0.5)]">
                                    Skystats
                                </h1>
                            </div>
                            <div class="mb-6 text-base-content/50">
                                {#if version.version === "dev"}
                                    {version.version} • {version.commit} • {version.date.toLocaleString()}
                                {:else}
                                    {version.version}
                                {/if}
                            </div>
                            <p class="mt-6 mb-6 text-sm text-base-content/50">
                                Created by <a href="https://github.com/tomcarman" target="_blank" rel="noopener noreferrer" class="text-accent hover:text-primary transition-colors">@tomcarman</a> with support from the SDR Enthusiasts community. Join us on <a href="https://discord.gg/znkBr2eyev" target="_blank" rel="noopener noreferrer" class="text-accent hover:text-primary transition-colors">Discord</a>.
                            </p>
                            <a href="https://github.com/tomcarman/skystats" target="_blank" rel="noopener noreferrer" class="inline-flex items-center gap-2 opacity-50 hover:opacity-100 transition-opacity">
                                <IconBrandGithub stroke={2} size={32} />
                                <span>GitHub</span>
                            </a>
                        </div>
                    {/if}
                </div>

                {#if activeMenuItem === 'display'}
                    <div class="modal-action justify-end">
                        <button
                            class="btn btn-primary"
                            on:click={saveSettings}
                            disabled={!settingsChanged || isSaving}
                        >
                            {isSaving ? 'Saving...' : 'Save'}
                        </button>
                    </div>
                {/if}

                {#if activeMenuItem === 'notifications'}
                    <div class="modal-action justify-end">
                        <button
                            class="btn btn-primary"
                            on:click={saveNotificationSettings}
                            disabled={!notificationsChanged || isSaving}
                        >
                            {isSaving ? 'Saving...' : 'Save'}
                        </button>
                    </div>
                {/if}
            </div>
        </div>
    </div>
</dialog>

<!-- Kept outside the settings dialog so it stacks on top of it rather than inside it. -->
<dialog id="clear-records-modal" class="modal">
    <div class="modal-box">
        <h3 class="text-lg font-bold text-error">Clear record holders?</h3>
        <p class="py-4">
            This permanently deletes the leaderboard entries for the
            {selectedCategories.size} selected categor{selectedCategories.size === 1 ? 'y' : 'ies'},
            across every period. This cannot be undone.
        </p>
        <div class="modal-action">
            <form method="dialog">
                <button class="btn">Cancel</button>
            </form>
            <button class="btn btn-error" on:click={clearSelectedRecords}>Yes, clear</button>
        </div>
    </div>
</dialog>
