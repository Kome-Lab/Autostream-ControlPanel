UPDATE service_tokens
SET scopes = JSON_ARRAY_APPEND(scopes, '$', 'notifications.email.send')
WHERE service_type = 'observability'
  AND revoked_at IS NULL
  AND JSON_CONTAINS(scopes, JSON_QUOTE('notifications.email.send')) = 0;
