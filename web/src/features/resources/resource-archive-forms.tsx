"use client";

import { useState } from "react";
import { type SubmitResource, type ResourceRow, noneValue } from "./resource-form-types";
import { useResourceOptions, useOAuthAccountOptions } from "./resource-form-queries";
import { rowString, rowValue, compactRecord, numberValue } from "./resource-values";
import { TextField, SelectField, NumberField, SwitchField, FormActions } from "./resource-input-fields";

export function ArchiveProfileForm({ disabled, submit, initial, submitLabel }: { disabled: boolean; submit: SubmitResource; initial?: ResourceRow; submitLabel?: string }) {
  const row = initial || {};
  const driveDestinations = useResourceOptions("/archive/destinations", ["id"], ["name", "id"]);
  const [name, setName] = useState(() => rowString(row, ["name"]) || "shared-drive");
  const [format, setFormat] = useState(() => rowString(row, ["format", "config.format"]) || "mp4");
  const [retentionDays, setRetentionDays] = useState(() => rowString(row, ["retention_days", "config.retention_days"]) || "180");
  const [uploadEnabled, setUploadEnabled] = useState(() => rowValue(row, ["upload_enabled", "config.upload_enabled"]) !== false);
  const [driveDestinationID, setDriveDestinationID] = useState(() => rowString(row, ["drive_destination_id", "config.drive_destination_id"]) || noneValue);
  const effectiveDriveDestinationID = uploadEnabled && driveDestinationID === noneValue && driveDestinations[0]?.value ? driveDestinations[0].value : driveDestinationID;

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
            drive_destination_id: effectiveDriveDestinationID === noneValue ? "" : effectiveDriveDestinationID,
          }),
        });
      }}
    >
      <div className="grid gap-3 md:grid-cols-2">
        <TextField label="プロファイル名" value={name} onChange={setName} required />
        <SelectField
          label="録画形式"
          value={format}
          onChange={setFormat}
          options={[
            { value: "mp4", label: "MP4" },
            { value: "mkv", label: "MKV" },
          ]}
        />
        <NumberField label="保存期間 (日)" value={retentionDays} onChange={setRetentionDays} min={1} required />
        <SelectField label="Drive保存先" value={effectiveDriveDestinationID} onChange={setDriveDestinationID} options={[{ value: noneValue, label: "未選択" }, ...driveDestinations]} />
      </div>
      <SwitchField label="録画後に保存先へアップロード" checked={uploadEnabled} onCheckedChange={setUploadEnabled} />
      <FormActions label={submitLabel} disabled={disabled} />
    </form>
  );
}

export function DriveDestinationForm({ disabled, submit, initial, submitLabel }: { disabled: boolean; submit: SubmitResource; initial?: ResourceRow; submitLabel?: string }) {
  const row = initial || {};
  const oauthAccounts = useOAuthAccountOptions("drive");
  const [name, setName] = useState(() => rowString(row, ["name"]) || "archive-drive");
  const [oauthAccountID, setOAuthAccountID] = useState(() => rowString(row, ["oauth_account_id"]) || noneValue);
  const [folderID, setFolderID] = useState(() => rowString(row, ["folder_id"]));
  const [sharedDrive, setSharedDrive] = useState(() => rowValue(row, ["shared_drive"]) === true);
  const effectiveOAuthAccountID = oauthAccountID === noneValue && oauthAccounts[0]?.value ? oauthAccounts[0].value : oauthAccountID;

  return (
    <form
      className="space-y-3"
      onSubmit={(event) => {
        event.preventDefault();
        submit(
          compactRecord({
            name,
            auth_mode: "oauth2",
            oauth_account_id: effectiveOAuthAccountID === noneValue ? "" : effectiveOAuthAccountID,
            folder_id: folderID,
            shared_drive: sharedDrive,
          }),
        );
      }}
    >
      <div className="grid gap-3 md:grid-cols-2">
        <TextField label="保存先名" value={name} onChange={setName} required />
        <SelectField label="OAuthアカウント" value={effectiveOAuthAccountID} onChange={setOAuthAccountID} options={[{ value: noneValue, label: "未選択" }, ...oauthAccounts]} />
        <TextField label={sharedDrive ? "共有ドライブ配下のフォルダID" : "DriveフォルダID"} value={folderID} onChange={setFolderID} required description="URLのfolders/以降にあるIDを入力します。" />
      </div>
      <SwitchField label="共有ドライブを使う" checked={sharedDrive} onCheckedChange={setSharedDrive} />
      {oauthAccounts.length === 0 ? <p className="text-sm text-muted-foreground">Drive保存用途でGoogleアカウントを接続してください。</p> : null}
      <FormActions label={submitLabel} disabled={disabled || effectiveOAuthAccountID === noneValue} />
    </form>
  );
}
