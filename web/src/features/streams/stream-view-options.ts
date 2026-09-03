"use client";

import { useMemo } from "react";

import { useResourceData, useServiceHealth } from "@/features/queries";
import {
  oauthAccountDisplayName as oauthAccountLabel,
  oauthAccountPurposeLabel,
  oauthAccountSupportsPurpose,
  oauthProviderTypeLabel as providerTypeLabel,
  type OAuthAccountPurpose,
} from "@/lib/oauth-account";
import { streamServiceAssignmentOption } from "@/lib/stream-create";
import { safeDisplayURL } from "@/lib/stream-presentation";
import type { Stream } from "@/types/domain";

export type StreamResourceRow = Record<string, unknown>;
export type StreamSelectOption = { value: string; label: string; description?: string; disabled?: boolean };

export const noneValue = "__none__";

export function useResourceOptions(path: string, labelKeys: string[], detailKeys: string[] = []) {
  const query = useResourceData<unknown>(path);
  const rows = useMemo(() => normalizeRows(query.data), [query.data]);
  return useMemo(
    () => rows
      .map((row) => {
        const value = rowString(row, ["id"]);
        const label = firstNonEmpty(rowString(row, labelKeys), value);
        const description = compactList(detailKeys.map((key) => rowString(row, [key]))).join(" / ");
        return { value, label, description };
      })
      .filter((option) => option.value),
    [detailKeys, labelKeys, rows],
  );
}

export function useOAuthAccountOptions(purpose: OAuthAccountPurpose) {
  const query = useResourceData<unknown>("/integrations/oauth-accounts");
  const rows = useMemo(() => normalizeRows(query.data), [query.data]);
  return useMemo(
    () => rows
      .filter((row) => oauthAccountSupportsPurpose(row, purpose))
      .map((row) => {
        const value = rowString(row, ["id"]);
        const provider = rowString(row, ["provider_type"]);
        return {
          value,
          label: oauthAccountLabel(row),
          description: compactList([provider ? providerTypeLabel(provider) : "", oauthAccountPurposeLabel(row)]).join(" / "),
        };
      })
      .filter((option) => option.value),
    [purpose, rows],
  );
}

export function useServiceOptions(serviceType: string, editingStreamID?: string) {
  const query = useServiceHealth();
  const rows = useMemo(() => query.data || [], [query.data]);
  return useMemo(
    () => rows
      .filter((row) => row.service_type === serviceType)
      .map((row) => {
        const value = row.service_id || row.id;
        const label = firstNonEmpty(row.service_name, row.service_id || row.id);
        return streamServiceAssignmentOption({ value, label, currentStreamID: row.current_stream_id }, editingStreamID);
      })
      .filter((option) => option.value),
    [editingStreamID, rows, serviceType],
  );
}

export function useOptionLabelMap(options: StreamSelectOption[]) {
  return useMemo(() => new Map(options.map((option) => [option.value, option.label])), [options]);
}

export function optionLabel(labels: Map<string, string>, value?: string) {
  const id = value?.trim() || "";
  if (!id) return "";
  return labels.get(id) || id;
}

export function compactList(values: Array<string | undefined>) {
  return values.map((value) => value?.trim() || "").filter(Boolean);
}

export function streamInputPresentation(stream: Stream) {
  const configured = safeDisplayURL(stream.encoder_input_url || stream.input_source);
  if (configured) return configured;
  if (stream.assigned_encoder_id) return "Node側で開始時に自動生成";
  return "入力未設定";
}

export function normalizeRows(data: unknown): StreamResourceRow[] {
  if (!data) return [];
  if (Array.isArray(data)) return data.filter(isRecord);
  if (isRecord(data)) {
    for (const key of ["items", "data", "results"]) {
      const value = data[key];
      if (Array.isArray(value)) return value.filter(isRecord);
    }
  }
  return [];
}

export function rowString(row: StreamResourceRow, keys: string[]) {
  for (const key of keys) {
    const value = row[key];
    if (typeof value === "string" && value.trim() !== "") return value;
    if (typeof value === "number") return String(value);
  }
  return "";
}

export function firstNonEmpty(...values: string[]) {
  return values.find((value) => value.trim() !== "") || "";
}

export function isRecord(value: unknown): value is StreamResourceRow {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

export function selectedValue(value: string) {
  return value === noneValue ? "" : value;
}

export function optionOrNone(value?: string) {
  return value?.trim() || noneValue;
}
