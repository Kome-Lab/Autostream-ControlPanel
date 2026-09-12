"use client";

import { useQueryClient } from "@tanstack/react-query";
import type { CurrentUser } from "@/types/domain";
import { type AccountAuthoritySnapshot } from "@/features/account/account-action-policy";

export type AccountNotice = { tone: "success" | "error"; text: string } | null;

export function readAccountAuthority(queryClient: ReturnType<typeof useQueryClient>): AccountAuthoritySnapshot {
  const state = queryClient.getQueryState<CurrentUser>(["auth", "me"]);
  const current = queryClient.getQueryData<CurrentUser>(["auth", "me"]);
  if (state?.fetchStatus === "fetching") {
    return Object.freeze({ session: "authenticated", freshness: "refreshing", revision: accountAuthorityRevision(current) });
  }
  if (state?.status !== "success" || !current?.user?.id || !current.user.username) {
    return Object.freeze({ session: "unavailable", freshness: "unavailable", revision: "unavailable" });
  }
  return Object.freeze({
    session: "authenticated",
    freshness: "fresh",
    revision: accountAuthorityRevision(current),
  });
}

function accountAuthorityRevision(current: CurrentUser | undefined) {
  if (!current?.user) return "unavailable";
  return JSON.stringify([
    current.user.id,
    current.user.username,
    current.user.status ?? "",
    [...(current.user.roles ?? [])].sort(),
    [...(current.permissions ?? [])].sort(),
  ]);
}
