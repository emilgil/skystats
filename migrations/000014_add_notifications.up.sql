CREATE TABLE notification_log (
    id           SERIAL PRIMARY KEY,
    kind         TEXT NOT NULL,          -- 'interesting' | 'record'
    category     TEXT,                   -- interesting: Mil/Gov/Pol/Civ ; record: highest/fastest/...
    icao         TEXT,                   -- aircraft hex
    first_seen   TIMESTAMPTZ,            -- record dedupe: identifies the flight-session
    metric_value DOUBLE PRECISION,       -- record: the value that set the record
    title        TEXT,
    body         TEXT,
    target       TEXT,                   -- apprise config key used
    status       TEXT NOT NULL,          -- 'sent' | 'failed'
    http_status  INTEGER,
    error        TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_notification_log_icao_created ON notification_log (icao, created_at);
CREATE INDEX idx_notification_log_record_dedupe ON notification_log (kind, category, icao, first_seen);

INSERT INTO user_settings (setting_key, setting_value, description) VALUES
    ('notifications_enabled',        'false', 'Master on/off for Apprise notifications'),
    ('apprise_api_url',              '',      'Base URL of the Apprise API server'),
    ('apprise_config_key',           '',      'Saved config key on the Apprise server'),
    ('notify_group_mil',             'true',  'Notify for military aircraft'),
    ('notify_group_gov',             'true',  'Notify for government aircraft'),
    ('notify_group_pol',             'true',  'Notify for police aircraft'),
    ('notify_group_civ',             'true',  'Notify for civilian (interesting) aircraft'),
    ('notification_cooldown_minutes','60',    'Per-ICAO cooldown between interesting notifications'),
    ('notify_record_fastest',        'true',  'Notify on new all-time fastest'),
    ('notify_record_slowest',        'true',  'Notify on new all-time slowest'),
    ('notify_record_highest',        'true',  'Notify on new all-time highest'),
    ('notify_record_lowest',         'true',  'Notify on new all-time lowest'),
    ('notify_record_furthest_flown', 'true',  'Notify on new all-time furthest flown'),
    ('notify_record_longest_route',  'true',  'Notify on new all-time longest route'),
    ('notify_record_most_remaining', 'true',  'Notify on new all-time most remaining')
ON CONFLICT (setting_key) DO NOTHING;
