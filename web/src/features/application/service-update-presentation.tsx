"use client";

import { Badge } from "@/components/ui/badge";
import { compareSystemUpdateVersions } from "@/lib/system-update-version";
import { formatDateTimeInTimeZone } from "@/lib/timezone";
import type { AppVersion, ServiceUpdateInfo, SystemUpdateTarget, WorkerNode } from "@/types/domain";

export function shortCommit(value?: string) { const commit = value?.trim() || ""; if (!commit || commit === "unknown") return "-"; return commit.length > 12 ? commit.slice(0, 12) : commit; }

export function formatOptionalDate(value?: string, timezone?: string) { const raw = value?.trim() || ""; if (!raw || raw === "unknown") return "-"; if (Number.isNaN(Date.parse(raw))) return raw; return formatDateTimeInTimeZone(raw, timezone, { year: "numeric", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" }); }

type UpdateState = { label: string; tone: "default" | "warning" | "muted" | "ok"; title?: string };

export function systemUpdateTargetState(target: SystemUpdateTarget): UpdateState { if (target.update_check_error) return { label: "確認失敗", tone: "warning", title: target.update_check_error }; if (target.update_check_source === "disabled") return { label: "更新確認なし", tone: "muted" }; if (!target.latest_version) return { label: "未確認", tone: "muted" }; return target.update_available ? { label: `更新あり ${target.latest_version}`, tone: "warning" } : { label: "更新なし", tone: "ok" }; }

export function controlPanelUpdateState(version?: AppVersion): UpdateState { if (!version) return { label: "確認中", tone: "muted" }; if (version.update_check_error) return { label: "確認失敗", tone: "warning", title: version.update_check_error }; if (version.update_available && version.latest_version) return { label: `更新あり ${version.latest_version}`, tone: "warning" }; if (version.update_check_source === "disabled") return { label: "更新確認なし", tone: "muted" }; return { label: "更新なし", tone: "ok" }; }

export function serviceUpdateForNode(node: WorkerNode, version?: AppVersion) { return version?.service_updates?.[node.service_type]; }

export function nodeUpdateState(node: WorkerNode, version?: ServiceUpdateInfo): UpdateState { if (!(node.reported_version || node.version)) return { label: "未報告", tone: "muted" }; if (version?.update_check_error) return { label: "確認失敗", tone: "warning", title: version.update_check_error }; const current = (node.reported_version || node.version || "").trim(); const latest = version?.latest_version?.trim() || ""; if (!latest) return version?.update_check_source === "disabled" ? { label: "更新確認なし", tone: "muted" } : { label: "確認ソース未設定", tone: "muted" }; const comparison = compareSystemUpdateVersions(current, latest); if (comparison === null) return { label: "比較不能", tone: "muted", title: `報告バージョン ${current} をSemVerとして比較できません。` }; if (comparison < 0) return { label: `更新候補 ${latest}`, tone: "warning" }; if (comparison > 0) return { label: "報告バージョンが新しい", tone: "muted" }; return { label: "更新なし", tone: "ok" }; }

export function UpdateStatusBadge({ state }: { state: UpdateState }) { const variant = state.tone === "warning" ? "destructive" : state.tone === "muted" ? "secondary" : "default"; return <Badge variant={variant} title={state.title} aria-label={state.title ? `${state.label}: ${state.title}` : state.label} tabIndex={state.title ? 0 : undefined}>{state.label}</Badge>; }

export function serviceTypeLabel(type: string) { const labels: Record<string, string> = { control_panel: "Control Panel", discord_bot: "Discord Bot", encoder_recorder: "Encoder/Recorder", observability: "Observability", update_agent: "AutoStream Updater", worker: "Worker" }; return labels[type] || type || "-"; }
