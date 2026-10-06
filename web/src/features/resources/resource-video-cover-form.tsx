"use client";

import { useEffect, useId, useRef, useState } from "react";
import { ImageUp, LoaderCircle } from "lucide-react";
import { useI18n } from "@/components/admin/i18n-provider";
import { useExistingDraft, useNonSecretDraft } from "@/components/forms/draft-exit";
import { Button } from "@/components/ui/button";
import { useCurrentUser } from "@/features/queries";
import { uploadDraftMediaAsset } from "@/features/streams/media-asset-client";
import { hasAnyPermission } from "@/lib/auth/permissions";
import { adaptAPIError } from "@/lib/foundation/api-errors/adapter";
import { FormActions, SwitchField, TextField } from "./resource-input-fields";
import type { ResourceRow, SubmitResource } from "./resource-form-types";
import { rowString } from "./resource-values";

type CoverImage = { assetID: string; variantID: string; name: string };

export function VideoCoverPresetForm({ disabled, submit, initial, submitLabel }: {
  disabled: boolean;
  submit: SubmitResource;
  initial?: ResourceRow;
  submitLabel?: string;
}) {
  const { locale, t } = useI18n();
  const currentUser = useCurrentUser();
  const inputID = useId();
  // Keep the form's original revision with its original fields. A list refresh
  // must never attach a newer revision to an older draft.
  const [baseline] = useState(() => ({
    name: rowString(initial || {}, ["name"]),
    enabled: initial?.enabled !== false,
    revision: Number(initial?.revision || 0),
    image: {
      assetID: rowString(initial || {}, ["asset_id"]),
      variantID: rowString(initial || {}, ["asset_variant_id"]),
      name: "",
    } as CoverImage,
  }));
  const [name, setName] = useState(baseline.name);
  const [enabled, setEnabled] = useState(baseline.enabled);
  const [image, setImage] = useState(baseline.image);
  const [preview, setPreview] = useState("");
  const [uploading, setUploading] = useState(false);
  const [error, setError] = useState("");
  const [sessionID, setSessionID] = useState("");
  const busy = useRef(false);
  const mounted = useRef(true);
  const previewURL = useRef("");
  const canUpload = currentUser.isSuccess && hasAnyPermission(currentUser.data, ["streams.create", "streams.update"]);
  const hasSavedImage = Boolean(baseline.image.assetID && baseline.image.variantID);
  const hasImage = Boolean(image.assetID && image.variantID);
  const validRevision = !initial || (Number.isSafeInteger(baseline.revision) && baseline.revision > 0);
  const ready = Boolean(name.trim() && hasImage && validRevision && !error);

  useNonSecretDraft([name, enabled, image.assetID, image.variantID]);
  useExistingDraft(false, uploading);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      if (previewURL.current) URL.revokeObjectURL(previewURL.current);
      previewURL.current = "";
    };
  }, []);

  const upload = async (file?: File) => {
    if (!file || disabled || !canUpload || busy.current) return;
    busy.current = true;
    setUploading(true);
    setError("");
    try {
      const result = await uploadDraftMediaAsset(file, "video_cover", sessionID ? { id: sessionID } : undefined);
      if (!mounted.current) return;
      const url = URL.createObjectURL(file);
      if (previewURL.current) URL.revokeObjectURL(previewURL.current);
      previewURL.current = url;
      setPreview(url);
      setSessionID(result.session.id);
      setImage({ assetID: result.asset.id, variantID: result.variant.id, name: file.name });
    } catch (cause) {
      if (mounted.current) setError(t(adaptAPIError(cause).messageKey));
    } finally {
      busy.current = false;
      if (mounted.current) setUploading(false);
    }
  };

  const restoreSavedImage = () => {
    if (previewURL.current) URL.revokeObjectURL(previewURL.current);
    previewURL.current = "";
    setPreview("");
    setImage(baseline.image);
    setError("");
  };

  return (
    <form className="space-y-4" onSubmit={(event) => {
      event.preventDefault();
      if (disabled || uploading || !ready) return;
      submit({
        name: name.trim(),
        asset_id: image.assetID,
        asset_variant_id: image.variantID,
        enabled,
        ...(initial ? { expected_revision: baseline.revision } : {}),
      });
    }}>
      <fieldset disabled={disabled || uploading} className="space-y-4">
        <TextField label={locale === "ja" ? "プリセット名" : "Preset name"} value={name} onChange={setName} required />
        <div className="space-y-3 rounded-md border p-3">
          <label htmlFor={inputID} className="block text-sm font-medium">{locale === "ja" ? "蓋画像" : "Video cover image"}</label>
          <p id={`${inputID}-help`} className="text-xs text-muted-foreground">{locale === "ja" ? "16:9の不透明なPNG・JPEG・WebP画像を選択してください。画像の検証と処理が完了してから保存できます。" : "Choose an opaque 16:9 PNG, JPEG, or WebP image. Save after validation and processing have completed."}</p>
          <label className="inline-flex min-h-10 cursor-pointer items-center gap-2 rounded-md border px-3 text-sm has-[:disabled]:cursor-not-allowed has-[:disabled]:opacity-50">
            {uploading ? <LoaderCircle aria-hidden="true" className="size-4 animate-spin" /> : <ImageUp aria-hidden="true" className="size-4" />}
            {locale === "ja" ? "画像を選択" : "Choose image"}
            <input id={inputID} type="file" accept="image/png,image/jpeg,image/webp" className="sr-only" disabled={disabled || uploading || !canUpload} aria-describedby={`${inputID}-help`} onChange={(event) => {
              const file = event.target.files?.[0];
              event.target.value = "";
              void upload(file);
            }} />
          </label>
          {preview ? <div className="aspect-video rounded-md border bg-muted bg-cover bg-center" role="img" aria-label={locale === "ja" ? "選択した蓋画像のプレビュー" : "Selected video cover preview"} style={{ backgroundImage: `url(${JSON.stringify(preview)})` }} /> : null}
          <p role="status" className="break-words text-sm text-muted-foreground">{uploading
            ? locale === "ja" ? "画像をアップロードして処理しています。" : "Uploading and processing the image."
            : image.name
              ? locale === "ja" ? `${image.name}：処理完了・保存できます。` : `${image.name}: ready to save.`
              : hasImage
                ? locale === "ja" ? "保存済みの画像を使用します。画像を選び直すと置き換わります。" : "The saved image is selected. Choose a new image to replace it."
                : locale === "ja" ? "画像が選択されていません。" : "No image is selected."}</p>
          {hasSavedImage && (image.assetID !== baseline.image.assetID || error) ? <Button type="button" variant="outline" size="sm" onClick={restoreSavedImage}>{locale === "ja" ? "保存済み画像を使用" : "Use the saved image"}</Button> : null}
          {!canUpload ? <p role="status" className="text-xs text-muted-foreground">{currentUser.isPending
            ? locale === "ja" ? "画像アップロード権限を確認しています。" : "Checking image upload permission."
            : locale === "ja" ? "画像の登録には配信枠の作成または更新権限が必要です。保存済み画像を使う編集は行えます。" : "Image upload requires stream create or update permission. You can still edit a preset using its saved image."}</p> : null}
        </div>
        <SwitchField label={locale === "ja" ? "配信枠の選択肢に表示" : "Available in stream slots"} checked={enabled} onCheckedChange={setEnabled} />
        <p className="text-xs text-muted-foreground">{locale === "ja" ? "プリセットの変更・削除後も、配信枠に保存された画像は保持されます。蓋画像を表示しても音声は継続し、ウォーターマークはその上に表示されます。" : "Existing stream slots retain their saved images after a preset changes or is deleted. Audio continues while the cover is shown, and the watermark stays above it."}</p>
        {error ? <p role="alert" className="text-sm text-destructive">{error}</p> : null}
        {!validRevision ? <p role="alert" className="text-sm text-destructive">{locale === "ja" ? "更新に必要な版を確認できません。一覧を更新して開き直してください。" : "The preset revision is unavailable. Refresh the list and reopen this form."}</p> : null}
        <FormActions label={submitLabel} disabled={disabled || uploading || !ready} />
      </fieldset>
    </form>
  );
}
