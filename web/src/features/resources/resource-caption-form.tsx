"use client";

import { useState } from "react";
import { type SubmitResource, type ResourceRow } from "./resource-form-types";
import { rowString, normalizeDeepgramLanguage, rowBoolean, numberValue } from "./resource-values";
import { TextField, SelectField, Field, NumberField, SwitchField, FormActions } from "./resource-input-fields";

export function CaptionProfileForm({ disabled, submit, initial, submitLabel }: { disabled: boolean; submit: SubmitResource; initial?: ResourceRow; submitLabel?: string }) {
  const row = initial || {};
  const [name, setName] = useState(() => rowString(row, ["name"]) || "日本語ライブ字幕");
  const [language, setLanguage] = useState(() => normalizeDeepgramLanguage(rowString(row, ["language", "config.language"]) || "ja"));
  const [delayMs, setDelayMs] = useState(() => rowString(row, ["delay_ms", "config.delay_ms"]) || "800");
  const [endpointingMs, setEndpointingMs] = useState(() => rowString(row, ["endpointing_ms", "config.endpointing_ms"]) || "300");
  const [utteranceEndMs, setUtteranceEndMs] = useState(() => rowString(row, ["utterance_end_ms", "config.utterance_end_ms"]) || "1000");
  const [localFinalizeMs, setLocalFinalizeMs] = useState(() => rowString(row, ["local_finalize_ms", "config.local_finalize_ms"]) || "1500");
  const [speakerIdleCloseSeconds, setSpeakerIdleCloseSeconds] = useState(() => rowString(row, ["speaker_idle_close_seconds", "config.speaker_idle_close_seconds"]) || "8");
  const [keepaliveIntervalSeconds, setKeepaliveIntervalSeconds] = useState(() => rowString(row, ["keepalive_interval_seconds", "config.keepalive_interval_seconds"]) || "4");
  const [replayBufferMaxMs, setReplayBufferMaxMs] = useState(() => rowString(row, ["replay_buffer_max_ms", "config.replay_buffer_max_ms"]) || "2000");
  const [captionAudioFlushMs, setCaptionAudioFlushMs] = useState(() => rowString(row, ["caption_audio_flush_ms", "config.caption_audio_flush_ms"]) || "100");
  const [captionAudioMaxBatchPackets, setCaptionAudioMaxBatchPackets] = useState(() => rowString(row, ["caption_audio_max_batch_packets", "config.caption_audio_max_batch_packets"]) || "5");
  const [unresolvedSSRCBufferMs, setUnresolvedSSRCBufferMs] = useState(() => rowString(row, ["unresolved_ssrc_buffer_ms", "config.unresolved_ssrc_buffer_ms"]) || "1000");
  const [conversationMaxItems, setConversationMaxItems] = useState(() => rowString(row, ["conversation_max_items", "config.conversation_max_items"]) || "12");
  const [conversationReorderWindowMs, setConversationReorderWindowMs] = useState(() => rowString(row, ["conversation_reorder_window_ms", "config.conversation_reorder_window_ms"]) || "500");
  const [voiceInterimTTLSeconds, setVoiceInterimTTLSeconds] = useState(() => rowString(row, ["voice_interim_ttl_seconds", "config.voice_interim_ttl_seconds"]) || "6");
  const [voiceFinalTTLSeconds, setVoiceFinalTTLSeconds] = useState(() => rowString(row, ["voice_final_ttl_seconds", "config.voice_final_ttl_seconds"]) || "15");
  const [interimResults, setInterimResults] = useState(() => rowBoolean(row, ["interim_results", "config.interim_results"], true));
  const [smartFormat, setSmartFormat] = useState(() => rowBoolean(row, ["smart_format", "config.smart_format"], true));
  const [showVoiceTranscripts, setShowVoiceTranscripts] = useState(() => rowBoolean(row, ["show_voice_transcripts", "config.show_voice_transcripts"], true));
  const [showLegacyCaptionBar, setShowLegacyCaptionBar] = useState(() => rowBoolean(row, ["show_legacy_caption_bar", "config.show_legacy_caption_bar"], false));
  const [apiKey, setAPIKey] = useState("");

  return (
    <form
      className="space-y-3"
      onSubmit={(event) => {
        event.preventDefault();
        submit(
          {
            name,
            config: {
              provider: "deepgram",
              model: "nova-3",
              language,
              api_key_secret_name: "deepgram_api_key",
              endpointing_ms: numberValue(endpointingMs, 300),
              utterance_end_ms: numberValue(utteranceEndMs, 1000),
              local_finalize_ms: numberValue(localFinalizeMs, 1500),
              speaker_idle_close_seconds: numberValue(speakerIdleCloseSeconds, 8),
              keepalive_interval_seconds: numberValue(keepaliveIntervalSeconds, 4),
              interim_results: interimResults,
              smart_format: smartFormat,
              replay_buffer_max_ms: numberValue(replayBufferMaxMs, 2000),
              delay_ms: numberValue(delayMs, 800),
              caption_audio_flush_ms: numberValue(captionAudioFlushMs, 100),
              caption_audio_max_batch_packets: numberValue(captionAudioMaxBatchPackets, 5),
              unresolved_ssrc_buffer_ms: numberValue(unresolvedSSRCBufferMs, 1000),
              conversation_max_items: numberValue(conversationMaxItems, 12),
              conversation_reorder_window_ms: numberValue(conversationReorderWindowMs, 500),
              voice_interim_ttl_seconds: numberValue(voiceInterimTTLSeconds, 6),
              voice_final_ttl_seconds: numberValue(voiceFinalTTLSeconds, 15),
              show_voice_transcripts: showVoiceTranscripts,
              show_legacy_caption_bar: showLegacyCaptionBar,
            },
          },
          apiKey.trim() ? { secretValue: apiKey.trim(), onSensitiveDispatched: () => setAPIKey("") } : undefined,
        );
      }}
    >
      <div className="grid gap-3 md:grid-cols-2">
        <TextField label="プロファイル名" value={name} onChange={setName} required />
        <SelectField
          label="言語"
          value={language}
          onChange={setLanguage}
          options={[
            { value: "ja", label: "日本語" },
            { value: "en", label: "英語" },
          ]}
        />
        <Field label="音声認識モデル"><div className="rounded-md border bg-background px-3 py-2 text-sm">Deepgram Nova-3</div></Field>
        <TextField label="Deepgram APIキー" value={apiKey} onChange={setAPIKey} type="password" placeholder={initial ? "変更しない場合は空欄" : "既に設定済みの場合は空欄"} description="入力したキーは暗号化シークレットとして保存し、Workerだけが配信開始時に取得します。" />
        <NumberField label="発話確定までの待機 (ms)" value={endpointingMs} onChange={setEndpointingMs} min={10} required />
        <NumberField label="遅延補正 (ms)" value={delayMs} onChange={setDelayMs} min={0} />
        <NumberField label="Utterance end (ms)" value={utteranceEndMs} onChange={setUtteranceEndMs} min={100} />
        <NumberField label="Local finalize (ms)" value={localFinalizeMs} onChange={setLocalFinalizeMs} min={0} />
        <NumberField label="Speaker idle close (sec)" value={speakerIdleCloseSeconds} onChange={setSpeakerIdleCloseSeconds} min={1} />
        <NumberField label="Keepalive interval (sec)" value={keepaliveIntervalSeconds} onChange={setKeepaliveIntervalSeconds} min={1} />
        <NumberField label="Replay buffer (ms)" value={replayBufferMaxMs} onChange={setReplayBufferMaxMs} min={0} />
        <NumberField label="Caption audio flush (ms)" value={captionAudioFlushMs} onChange={setCaptionAudioFlushMs} min={10} />
        <NumberField label="Caption audio max packets" value={captionAudioMaxBatchPackets} onChange={setCaptionAudioMaxBatchPackets} min={1} />
        <NumberField label="Unresolved SSRC buffer (ms)" value={unresolvedSSRCBufferMs} onChange={setUnresolvedSSRCBufferMs} min={0} />
        <NumberField label="Conversation max items" value={conversationMaxItems} onChange={setConversationMaxItems} min={1} />
        <NumberField label="Conversation reorder window (ms)" value={conversationReorderWindowMs} onChange={setConversationReorderWindowMs} min={0} />
        <NumberField label="Voice interim TTL (sec)" value={voiceInterimTTLSeconds} onChange={setVoiceInterimTTLSeconds} min={1} />
        <NumberField label="Voice final TTL (sec)" value={voiceFinalTTLSeconds} onChange={setVoiceFinalTTLSeconds} min={1} />
      </div>
      <div className="grid gap-3 md:grid-cols-2">
        <SwitchField label="途中結果を表示" checked={interimResults} onCheckedChange={setInterimResults} />
        <SwitchField label="読みやすく整形" checked={smartFormat} onCheckedChange={setSmartFormat} />
        <SwitchField label="Show voice transcripts" checked={showVoiceTranscripts} onCheckedChange={setShowVoiceTranscripts} />
        <SwitchField label="Show legacy caption bar" checked={showLegacyCaptionBar} onCheckedChange={setShowLegacyCaptionBar} />
      </div>
      <FormActions label={submitLabel} disabled={disabled} />
    </form>
  );
}
