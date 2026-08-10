const staticRelayMode = "live_api_relay_static";
const recoveryEligibleStatuses = new Set(["failed", "completed"]);

export function staticRelayRecoveryActionAvailable(outputMode: unknown, streamStatus: unknown) {
  return outputMode === staticRelayMode && recoveryEligibleStatuses.has(String(streamStatus || "").toLowerCase());
}

export function staticRelayRecoveryConfirmation() {
  return { confirm_external_cleanup: true };
}

export function staticRelayRecoveryErrorMessage(code: string) {
  const messages: Record<string, string> = {
    youtube_relay_static_recovery_required: "固定RelayのYouTube配信枠の作成結果を確定できませんでした。対象配信が停止または失敗であることを確認してから、固定Relay回復を実行してください。",
    stream_relay_recovery_not_safe_while_active: "配信中または開始・停止処理中のため、固定Relay回復は実行できません。配信の停止処理が完了してから再試行してください。",
    youtube_relay_static_recovery_not_required: "固定Relayの回復が必要な状態ではありません。配信一覧を更新して状態を確認してください。",
    youtube_relay_static_recovery_not_found: "固定Relayの回復対象は見つかりませんでした。すでに解消済みか、配信一覧の状態が古い可能性があります。",
    youtube_relay_static_recovery_cleanup_failed: "YouTube側の未開始配信枠を安全に削除できませんでした。固定Relayを再利用せず、YouTube Studioで状態を確認してから再試行してください。",
  };
  return messages[code] || "";
}
