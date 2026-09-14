"use client";

import { useMemo } from "react";
import { oauthAccountName } from "@/lib/i18n/ui-v2/presentation-copy";
import { useUICopy } from "@/lib/i18n/ui-v2/use-ui-copy";
import { useResourceData } from "@/features/queries";
import { oauthAccountSupportsPurpose, type OAuthAccountPurpose } from "@/lib/oauth-account";
import { normalizeRows, rowString, firstNonEmpty } from "./resource-values";
import { oauthAccountOptionDescription, compactList } from "./resource-presentation";

export function useResourceRows(path: string) {
  const query = useResourceData<unknown>(path);
  return useMemo(() => normalizeRows(query.data), [query.data]);
}

export function useResourceOptions(path: string, valueKeys: string[], labelKeys: string[], detailKeys: string[] = []) {
  const rows = useResourceRows(path);
  return useMemo(
    () =>
      rows
        .map((row) => {
          const value = rowString(row, valueKeys);
          const label = firstNonEmpty(rowString(row, labelKeys), value);
          const description = firstNonEmpty(rowString(row, detailKeys));
          return { value, label, description };
        })
        .filter((option) => option.value),
    [detailKeys, labelKeys, rows, valueKeys],
  );
}

export function useOAuthAccountOptions(purpose: OAuthAccountPurpose) {
  const uiText = useUICopy();
  const rows = useResourceRows("/integrations/oauth-accounts");
  return useMemo(
    () =>
      rows
        .filter((row) => oauthAccountSupportsPurpose(row, purpose))
        .map((row) => {
          const value = rowString(row, ["id"]);
          return {
            value,
            label: oauthAccountName(row, uiText),
            description: oauthAccountOptionDescription(row, uiText),
          };
        })
        .filter((option) => option.value),
    [purpose, rows, uiText],
  );
}

export function useRegisteredNodeOptions(serviceType: string) {
  const rows = useResourceRows("/nodes");
  return useMemo(
    () =>
      rows
        .filter((row) => rowString(row, ["service_type", "node_type"]) === serviceType)
        .map((row) => {
          const value = rowString(row, ["service_id", "id"]);
          const label = firstNonEmpty(rowString(row, ["service_name", "name"]), value);
          const description = compactList([rowString(row, ["status"]), rowString(row, ["public_url", "host"])]).join(" / ");
          return { value, label, description };
        })
        .filter((option) => option.value),
    [rows, serviceType],
  );
}
