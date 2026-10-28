"use client";
import { useUICopy } from "@/lib/i18n/ui-v2/use-ui-copy";


import { useNonSecretDraft } from "@/components/forms/draft-exit";

import { useEffect, useState } from "react";
import { type SubmitResource, type ResourceRow, type SelectOption, noneValue } from "./resource-form-types";
import { useResourceOptions, useOAuthAccountOptions } from "./resource-form-queries";
import { rowString, rowValue, compactRecord, numberValue } from "./resource-values";
import { TextField, SelectField, NumberField, SwitchField, FormActions } from "./resource-input-fields";

export function ArchiveProfileForm({ disabled, submit, initial, submitLabel }: { disabled: boolean; submit: SubmitResource; initial?: ResourceRow; submitLabel?: string }) {
  const uiText = useUICopy();
  const row = initial || {};
  const driveDestinations = useResourceOptions("/archive/destinations", ["id"], ["name", "id"]);
  const [name, setName] = useState(() => rowString(row, ["name"]) || "shared-drive");
  const [format, setFormat] = useState(() => rowString(row, ["format", "config.format"]) || "mp4");
  const [retentionDays, setRetentionDays] = useState(() => rowString(row, ["retention_days", "config.retention_days"]) || "180");
  const [uploadEnabled, setUploadEnabled] = useState(() => rowValue(row, ["upload_enabled", "config.upload_enabled"]) !== false);
  const [driveDestinationID, setDriveDestinationID, driveDestinationDraftValue] = useDraftDefaultOption(rowString(row, ["drive_destination_id", "config.drive_destination_id"]), driveDestinations[0]?.value, uploadEnabled);

  useNonSecretDraft([name, format, retentionDays, uploadEnabled, driveDestinationDraftValue]);

  return (
    <form
      className="space-y-3"
      onSubmit={(event) => {
        event.preventDefault();
        submit({
          name,
          config: compactRecord({
            format,
            retention_days: numberValue(retentionDays, 180),
            upload_enabled: uploadEnabled,
            drive_destination_id: driveDestinationID === noneValue ? "" : driveDestinationID,
          }),
        });
      }}
    >
      <div className="grid gap-3 md:grid-cols-2">
        <TextField label={uiText("プロファイル名")} value={name} onChange={setName} required />
        <SelectField
          label={uiText("録画形式")}
          value={format}
          onChange={setFormat}
          options={[
            { value: "mp4", label: "MP4" },
            { value: "mkv", label: "MKV" },
          ]}
        />
        <NumberField label={uiText("保存期間 (日)")} value={retentionDays} onChange={setRetentionDays} min={1} required />
        <SelectField label={uiText("Drive保存先")} value={driveDestinationID} onChange={setDriveDestinationID} options={[{ value: noneValue, label: uiText("未選択") }, ...retainedDraftOptions(driveDestinations, driveDestinationID)]} />
      </div>
      <SwitchField label={uiText("録画後に保存先へアップロード")} checked={uploadEnabled} onCheckedChange={setUploadEnabled} />
      <FormActions label={submitLabel} disabled={disabled} />
    </form>
  );
}

export function DriveDestinationForm({ disabled, submit, initial, submitLabel }: { disabled: boolean; submit: SubmitResource; initial?: ResourceRow; submitLabel?: string }) {
  const uiText = useUICopy();
  const row = initial || {};
  const oauthAccounts = useOAuthAccountOptions("drive");
  const [name, setName] = useState(() => rowString(row, ["name"]) || "archive-drive");
  const [oauthAccountID, setOAuthAccountID, oauthAccountDraftValue] = useDraftDefaultOption(rowString(row, ["oauth_account_id"]), oauthAccounts[0]?.value);
  const [folderID, setFolderID] = useState(() => rowString(row, ["folder_id"]));
  const [sharedDrive, setSharedDrive] = useState(() => rowValue(row, ["shared_drive"]) === true);

  useNonSecretDraft([name, oauthAccountDraftValue, folderID, sharedDrive]);

  return (
    <form
      className="space-y-3"
      onSubmit={(event) => {
        event.preventDefault();
        submit(
          compactRecord({
            name,
            auth_mode: "oauth2",
            oauth_account_id: oauthAccountID === noneValue ? "" : oauthAccountID,
            folder_id: folderID,
            shared_drive: sharedDrive,
          }),
        );
      }}
    >
      <div className="grid gap-3 md:grid-cols-2">
        <TextField label={uiText("保存先名")} value={name} onChange={setName} required />
        <SelectField label={uiText("OAuthアカウント")} value={oauthAccountID} onChange={setOAuthAccountID} options={[{ value: noneValue, label: uiText("未選択") }, ...retainedDraftOptions(oauthAccounts, oauthAccountID)]} />
        <TextField label={sharedDrive ? uiText("共有ドライブ配下のフォルダID") : uiText("DriveフォルダID")} value={folderID} onChange={setFolderID} required description={uiText("URLのfolders/以降にあるIDを入力します。")} />
      </div>
      <SwitchField label={uiText("共有ドライブを使う")} checked={sharedDrive} onCheckedChange={setSharedDrive} />
      {oauthAccounts.length === 0 ? <p className="text-sm text-muted-foreground">{uiText("Drive保存用途でGoogleアカウントを接続してください。")}</p> : null}
      <FormActions label={submitLabel} disabled={disabled || oauthAccountID === noneValue} />
    </form>
  );
}

// Undefined means the first automatic default is still pending; noneValue is an
// explicit choice. Keep the default and its dirty baseline in the draft owner.
function useDraftDefaultOption(initialValue: string, firstValue: string | undefined, enabled = true) {
  const [selection, setSelection] = useState(() => {
    const value = initialValue && initialValue !== noneValue ? initialValue : enabled ? firstValue : undefined;
    return { value, baseline: value };
  });
  useEffect(() => {
    if (!enabled || selection.value !== undefined || !firstValue) return;
    const handle = window.setTimeout(() => {
      setSelection((current) => current.value === undefined ? { value: firstValue, baseline: firstValue } : current);
    }, 0);
    return () => window.clearTimeout(handle);
  }, [enabled, firstValue, selection.value]);
  const value = selection.value ?? noneValue;
  const dirtyValue = selection.value === selection.baseline ? "" : value;
  return [value, (nextValue: string) => setSelection((current) => ({ ...current, value: nextValue || noneValue })), dirtyValue] as const;
}

function retainedDraftOptions(options: SelectOption[], value: string): SelectOption[] {
  return value !== noneValue && !options.some((option) => option.value === value)
    ? [{ value, label: value }, ...options]
    : options;
}
