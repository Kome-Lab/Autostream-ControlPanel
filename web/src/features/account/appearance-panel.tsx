"use client";

import { type KeyboardEvent, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Check, Palette, Save } from "lucide-react";
import { useI18n } from "@/components/admin/i18n-provider";
import { useTheme } from "@/components/admin/theme-provider";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { APIError, apiGet, apiPut } from "@/lib/api/client";
import { cn } from "@/lib/utils";
import {
  buildUIPreferenceUpdate,
  safeUserUIPreference,
  themeLabels,
  uiPreferenceQueryKey,
  userColorModes,
  userThemeIDs,
  type SafeUserUIPreference,
  type UserColorMode,
  type UserUIPreference,
  type UserThemeID,
} from "@/features/account/ui-preferences";

const modeLabels: Readonly<Record<UserColorMode, string>> = Object.freeze({ system: "システム", light: "ライト", dark: "ダーク" });
const previewColors: Readonly<Record<UserThemeID, string>> = Object.freeze({
  autostream: "#16866f", slate: "#566273", ocean: "#1769a6", cyan: "#087f8c", indigo: "#4c5bd4", violet: "#7651c9",
  magenta: "#ac3c9a", rose: "#bf4265", crimson: "#b33443", amber: "#9a6508", emerald: "#238452", monochrome: "#454545",
});

function moveRadio<T extends string>(
  event: KeyboardEvent<HTMLButtonElement>,
  currentIndex: number,
  values: readonly T[],
  selectValue: (value: T) => void,
) {
  let nextIndex: number | undefined;
  switch (event.key) {
    case "ArrowLeft":
    case "ArrowUp":
      nextIndex = (currentIndex - 1 + values.length) % values.length;
      break;
    case "ArrowRight":
    case "ArrowDown":
      nextIndex = (currentIndex + 1) % values.length;
      break;
    case "Home":
      nextIndex = 0;
      break;
    case "End":
      nextIndex = values.length - 1;
      break;
    default:
      return;
  }
  event.preventDefault();
  selectValue(values[nextIndex]);
  const radios = event.currentTarget.closest('[role="radiogroup"]')?.querySelectorAll<HTMLButtonElement>('[role="radio"]');
  radios?.item(nextIndex).focus();
}

