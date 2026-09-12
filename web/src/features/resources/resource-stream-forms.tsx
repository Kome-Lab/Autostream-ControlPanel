"use client";

import { useState } from "react";
import { Textarea } from "@/components/ui/textarea";
import { buildDiscordTargetPresetPayload, validDiscordTargetPreset } from "@/features/resources/discord-target-preset";
import { type SubmitResource, type ResourceRow, noneValue } from "./resource-form-types";
import { rowString, numberValue, compactRecord, rowValue } from "./resource-values";
import { TextField, NumberField, FormActions, SelectField, Field, SwitchField } from "./resource-input-fields";
import { useRegisteredNodeOptions, useOAuthAccountOptions } from "./resource-form-queries";

export function EncoderProfileForm({ disabled, submit, initial, submitLabel }: { disabled: boolean; submit: SubmitResource; initial?: ResourceRow; submitLabel?: string }) {
  const row = initial || {};
  const [name, setName] = useState(() => rowString(row, ["name"]) || "1080p60");
  const [width, setWidth] = useState(() => rowString(row, ["width", "config.width"]) || "1920");
  const [height, setHeight] = useState(() => rowString(row, ["height", "config.height"]) || "1080");
  const [fps, setFps] = useState(() => rowString(row, ["fps", "config.fps"]) || "60");
  const [videoBitrate, setVideoBitrate] = useState(() => rowString(row, ["video_bitrate_kbps", "bitrate_kbps", "config.video_bitrate_kbps", "config.bitrate_kbps"]) || "8000");
  const [audioBitrate, setAudioBitrate] = useState(() => rowString(row, ["audio_bitrate_kbps", "config.audio_bitrate_kbps"]) || "192");

  return (
    <form
      className="space-y-3"
      onSubmit={(event) => {
        event.preventDefault();
        submit({
          name,
          config: {
            width: numberValue(width, 1920),
            height: numberValue(height, 1080),
            fps: numberValue(fps, 60),
            video_bitrate_kbps: numberValue(videoBitrate, 8000),
            audio_bitrate_kbps: numberValue(audioBitrate, 192),
          },
        });
      }}
    >
      <div className="grid gap-3 md:grid-cols-2">
        <TextField label="プロファイル名" value={name} onChange={setName} required />
        <NumberField label="映像ビットレート (kbps)" value={videoBitrate} onChange={setVideoBitrate} min={1} required />
        <NumberField label="横解像度" value={width} onChange={setWidth} min={1} required />
        <NumberField label="縦解像度" value={height} onChange={setHeight} min={1} required />
        <NumberField label="フレームレート" value={fps} onChange={setFps} min={1} required />
        <NumberField label="音声ビットレート (kbps)" value={audioBitrate} onChange={setAudioBitrate} min={1} required />
      </div>
      <FormActions label={submitLabel} disabled={disabled} />
    </form>
  );
}

export function DiscordConfigForm({ disabled, submit, initial, submitLabel }: { disabled: boolean; submit: SubmitResource; initial?: ResourceRow; submitLabel?: string }) {
  const row = initial || {};
  const discordNodes = useRegisteredNodeOptions("discord_bot");
  const [name, setName] = useState(() => rowString(row, ["name"]) || "main-discord-bot");
  const [serviceID, setServiceID] = useState(() => rowString(row, ["service_id", "config.service_id"]) || noneValue);
  const [botToken, setBotToken] = useState("");
  const [reconnectMaxAttempts, setReconnectMaxAttempts] = useState(() => rowString(row, ["reconnect_max_attempts", "config.reconnect_max_attempts"]) || "5");
  const effectiveServiceID = serviceID === noneValue && discordNodes[0]?.value ? discordNodes[0].value : serviceID;

  return (
    <form
      className="space-y-3"
      onSubmit={(event) => {
        event.preventDefault();
        submit(
          compactRecord({
            name,
            service_id: effectiveServiceID === noneValue ? "" : effectiveServiceID,
            bot_token: botToken,
            reconnect_enabled: true,
            reconnect_max_attempts: numberValue(reconnectMaxAttempts, 5),
            reconnect_base_delay: "2s",
            reconnect_max_delay: "30s",
            audio_forward_enabled: true,
          }),
          botToken ? { onSensitiveDispatched: () => setBotToken("") } : undefined,
        );
      }}
    >
      <div className="grid gap-3 md:grid-cols-2">
        <TextField label="BOT設定名" value={name} onChange={setName} required />
        <SelectField
          key={discordNodes.map((node) => node.value).join("|") || "no-discord-nodes"}
          label="Discord BOT Node"
          value={effectiveServiceID}
          onChange={setServiceID}
          options={[{ value: noneValue, label: "未選択" }, ...discordNodes]}
        />
        <TextField label="Bot Token" value={botToken} onChange={setBotToken} type="password" description="入力した場合のみ保存します。" />
        <NumberField label="再接続最大回数" value={reconnectMaxAttempts} onChange={setReconnectMaxAttempts} min={0} />
      </div>
      {discordNodes.length === 0 ? <p className="text-sm text-muted-foreground">先にNode登録でDiscord Bot Nodeを登録してください。</p> : null}
      <div className="rounded-md border bg-muted/30 px-3 py-2 text-sm text-muted-foreground">音声転送と自動再接続は常に有効です。</div>
      <FormActions label={submitLabel} disabled={disabled || effectiveServiceID === noneValue} />
    </form>
  );
}

