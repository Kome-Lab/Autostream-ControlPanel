-- Discord VC auto-start and auto-stop are one paired capability. Older
-- runtime tokens issued with streams.start only otherwise receive 403
-- missing_service_scope on the first empty-VC stop, leaving the stream and
-- Bot state out of sync. Grant only the paired stop scope to existing Discord
-- Bot tokens that already hold streams.start; no other service type changes.
UPDATE service_tokens
SET scopes = JSON_ARRAY_APPEND(scopes, '$', 'streams.stop')
WHERE service_type = 'discord_bot'
  AND JSON_CONTAINS(scopes, JSON_QUOTE('streams.start'))
  AND NOT JSON_CONTAINS(scopes, JSON_QUOTE('streams.stop'));