export function AppearancePanel() {
  const { locale } = useI18n();
  const queryClient = useQueryClient();
  const theme = useTheme();
  const preferenceQuery = useQuery({
    queryKey: uiPreferenceQueryKey,
    queryFn: () => apiGet<UserUIPreference>("/account/preferences/ui"),
    retry: false,
  });
  const savedPreference = preferenceQuery.data ? safeUserUIPreference(preferenceQuery.data) : theme.preference;
  const [draftOverride, setDraftOverride] = useState<SafeUserUIPreference | null>(null);
  const draft = draftOverride?.revision === savedPreference.revision ? draftOverride : savedPreference;
  const [notice, setNotice] = useState("");

  const select = (next: Partial<UserUIPreference>) => {
    const selected = safeUserUIPreference({ ...draft, ...next, fallback: false });
    setDraftOverride(selected);
    theme.previewPreference(selected);
    setNotice("");
  };

  const save = useMutation({
    mutationFn: () => apiPut<UserUIPreference>("/account/preferences/ui", buildUIPreferenceUpdate(draft)),
    onSuccess: (saved) => {
      const safe = safeUserUIPreference(saved);
      queryClient.setQueryData(uiPreferenceQueryKey, saved);
      setDraftOverride(null);
      theme.applyPersistedPreference(safe);
      setNotice("表示設定を保存しました。");
    },
    onError: (error) => {
      const previous = safeUserUIPreference(preferenceQuery.data);
      setDraftOverride(previous);
      theme.applyPersistedPreference(previous);
      const code = error instanceof APIError ? error.code : undefined;
      setNotice(code === "revision_conflict" ? "別の画面で更新されました。再読み込みしてから選び直してください。" : "表示設定を保存できなかったため、直前の保存状態へ戻しました。");
    },
  });

  const changed = preferenceQuery.data
    ? draft.theme_id !== safeUserUIPreference(preferenceQuery.data).theme_id || draft.color_mode !== safeUserUIPreference(preferenceQuery.data).color_mode
    : false;
  const themeGroupLabel = locale === "en" ? "Color theme" : "配色テーマ";
  const modeGroupLabel = locale === "en" ? "Color mode" : "表示モード";
  const themeAccessibleName = (themeID: UserThemeID) => locale === "en" ? `${themeLabels[themeID]} theme` : `${themeLabels[themeID]}テーマ`;
  const modeAccessibleName = (mode: UserColorMode) => locale === "en"
    ? `${({ system: "System", light: "Light", dark: "Dark" } as const)[mode]} mode`
    : `${modeLabels[mode]}モード`;

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-lg"><Palette className="size-5" />外観</CardTitle>
        <CardDescription>配色テーマと明るさを選択します。状態の意味を示す色は、どのテーマでも変わりません。</CardDescription>
      </CardHeader>
      <CardContent className="space-y-5">
        {preferenceQuery.isError ? <p role="alert" className="text-sm text-destructive">保存済みの表示設定を読み込めませんでした。再読み込みしてください。</p> : null}
        {draft.fallback ? <p role="status" className="rounded-md border border-amber-300 bg-amber-50 px-3 py-2 text-sm text-amber-900 dark:bg-amber-950/30 dark:text-amber-200">保存されたテーマを安全に表示できないため、AutoStream / システムで表示しています。DB値は自動変更していません。</p> : null}
        <fieldset disabled={preferenceQuery.isLoading || save.isPending}>
          <legend className="mb-2 text-sm font-medium">テーマ</legend>
          <div role="radiogroup" aria-label={themeGroupLabel} className="grid grid-cols-2 gap-2 sm:grid-cols-3 lg:grid-cols-4">
            {userThemeIDs.map((themeID, index) => {
              const selected = draft.theme_id === themeID;
              return (
                <button
                  key={themeID}
                  type="button"
                  role="radio"
                  aria-checked={selected}
                  aria-label={themeAccessibleName(themeID)}
                  tabIndex={selected ? 0 : -1}
                  onClick={() => select({ theme_id: themeID })}
                  onKeyDown={(event) => moveRadio(event, index, userThemeIDs, (next) => select({ theme_id: next }))}
                  className={cn("relative min-h-20 rounded-md border bg-card p-3 text-left outline-none transition-colors focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2", selected && "border-primary ring-1 ring-primary")}
                >
                  <span className="mb-3 block h-3 rounded-full" style={{ backgroundColor: previewColors[themeID] }} aria-hidden="true" />
                  <span className="text-sm font-medium">{themeLabels[themeID]}</span>
                  {selected ? <Check className="absolute right-2 top-2 size-4 text-primary" aria-hidden="true" /> : null}
                </button>
              );
            })}
          </div>
        </fieldset>
        <fieldset disabled={preferenceQuery.isLoading || save.isPending}>
          <legend className="mb-2 text-sm font-medium">表示モード</legend>
          <div role="radiogroup" aria-label={modeGroupLabel} className="inline-grid grid-cols-3 rounded-md border p-1">
            {userColorModes.map((mode, index) => (
              <button
                key={mode}
                type="button"
                role="radio"
                aria-checked={draft.color_mode === mode}
                aria-label={modeAccessibleName(mode)}
                tabIndex={draft.color_mode === mode ? 0 : -1}
                onClick={() => select({ color_mode: mode })}
                onKeyDown={(event) => moveRadio(event, index, userColorModes, (next) => select({ color_mode: next }))}
                className={cn("rounded px-4 py-2 text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring", draft.color_mode === mode ? "bg-primary text-primary-foreground" : "text-muted-foreground hover:bg-muted")}
              >
                {modeLabels[mode]}
              </button>
            ))}
          </div>
        </fieldset>
        {notice ? <p role="status" aria-live="polite" className="text-sm text-muted-foreground">{notice}</p> : null}
        <div className="flex justify-end">
          <Button type="button" aria-label="表示設定を保存" onClick={() => save.mutate()} disabled={!changed || preferenceQuery.isLoading || save.isPending}>
            <Save />{save.isPending ? "保存中" : "表示設定を保存"}
          </Button>
        </div>
      </CardContent>
    </Card>
  );
}