export function DiscordTargetPresetForm({ disabled, submit, initial, submitLabel }: { disabled: boolean; submit: SubmitResource; initial?: ResourceRow; submitLabel?: string }) {
  const row = initial || {};
  const [name, setName] = useState(() => rowString(row, ["name"]) || "main-target");
  const [guildID, setGuildID] = useState(() => rowString(row, ["guild_id"]));
  const [textChannelID, setTextChannelID] = useState(() => rowString(row, ["text_channel_id"]));
  const [voiceChannelID, setVoiceChannelID] = useState(() => rowString(row, ["voice_channel_id"]));
  const input = { name, guildID, textChannelID, voiceChannelID, ...(initial ? { revision: numberValue(rowString(row, ["revision"]), 0) } : {}) };
  const ready = validDiscordTargetPreset(input);

  return (
    <form
      className="space-y-3"
      onSubmit={(event) => {
        event.preventDefault();
        if (!ready) return;
        submit(buildDiscordTargetPresetPayload(input));
      }}
    >
      <div className="grid gap-3 md:grid-cols-2">
        <TextField label="プリセット名" value={name} onChange={setName} required />
        <TextField label="DiscordサーバーID" value={guildID} onChange={setGuildID} required />
        <TextField label="チャットチャンネルID" value={textChannelID} onChange={setTextChannelID} required />
        <TextField label="ボイスチャンネルID" value={voiceChannelID} onChange={setVoiceChannelID} required />
      </div>
      {!ready ? <p role="status" className="text-sm text-amber-700 dark:text-amber-300">名前と、32桁以内の数字だけで構成された3つのDiscord IDを入力してください。</p> : null}
      <FormActions label={submitLabel} disabled={disabled || !ready} />
    </form>
  );
}

