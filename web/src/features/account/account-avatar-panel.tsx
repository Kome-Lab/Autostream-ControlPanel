"use client";

import { useEffect, useRef, useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { Camera, Save, Trash2, Upload, X } from "lucide-react";
import { AccountAvatar } from "@/components/ui/account-avatar";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { apiDelete, apiPutBinary } from "@/lib/api/client";
import { AccountActionConfirmation } from "@/features/account/account-action-confirmation";
import { type AccountActionController, type AccountAuthoritySnapshot } from "@/features/account/account-action-policy";
import { type AccountNotice } from "./account-authority";

type AvatarResponse = {
  avatar_url: string;
  content_type: string;
  size_bytes: number;
  updated_at: string;
};

const maxAvatarBytes = 768 * 1024;

const minAvatarDimension = 32;

const maxAvatarDimension = 2048;

export function AvatarPanel({
  username,
  currentAvatarURL,
  setNotice,
  onError,
  refresh,
  actionController,
  authority,
  refreshAuthority,
  accountResourceID,
}: {
  username: string;
  currentAvatarURL?: string;
  setNotice: (notice: AccountNotice) => void;
  onError: (error: unknown, fallback: string) => void;
  refresh: () => void;
  actionController: AccountActionController;
  authority: AccountAuthoritySnapshot;
  refreshAuthority: () => Promise<AccountAuthoritySnapshot>;
  accountResourceID: string;
}) {
  const fileInputRef = useRef<HTMLInputElement>(null);
  const [selectedFile, setSelectedFile] = useState<File | null>(null);
  const [previewURL, setPreviewURL] = useState("");
  const [dimensions, setDimensions] = useState<{ width: number; height: number } | null>(null);

  useEffect(() => () => {
    if (previewURL) URL.revokeObjectURL(previewURL);
  }, [previewURL]);

  const clearSelection = () => {
    setSelectedFile(null);
    setDimensions(null);
    setPreviewURL("");
    if (fileInputRef.current) fileInputRef.current.value = "";
  };

  const upload = useMutation({
    mutationFn: (file: File) => apiPutBinary<AvatarResponse>("/auth/avatar", file),
    onSuccess: async () => {
      clearSelection();
      setNotice({ tone: "success", text: "アカウントアイコンを更新しました。" });
      await refresh();
    },
    onError: (error) => onError(error, "アカウントアイコンを更新できませんでした"),
  });
  const remove = useMutation({
    mutationFn: () => apiDelete<void>("/auth/avatar"),
    onSuccess: async () => {
      clearSelection();
      setNotice({ tone: "success", text: "アカウントアイコンを削除しました。" });
      await refresh();
    },
    onError: (error) => onError(error, "アカウントアイコンを削除できませんでした"),
  });

  const selectFile = async (file?: File) => {
    if (!file) return;
    clearSelection();
    if (!(["image/jpeg", "image/png"] as string[]).includes(file.type)) {
      setNotice({ tone: "error", text: "JPEGまたはPNG画像を選択してください。" });
      return;
    }
    if (file.size > maxAvatarBytes) {
      setNotice({ tone: "error", text: "画像は768 KB以下にしてください。" });
      return;
    }
    try {
      const nextDimensions = await readImageDimensions(file);
      if (nextDimensions.width < minAvatarDimension || nextDimensions.height < minAvatarDimension || nextDimensions.width > maxAvatarDimension || nextDimensions.height > maxAvatarDimension) {
        setNotice({ tone: "error", text: "画像の縦横は32〜2048 pxにしてください。" });
        return;
      }
      const nextURL = URL.createObjectURL(file);
      setPreviewURL(nextURL);
      setSelectedFile(file);
      setDimensions(nextDimensions);
      setNotice(null);
    } catch {
      setNotice({ tone: "error", text: "画像を読み込めませんでした。別の画像を選択してください。" });
    }
  };

  return (
    <Card className="h-fit">
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-lg"><Camera className="size-5" />アカウントアイコン</CardTitle>
        <CardDescription>ヘッダーとアカウントメニューに表示する画像です。</CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="flex flex-col items-center gap-4 rounded-md border bg-muted/20 p-4 text-center sm:flex-row sm:text-left">
          <AccountAvatar name={username} src={previewURL || currentAvatarURL} alt={previewURL ? "選択したアカウントアイコンのプレビュー" : `${username}のアカウントアイコン`} className="size-24" sizes="96px" />
          <div className="min-w-0 flex-1 space-y-2">
            <div>
              <div className="text-sm font-medium">{previewURL ? "変更後のプレビュー" : currentAvatarURL ? "現在のアイコン" : "アイコン未設定"}</div>
              <div className="mt-1 text-xs text-muted-foreground">JPEG / PNG、768 KB以下、32〜2048 px</div>
            </div>
            {selectedFile ? (
              <div className="text-xs text-muted-foreground">
                <div className="truncate font-medium text-foreground">{selectedFile.name}</div>
                <div>{formatFileSize(selectedFile.size)}{dimensions ? ` / ${dimensions.width}×${dimensions.height} px` : ""}</div>
              </div>
            ) : null}
          </div>
        </div>
        <input
          ref={fileInputRef}
          type="file"
          accept="image/png,image/jpeg"
          className="sr-only"
          aria-label="アカウントアイコン画像を選択"
          onChange={(event) => void selectFile(event.target.files?.[0])}
        />
        <div className="flex flex-wrap gap-2">
          <Button type="button" variant="outline" onClick={() => fileInputRef.current?.click()} disabled={upload.isPending || remove.isPending}>
            <Upload />画像を選択
          </Button>
          {selectedFile ? (
            <>
              <Button type="button" onClick={() => upload.mutate(selectedFile)} disabled={upload.isPending}>
                <Save />{upload.isPending ? "保存中" : "この画像を保存"}
              </Button>
              <Button type="button" variant="ghost" size="icon" aria-label="画像の選択を取り消す" onClick={clearSelection} disabled={upload.isPending}>
                <X />
              </Button>
            </>
          ) : null}
          {!selectedFile && currentAvatarURL ? (
            <AccountActionConfirmation
              controller={actionController}
              intent={{ id: "AUTH-10", resourceId: accountResourceID, authorityRevision: authority.revision }}
              authority={authority}
              refreshAuthority={refreshAuthority}
              label="削除"
              icon={<Trash2 />}
              variant="outline"
              disabled={remove.isPending}
              handler={() => remove.mutateAsync()}
            />
          ) : null}
        </div>
      </CardContent>
    </Card>
  );
}

function readImageDimensions(file: File) {
  return new Promise<{ width: number; height: number }>((resolve, reject) => {
    const objectURL = URL.createObjectURL(file);
    const image = new window.Image();
    image.onload = () => {
      URL.revokeObjectURL(objectURL);
      resolve({ width: image.naturalWidth, height: image.naturalHeight });
    };
    image.onerror = () => {
      URL.revokeObjectURL(objectURL);
      reject(new Error("invalid image"));
    };
    image.src = objectURL;
  });
}

function formatFileSize(size: number) {
  if (size < 1024) return `${size} B`;
  return `${Math.round(size / 1024)} KB`;
}
