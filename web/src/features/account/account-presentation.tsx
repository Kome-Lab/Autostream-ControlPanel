"use client";

import { formatDateTimeInTimeZone } from "@/lib/timezone";

export function providerLabel(value: string) {
  if (!value) return "OAuth";
  return value.charAt(0).toUpperCase() + value.slice(1);
}

export function formatDateTime(value: string, timezone?: string) {
  return formatDateTimeInTimeZone(value, timezone, { dateStyle: "short", timeStyle: "short" });
}

export function accountStatusLabel(status?: string) {
  switch (status) {
    case "active":
      return "有効";
    case "locked":
      return "ロック中";
    case "disabled":
      return "無効";
    case "pending_password_change":
      return "初回設定待ち";
    default:
      return status || "確認中";
  }
}

export function roleLabel(role: string) {
  switch (role) {
    case "super_admin":
      return "システム管理者";
    case "admin":
      return "管理者";
    case "operator":
      return "配信担当者";
    case "viewer":
      return "閲覧者";
    default:
      return role.replaceAll("_", " ");
  }
}