export function YouTubeOutputForm({ disabled, submit, initial, submitLabel }: { disabled: boolean; submit: SubmitResource; initial?: ResourceRow; submitLabel?: string }) {
  const row = initial || {};
  const oauthAccounts = useOAuthAccountOptions("youtube");
  const [name, setName] = useState(() => rowString(row, ["name"]) || "public-live");
  const [mode, setMode] = useState(() => rowString(row, ["mode", "config.mode"]) || "live_api");
  const [rtmpURL, setRTMPURL] = useState(() => rowString(row, ["rtmp_url", "config.rtmp_url"]) || "rtmps://a.rtmps.youtube.com/live2");
  const [streamKey, setStreamKey] = useState("");
  const [watchURL, setWatchURL] = useState(() => rowString(row, ["watch_url", "config.watch_url"]));
  const [oauthAccountID, setOAuthAccountID] = useState(() => rowString(row, ["oauth_account_id", "config.oauth_account_id"]) || noneValue);
  const [privacyStatus, setPrivacyStatus] = useState(() => rowString(row, ["privacy_status", "config.privacy_status"]) || "public");
  const [latencyPreference, setLatencyPreference] = useState(() => rowString(row, ["latency_preference", "config.latency_preference"]) || "low");
  const [titleTemplate, setTitleTemplate] = useState(() => rowString(row, ["broadcast_title_template", "title_template", "config.broadcast_title_template", "config.title_template"]) || "{{program_title}}");
  const [description, setDescription] = useState(() => rowString(row, ["broadcast_description", "description", "config.broadcast_description", "config.description"]));
  const [useConfiguredStreamKey, setUseConfiguredStreamKey] = useState(() => rowValue(row, ["use_configured_stream_key", "config.use_configured_stream_key"]) === true);
  const [relayBindingID, setRelayBindingID] = useState(() => rowString(row, ["relay_binding_id", "config.relay_binding_id"]));
  const [reusableLiveStreamID, setReusableLiveStreamID] = useState(() => rowString(row, ["reusable_live_stream_id", "config.reusable_live_stream_id"]));
  const [autoStart, setAutoStart] = useState(() => rowValue(row, ["enable_auto_start", "config.enable_auto_start"]) !== false);
  const [autoStop, setAutoStop] = useState(() => rowValue(row, ["enable_auto_stop", "config.enable_auto_stop"]) !== false);
  const [completeOnStop, setCompleteOnStop] = useState(() => rowValue(row, ["complete_on_stop", "config.complete_on_stop"]) !== false);
  const staticRelayMode = mode === "live_api_relay_static";
  const configuredLiveAPIKeyMode = mode === "live_api" && useConfiguredStreamKey;
  const outputModeOptions = [
    { value: "live_api", label: "YouTube Live API（本番・通常）", description: "通常はこちら。Control Panelが配信枠を作成し、EncoderからYouTubeへ直接送信します。固定Relayは不要です。" },
    { value: "live_api_dry_run", label: "YouTube Live API（検証）", description: "接続確認用です。実際に配信を開始する場合は本番・通常を選択してください。" },
    { value: "stream_key", label: "ストリームキー（従来方式）", description: "既存のストリームキー設定を使う場合だけ選択します。新規設定では本番・通常を推奨します。" },
    ...(staticRelayMode
      ? [{ value: "live_api_relay_static", label: "YouTube Live API（固定Relay・既存互換）", description: "既存の固定Relay設定を編集する場合だけ使用します。新規作成では選択できません。" }]
      : []),
  ];
  const requiresOAuth = mode === "live_api" || mode === "live_api_dry_run" || staticRelayMode;
  const effectiveOAuthAccountID = requiresOAuth && oauthAccountID === noneValue && oauthAccounts[0]?.value ? oauthAccounts[0].value : oauthAccountID;
  const staticRelayReady = relayBindingID.trim() !== "" && reusableLiveStreamID.trim() !== "";

  return (
    <form
      className="space-y-3"
      onSubmit={(event) => {
        event.preventDefault();
        submit(
          compactRecord({
            name,
            mode,
            rtmp_url: staticRelayMode ? "" : rtmpURL,
            stream_key: staticRelayMode ? "" : streamKey,
            relay_binding_id: staticRelayMode ? relayBindingID.trim() : "",
            reusable_live_stream_id: staticRelayMode ? reusableLiveStreamID.trim() : "",
            // A profile edited from stream_key can retain this state even though
            // the field is hidden in static-relay mode. Do not submit a stale
            // watch URL: the server correctly rejects ingest-adjacent fields
            // for the non-secret relay binding flow.
            watch_url: staticRelayMode ? "" : watchURL,
            oauth_account_id: effectiveOAuthAccountID === noneValue ? "" : effectiveOAuthAccountID,
            use_configured_stream_key: configuredLiveAPIKeyMode,
            broadcast_title_template: titleTemplate,
            broadcast_description: description,
            privacy_status: privacyStatus,
            latency_preference: latencyPreference,
            enable_auto_start: autoStart,
            enable_auto_stop: autoStop,
            complete_on_stop: staticRelayMode ? true : completeOnStop,
          }),
          streamKey ? { onSensitiveDispatched: () => setStreamKey("") } : undefined,
        );
      }}
    >
      <div className="grid gap-3 md:grid-cols-2">
        <TextField label="出力名" value={name} onChange={setName} required />
        <SelectField
          label="出力方式"
          value={mode}
          onChange={setMode}
          options={outputModeOptions}
        />
        {!staticRelayMode ? <TextField label="RTMP URL" value={rtmpURL} onChange={setRTMPURL} required /> : null}
        <SelectField label="接続済みGoogleアカウント" value={effectiveOAuthAccountID} onChange={setOAuthAccountID} options={[{ value: noneValue, label: "未選択" }, ...oauthAccounts]} />
        {staticRelayMode ? (
          <>
            <TextField label="固定RelayバインディングID" value={relayBindingID} onChange={setRelayBindingID} description="管理済みの固定Relayを識別する非秘密IDです。配信キーは入力しません。" required />
            <TextField label="再利用するYouTube Live Stream ID" value={reusableLiveStreamID} onChange={setReusableLiveStreamID} description="固定Relayが配信する既存のYouTube Live Stream IDを入力します。URLやストリームキーは入力しません。" required />
            <div className="rounded-md border bg-muted/30 px-3 py-2 text-sm text-muted-foreground md:col-span-2">
              <div className="font-medium text-foreground">固定Relayの同時配信制約</div>
              <p className="mt-1">1つの固定Relayは同時に1配信枠だけを処理できます。Relayの設定と同じバインディングID・Live Stream IDを指定してください。</p>
            </div>
          </>
        ) : mode === "stream_key" || configuredLiveAPIKeyMode ? (
          <TextField
            label="ストリームキー"
            value={streamKey}
            onChange={setStreamKey}
            type="password"
            description={configuredLiveAPIKeyMode
              ? "YouTube Studioで作成した再利用可能なカスタムキーを入力します。保存済みなら空欄のままで構いません。"
              : "既存ストリームキー方式で使うキーを入力します。"}
          />
        ) : null}
        {mode === "stream_key" ? <TextField label="YouTube視聴URL" value={watchURL} onChange={setWatchURL} placeholder="https://www.youtube.com/watch?v=..." description="配信開始時のDiscord通知に使用します。" required /> : null}
        <TextField label="番組タイトルテンプレート" value={titleTemplate} onChange={setTitleTemplate} />
        <SelectField
          label="公開範囲"
          value={privacyStatus}
          onChange={setPrivacyStatus}
          options={[
            { value: "public", label: "公開" },
            { value: "unlisted", label: "限定公開" },
            { value: "private", label: "非公開" },
          ]}
        />
        <SelectField
          label="遅延設定"
          value={latencyPreference}
          onChange={setLatencyPreference}
          options={[
            { value: "normal", label: "標準" },
            { value: "low", label: "低遅延" },
            { value: "ultra_low", label: "超低遅延" },
          ]}
        />
      </div>
      <Field label="説明">
        <Textarea value={description} onChange={(event) => setDescription(event.target.value)} className="min-h-24" />
      </Field>
      <div className="grid gap-3 md:grid-cols-3">
        {mode === "live_api" ? (
          <SwitchField label="Studioのカスタムキーを固定利用" checked={useConfiguredStreamKey} onCheckedChange={setUseConfiguredStreamKey} />
        ) : null}
        <SwitchField label="自動開始" checked={autoStart} onCheckedChange={setAutoStart} />
        <SwitchField label="自動停止" checked={autoStop} onCheckedChange={setAutoStop} />
        {staticRelayMode ? (
          <div className="rounded-md border bg-muted/30 px-3 py-2 text-sm">
            <div className="font-medium">停止時に完了扱い: 常に有効</div>
            <p className="mt-1 text-xs text-muted-foreground">固定Relayモードでは停止時にYouTube配信を必ず完了扱いにします。</p>
          </div>
        ) : <SwitchField label="停止時に完了扱い" checked={completeOnStop} onCheckedChange={setCompleteOnStop} />}
      </div>
      {configuredLiveAPIKeyMode ? (
        <div className="rounded-md border bg-muted/30 px-3 py-2 text-sm text-muted-foreground">
          YouTube Studioでカスタムキーの解像度を手動1080p60、デュアルストリームをOFFに設定してください。1つのキーを同時に複数枠へ割り当てることはできません。Control Panelは配信枠を自動作成し、このキーのLiveStreamへバインドします。
        </div>
      ) : null}
      {requiresOAuth && oauthAccounts.length === 0 ? <p className="text-sm text-muted-foreground">YouTube Live APIを使うには、YouTube Live用途でGoogleアカウントを接続してください。</p> : null}
      <FormActions label={submitLabel} disabled={disabled || (requiresOAuth && effectiveOAuthAccountID === noneValue) || (staticRelayMode && !staticRelayReady)} />
    </form>
  );
}
