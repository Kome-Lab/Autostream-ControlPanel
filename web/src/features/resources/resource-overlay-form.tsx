"use client";

import { useState } from "react";
import { Input } from "@/components/ui/input";
import { type SubmitResource, type ResourceRow } from "./resource-form-types";
import { rowString, compactRecord } from "./resource-values";
import { TextField, Field, FormActions } from "./resource-input-fields";

const watermarkCanvasWidth = 1920;

const watermarkCanvasHeight = 1080;

const watermarkMaxBytes = 5 * 1024 * 1024;

export function OverlayProfileForm({ disabled, submit, initial, submitLabel }: { disabled: boolean; submit: SubmitResource; initial?: ResourceRow; submitLabel?: string }) {
  const row = initial || {};
  const existingImageName = rowString(row, ["watermark_image_name", "watermark_file_name", "config.watermark_image_name", "config.watermark_file_name"]);
  const existingPreviewImage = rowString(row, ["watermark_image_data_url", "config.watermark_image_data_url", "watermark_image_url", "config.watermark_image_url"]);
  const [name, setName] = useState(() => rowString(row, ["name"]) || "station-logo");
  const [watermarkImage, setWatermarkImage] = useState("");
  const [watermarkFileName, setWatermarkFileName] = useState(existingImageName);
  const [fileMessage, setFileMessage] = useState("");
  const editing = Boolean(initial);

  return (
    <form
      className="space-y-3"
      onSubmit={(event) => {
        event.preventDefault();
        submit({
          name,
          config: compactRecord({
            watermark_enabled: true,
            watermark_image_data_url: watermarkImage || undefined,
            watermark_file_name: watermarkImage ? watermarkFileName : existingImageName,
            watermark_canvas_width: watermarkCanvasWidth,
            watermark_canvas_height: watermarkCanvasHeight,
            watermark_fit_mode: "scale_to_output",
          }),
        });
      }}
    >
      <TextField label="プロファイル名" value={name} onChange={setName} required />
      <div className="grid gap-3 md:grid-cols-2">
        <Field label="画像アップロード" description="PNG、JPEG、WebPのみ。1920x1080の画像を保存します。5MB以下にしてください。">
          <Input
            type="file"
            accept="image/png,image/jpeg,image/webp"
            onChange={(event) => {
              const file = event.target.files?.[0];
              if (!file) return;
              if (!["image/png", "image/jpeg", "image/webp"].includes(file.type)) {
                setFileMessage("PNG、JPEG、WebPの画像を選択してください。");
                setWatermarkImage("");
                setWatermarkFileName("");
                return;
              }
              if (file.size > watermarkMaxBytes) {
                setFileMessage("画像は5MB以下にしてください。");
                setWatermarkImage("");
                setWatermarkFileName("");
                return;
              }
              const reader = new FileReader();
              reader.onload = () => {
                if (typeof reader.result !== "string") {
                  setFileMessage("画像を読み込めませんでした。");
                  setWatermarkImage("");
                  setWatermarkFileName("");
                  return;
                }
                const probe = new window.Image();
                probe.onload = () => {
                  if (probe.naturalWidth !== watermarkCanvasWidth || probe.naturalHeight !== watermarkCanvasHeight) {
                    setFileMessage(`画像サイズは${watermarkCanvasWidth}x${watermarkCanvasHeight}にしてください。`);
                    setWatermarkImage("");
                    setWatermarkFileName("");
                    return;
                  }
                  setWatermarkImage(reader.result as string);
                  setWatermarkFileName(file.name);
                  setFileMessage(`${file.name} を読み込みました。配信画質が1080未満の場合は自動でフィットします。`);
                };
                probe.onerror = () => {
                  setFileMessage("画像を読み込めませんでした。");
                  setWatermarkImage("");
                  setWatermarkFileName("");
                };
                probe.src = reader.result;
              };
              reader.onerror = () => setFileMessage("画像を読み込めませんでした。");
              reader.readAsDataURL(file);
            }}
          />
          {fileMessage ? <p className="mt-1 text-xs text-muted-foreground">{fileMessage}</p> : null}
        </Field>
        <Field label="合成サイズ" description="ウォーターマーク画像は配信映像全体に重ねる1920x1080固定です。配信画質が1080未満の場合は自動でフィットします。">
          <div className="rounded-md border bg-muted/40 px-3 py-2 text-sm">1920x1080 / 自動フィット</div>
        </Field>
      </div>
      {editing && existingImageName && !watermarkImage ? <div className="rounded-md border bg-muted/30 px-3 py-2 text-sm text-muted-foreground">現在の画像: {existingImageName}。差し替える場合だけ新しい画像を選択してください。</div> : null}
      <WatermarkPreview image={watermarkImage || existingPreviewImage} imageName={watermarkFileName || existingImageName} />
      <FormActions label={submitLabel} disabled={disabled || (!editing && !watermarkImage)} />
    </form>
  );
}

function WatermarkPreview({ image, imageName }: { image: string; imageName?: string }) {
  return (
    <div className="space-y-2">
      <div className="text-sm font-medium">プレビュー</div>
      <div className="relative aspect-video overflow-hidden rounded-md border bg-slate-950">
        <div className="absolute inset-0 bg-[linear-gradient(135deg,rgba(255,255,255,.08)_25%,transparent_25%,transparent_50%,rgba(255,255,255,.08)_50%,rgba(255,255,255,.08)_75%,transparent_75%,transparent)] bg-[length:24px_24px]" />
        {image ? (
          // eslint-disable-next-line @next/next/no-img-element
          <img
            src={image}
            alt="ウォーターマークプレビュー"
            className="absolute inset-0 h-full w-full object-contain"
          />
        ) : imageName ? (
          <div className="absolute inset-0 flex items-center justify-center px-6 text-center text-sm text-slate-300">
            現在の画像「{imageName}」が設定されています。APIがプレビュー用URLを返した場合はここに画像を表示します。
          </div>
        ) : (
          <div className="absolute inset-0 flex items-center justify-center text-center text-sm text-slate-300">1920x1080の画像を選択するとプレビューされます。</div>
        )}
      </div>
    </div>
  );
}
