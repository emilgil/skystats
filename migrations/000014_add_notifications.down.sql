DELETE FROM user_settings WHERE setting_key IN (
    'notifications_enabled','apprise_api_url','apprise_config_key',
    'notify_group_mil','notify_group_gov','notify_group_pol','notify_group_civ',
    'notification_cooldown_minutes',
    'notify_record_fastest','notify_record_slowest','notify_record_highest','notify_record_lowest',
    'notify_record_furthest_flown','notify_record_longest_route','notify_record_most_remaining'
);

DROP TABLE IF EXISTS notification_log;
