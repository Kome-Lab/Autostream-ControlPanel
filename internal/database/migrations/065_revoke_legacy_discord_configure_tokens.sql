-- `streams.stop` is paired with `streams.start` for Discord VC auto-stop.
-- A Configure Token issued before that scope existed has no durable issuer or
-- scope snapshot. Revoke only those one-time tokens so the operator must issue
-- a fresh Configure Token after this release; active Node Runtime Tokens are
-- deliberately untouched.
UPDATE services AS s
JOIN service_tokens AS t ON t.id = s.token_id
SET s.configure_token_hash = NULL,
    s.configure_token_expires_at = NULL,
    s.updated_at = UTC_TIMESTAMP(6)
WHERE s.service_type = 'discord_bot'
  AND s.configure_token_hash IS NOT NULL
  AND s.configure_token_used_at IS NULL
  AND JSON_CONTAINS(t.scopes, JSON_QUOTE('streams.start'))
  AND NOT JSON_CONTAINS(t.scopes, JSON_QUOTE('streams.stop'));
