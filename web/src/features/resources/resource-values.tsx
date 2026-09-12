"use client";

import { type ResourceRow } from "./resource-form-types";

export function toggleListValue(values: string[], value: string, checked: boolean) {
  if (checked) return values.includes(value) ? values : [...values, value];
  return values.filter((item) => item !== value);
}

export function numberValue(value: string, fallback: number) {
  const parsed = Number(value);
  return Number.isFinite(parsed) ? parsed : fallback;
}

export function numberSetting(value: unknown, fallback: number) {
  return typeof value === "number" && Number.isFinite(value) ? value : fallback;
}

export function stringSetting(value: unknown, fallback: string) {
  return typeof value === "string" && value.trim() !== "" ? value : fallback;
}

export function stringListSetting(value: unknown) {
  if (Array.isArray(value)) return value.map((item) => String(item).trim()).filter(Boolean);
  if (typeof value === "string") return splitList(value);
  return [];
}

export function splitList(value: string) {
  return value
    .split(/[,\n]/)
    .map((item) => item.trim())
    .filter(Boolean);
}

export function compactRecord(record: Record<string, unknown>) {
  return Object.fromEntries(Object.entries(record).filter(([, value]) => value !== "" && value !== undefined));
}

export function rowString(row: ResourceRow, keys: string[]) {
  for (const key of keys) {
    const value = nestedRowValue(row, key);
    if (typeof value === "string" && value.trim() !== "") return value;
    if (typeof value === "number") return String(value);
    if (Array.isArray(value) && value.length > 0) return value.map((item) => String(item)).join(", ");
  }
  return "";
}

export function rowBoolean(row: ResourceRow, keys: string[], fallback: boolean) {
  const value = rowValue(row, keys);
  if (typeof value === "boolean") return value;
  if (typeof value === "string") {
    const normalized = value.trim().toLowerCase();
    if (["true", "1", "yes", "on"].includes(normalized)) return true;
    if (["false", "0", "no", "off"].includes(normalized)) return false;
  }
  return fallback;
}

export function normalizeDeepgramLanguage(value: string) {
  return value.trim().toLowerCase().startsWith("en") ? "en" : "ja";
}

export function firstNonEmpty(...values: string[]) {
  return values.find((value) => value.trim() !== "") || "";
}

export function normalizeRows(data: unknown): Record<string, unknown>[] {
  if (!data) return [];
  if (Array.isArray(data)) return data.map((item) => normalizeRow(item));
  if (isRecord(data)) {
    for (const key of ["items", "data", "results", "secrets", "permissions", "nodes", "services"]) {
      const value = data[key];
      if (Array.isArray(value)) return value.map((item) => normalizeRow(item));
    }
    return Object.entries(data).map(([key, value]) => ({ name: key, value }));
  }
  return [{ value: data }];
}

function normalizeRow(item: unknown): Record<string, unknown> {
  if (isRecord(item)) {
    const row: Record<string, unknown> = {};
    for (const [key, value] of Object.entries(item)) row[key] = value;
    return row;
  }
  return { value: item };
}

export function rowValue(row: ResourceRow, keys: string[]) {
  for (const key of keys) {
    const value = nestedRowValue(row, key);
    if (value !== undefined && value !== null && value !== "") return value;
  }
  return undefined;
}

function nestedRowValue(row: ResourceRow, key: string): unknown {
  const parts = key.split(".");
  let current: unknown = row;
  for (const part of parts) {
    if (!isRecord(current)) return undefined;
    current = current[part];
  }
  return current;
}

export function resourceRowID(row: ResourceRow) {
  return rowString(row, ["id", "service_id"]);
}

export function resourceRowLabel(row: ResourceRow) {
  return firstNonEmpty(rowString(row, ["name", "service_name", "username", "oauth_account_display_name", "display_name", "account_label", "provider_type", "id"]), "この項目");
}

export function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
