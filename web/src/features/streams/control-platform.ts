export type DiscordTargetMode = "inherit" | "preset" | "manual";

export type DiscordTargetSelection = {
  mode: DiscordTargetMode;
  presetID?: string;
  presetRevision?: number;
  guildID?: string;
  textChannelID?: string;
  voiceChannelID?: string;
};

export type StreamVisualSettings = {
  cover_candidates?: CoverCandidate[];
  stream_id: string;
  background_mode: "default" | "image";
  background_asset_id?: string;
  background_variant_id?: string;
  header_title_mode: "default" | "custom";
  header_title_value?: string;
  discord_target_mode?: DiscordTargetMode;
  discord_target_preset_id?: string;
  discord_target_preset_revision?: number;
  discord_snapshot_revision: number;
  discord_guild_id?: string;
  discord_text_channel_id?: string;
  discord_voice_channel_id?: string;
  discord_preset_deleted?: boolean;
  cover_source: "none" | "preset" | "upload";
  cover_preset_id?: string;
  cover_preset_revision?: number;
  cover_asset_id?: string;
  cover_variant_id?: string;
  cover_start_active: boolean;
  revision: number;
};

export type CoverCandidate = { name: string; source: "preset" | "upload"; preset_id?: string; preset_revision?: number; asset_id: string; variant_id: string };

export type VideoCoverState = {
  stream_id: string;
  job_generation: number;
  desired_active: boolean;
  desired_revision: number;
  applied_active: boolean | null;
  applied_revision: number | null;
  asset_variant_id?: string;
  applied_asset_variant_id?: string;
  last_error_code?: string;
  status: "idle" | "confirming" | "applied" | "failed";
  pipeline_order: readonly string[];
  cover_watermark_independent: boolean;
};

export function buildDiscordTargetPayload(selection: DiscordTargetSelection): Record<string, unknown> {
  if (selection.mode === "inherit") return { discord_target: { mode: "inherit" } };
  if (selection.mode === "preset") {
    return {
      discord_target: {
        mode: "preset",
        preset_id: selection.presetID?.trim() || "",
        preset_revision: selection.presetRevision || 0,
      },
    };
  }
  return {
    discord_target: {
      mode: "manual",
      guild_id: selection.guildID?.trim() || "",
      text_channel_id: selection.textChannelID?.trim() || "",
      voice_channel_id: selection.voiceChannelID?.trim() || "",
    },
  };
}

export function buildVisualUpdate(
  currentRevision: number,
  fields: Record<string, unknown>,
  uploadSessionID?: string,
) {
  return {
    expected_revision: currentRevision,
    ...(uploadSessionID ? { upload_session_id: uploadSessionID } : {}),
    ...fields,
  };
}

export function visualPresentation(settings: StreamVisualSettings | undefined, streamName: string, locale: "ja" | "en" = "ja") {
  if (!settings) return { title: streamName, background: locale === "ja" ? "既定" : "Default", discord: locale === "ja" ? "従来設定" : "Legacy settings", cover: "OFF", warning: "" };
  const title = settings.header_title_mode === "custom" ? settings.header_title_value?.trim() || streamName : streamName;
  const discord = settings.discord_target_mode === "preset"
    ? `${locale === "ja" ? "プリセット" : "Preset"} snapshot r${settings.discord_target_preset_revision || 0}`
    : settings.discord_target_mode === "manual" ? (locale === "ja" ? "手動 snapshot" : "Manual snapshot") : (locale === "ja" ? "継承" : "Inherited");
  return {
    title,
    background: settings.background_mode === "image" ? (locale === "ja" ? "カスタム背景（cover / center crop）" : "Custom background (cover / center crop)") : (locale === "ja" ? "既定" : "Default"),
    discord,
    cover: settings.cover_source === "none" ? "OFF" : `${settings.cover_source === "preset" ? (locale === "ja" ? "プリセット" : "Preset") : (locale === "ja" ? "アップロード" : "Upload")}${settings.cover_start_active ? (locale === "ja" ? " / 開始時ON" : " / ON at start") : (locale === "ja" ? " / 開始時OFF" : " / OFF at start")}`,
    warning: settings.discord_preset_deleted ? (locale === "ja" ? "元のDiscordプリセットは削除済みです。保存済みsnapshotを継続します。" : "The original Discord preset was deleted; the saved snapshot remains active.") : "",
  };
}

export type CoverPermissionSnapshot = {
  status: "loading" | "ready" | "error";
  permissions: readonly string[];
};

export type CoverCapabilitySnapshot = {
  status: "loading" | "ready" | "error";
  supported: boolean;
};

export function coverActionAvailability(
  active: boolean,
  permission: CoverPermissionSnapshot,
  pending: boolean,
  capability: CoverCapabilitySnapshot = { status: "ready", supported: true },
  unresolved = false,
) {
  const required = active ? "streams.show_cover" : "streams.hide_cover";
  if (permission.status !== "ready") return { allowed: false, required, reason: "権限を確認できるまで操作できません。", confirmationRequired: !active };
  if (!permission.permissions.includes("*") && !permission.permissions.includes(required)) {
    return { allowed: false, required, reason: `この操作には ${required} 権限が必要です。`, confirmationRequired: !active };
  }
  if (capability.status !== "ready") return { allowed: false, required, reason: "Encoder capabilityを確認できるまで操作できません。", confirmationRequired: !active };
  if (!capability.supported) return { allowed: false, required, reason: "割当済みEncoderが live_video_cover_v1 を報告していません。", confirmationRequired: !active };
  if (pending) return { allowed: false, required, reason: "同じ操作を処理中です。", confirmationRequired: !active };
  if (unresolved) return { allowed: false, required, reason: "前回結果が未確定です。再送せず最新状態を確認してください。", confirmationRequired: !active };
  return { allowed: true, required, reason: "", confirmationRequired: !active };
}

