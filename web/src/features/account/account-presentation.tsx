"use client";
import { japaneseCopy, type UICopy } from "@/lib/i18n/ui-v2/copy";


import { formatDateTimeInTimeZone } from "@/lib/timezone";

export function providerLabel(value: string) {
  if (!value) return "OAuth";
  return value.charAt(0).toUpperCase() + value.slice(1);
}

export function formatDateTime(value: string, timezone?: string) {
  return formatDateTimeInTimeZone(value, timezone, { dateStyle: "short", timeStyle: "short" });
}

export function accountStatusLabel(status?: string, uiText: UICopy = japaneseCopy) {
  switch (status) {
    case "active":
      return uiText("有効");
    case "locked":
      return uiText("ロック中");
    case "disabled":
      return uiText("無効");
    case "pending_password_change":
      return uiText("初回設定待ち");
    default:
      return status || uiText("確認中");
  }
}

export function roleLabel(role: string, uiText: UICopy = japaneseCopy) {
  switch (role) {
    case "super_admin":
      return uiText("システム管理者");
    case "admin":
      return uiText("管理者");
    case "operator":
      return uiText("配信担当者");
    case "viewer":
      return uiText("閲覧者");
    default:
      return role.replaceAll("_", " ");
  }
}