export function buildVideoCoverAction(active: boolean, state: VideoCoverState, idempotencyKey: string, hideConfirmed = false) {
  return {
    active,
    expected_job_generation: state.job_generation,
    expected_revision: state.desired_revision,
    idempotency_key: idempotencyKey,
    ...(active ? {} : { hide_confirmed: hideConfirmed }),
  };
}

export type VideoCoverActionRequest = ReturnType<typeof buildVideoCoverAction> & { candidate_variant_id?: string; expected_visual_revision?: number };
export type VideoCoverAuthorityProof = Readonly<{ permission: CoverPermissionSnapshot; capability: CoverCapabilitySnapshot; valid: boolean; reason?: string }>;
export type VideoCoverAuthorityBaseline = Readonly<{ streamID: string; streamStatus: string; encoderID: string; encoderAuthority: string; visualRevision: number }>;

export function newVideoCoverIdempotencyKey(random: Pick<Crypto, "getRandomValues" | "randomUUID"> = globalThis.crypto) {
  if (typeof random?.randomUUID === "function") return random.randomUUID();
  if (typeof random?.getRandomValues !== "function") throw new Error("video_cover_secure_random_unavailable");
  const bytes = random.getRandomValues(new Uint8Array(16));
  bytes[6] = (bytes[6] & 0x0f) | 0x40;
  bytes[8] = (bytes[8] & 0x3f) | 0x80;
  const hex = [...bytes].map((value) => value.toString(16).padStart(2, "0"));
  return `${hex.slice(0, 4).join("")}-${hex.slice(4, 6).join("")}-${hex.slice(6, 8).join("")}-${hex.slice(8, 10).join("")}-${hex.slice(10).join("")}`;
}

export function createVideoCoverActionController<T>(
	permission: () => CoverPermissionSnapshot,
	send: (request: VideoCoverActionRequest, signal?: AbortSignal) => Promise<T>,
	capability: () => CoverCapabilitySnapshot = () => ({ status: "ready", supported: true }),
	revalidate?: (request: VideoCoverActionRequest, signal: AbortSignal, baseline?: VideoCoverAuthorityBaseline) => Promise<VideoCoverAuthorityProof>,
) {
	let pending = false;
	let unresolved = false;
	let uncertainIssued = false;
	let accepted: VideoCoverState | undefined;
	let active: { abort: AbortController; sent: boolean } | undefined;
	const issueRequest = async (request: VideoCoverActionRequest, baseline?: VideoCoverAuthorityBaseline) => {
		const availability = coverActionAvailability(request.active, permission(), pending, capability(), unresolved);
		if (!availability.allowed || (!request.active && request.hide_confirmed !== true)) {
			return { sent: false as const, reason: availability.reason || "確認が必要です。" };
		}
		pending = true;
		const operation = { abort: new AbortController(), sent: false };
		active = operation;
		try {
			if (revalidate) {
				let proof: VideoCoverAuthorityProof;
				try { proof = await revalidate(request, operation.abort.signal, baseline); }
				catch { return { sent: false as const, reason: "最新の権限または配信状態を確認できないため、操作を送信しませんでした。" }; }
				const fresh = coverActionAvailability(request.active, proof.permission, false, proof.capability, unresolved);
				if (!fresh.allowed || !proof.valid) return { sent: false as const, reason: fresh.reason || proof.reason || "対象の設定または担当Encoderが変更されました。最新状態を確認してください。" };
			}
			if (operation.abort.signal.aborted) return { sent: false as const, reason: "最新の権限または配信状態を確認できないため、操作を送信しませんでした。" };
			try {
				operation.sent = true;
				return { sent: true as const, value: await send(request, operation.abort.signal) };
			} catch (error) {
				uncertainIssued = true;
				unresolved = true;
				accepted = undefined;
				throw error;
			}
		} finally {
			pending = false;
			if (active === operation) active = undefined;
		}
	};
	return {
		get pending() { return pending; },
		get unresolved() { return unresolved; },
		cancel() { if (active && !active.sent) active.abort.abort(); },
		holdForReconciliation(state?: VideoCoverState) { unresolved = true; if (!state) { uncertainIssued = true; accepted = undefined; } else if (!uncertainIssued) accepted = { ...state }; },
		reconcile(state?: VideoCoverState) {
			if (!unresolved) return true;
			if (uncertainIssued || !accepted || !state || !Number.isSafeInteger(state.job_generation) || state.job_generation < 0 || !Number.isSafeInteger(state.desired_revision) || state.desired_revision < 0 || state.stream_id !== accepted.stream_id || state.job_generation !== accepted.job_generation || state.desired_revision !== accepted.desired_revision || state.desired_active !== accepted.desired_active || (state.asset_variant_id || "") !== (accepted.asset_variant_id || "")) return false;
			const applied = state.status === "applied" && state.applied_revision === state.desired_revision && state.applied_active === state.desired_active && (!state.desired_active || state.applied_asset_variant_id === state.asset_variant_id);
			if (!applied && state.status !== "failed") return false;
			unresolved = false;
			accepted = undefined;
			return true;
		},
		issueRequest,
		async issue(active: boolean, state: VideoCoverState, idempotencyKey: string, hideConfirmed = false) {
			return issueRequest(buildVideoCoverAction(active, state, idempotencyKey, hideConfirmed));
		},
	};
}
