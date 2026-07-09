"use client";

import { type ReactNode, useEffect, useMemo, useState } from "react";
import Image from "next/image";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Plus, RefreshCcw, Trash2 } from "lucide-react";
import { DangerConfirm } from "@/components/admin/danger-confirm";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Textarea } from "@/components/ui/textarea";
import { APIError, apiDelete, apiPost, apiPut } from "@/lib/api/client";
import { useI18n } from "@/components/admin/i18n-provider";
import { useResourceData } from "@/features/queries";
import { resourcePages, type ResourceDefinition, type ResourcePageId } from "@/features/resources/resource-config";

const watermarkCanvasWidth = 1920;
const watermarkCanvasHeight = 1080;
const watermarkMaxBytes = 5 * 1024 * 1024;

export function ResourcePage({ pageId }: { pageId: ResourcePageId }) {
  const { t } = useI18n();
  const page = resourcePages[pageId];
  const defaultTab = page.resources[0]?.path || "";

  return (
    <div className="space-y-6">
      <section>
        <h1 className="text-2xl font-semibold tracking-normal">{t(page.titleKey)}</h1>
        <p className="mt-2 max-w-3xl text-sm text-muted-foreground">{page.description}</p>
      </section>

      {page.resources.length === 1 ? (
        <ResourcePanel resource={page.resources[0]} />
      ) : (
        <Tabs defaultValue={defaultTab} className="space-y-4">
          <TabsList className="max-w-full flex-wrap justify-start">
            {page.resources.map((resource) => (
              <TabsTrigger key={resource.path} value={resource.path}>
                {resource.title}
              </TabsTrigger>
            ))}
          </TabsList>
          {page.resources.map((resource) => (
            <TabsContent key={resource.path} value={resource.path}>
              <ResourcePanel resource={resource} />
            </TabsContent>
          ))}
        </Tabs>
      )}
    </div>
  );
}

function ResourcePanel({ resource }: { resource: ResourceDefinition }) {
  const queryClient = useQueryClient();
  const query = useResourceData<unknown>(resource.path);
  const rows = useMemo(() => normalizeRows(query.data).map((row) => enrichResourceRow(resource, row)), [query.data, resource]);
  const columns = useMemo(() => visibleColumns(rows, resource), [rows, resource]);
  const [deleteMessage, setDeleteMessage] = useState("");
  const deleteMutation = useMutation<unknown, Error, ResourceRow>({
    mutationFn: async (row) => apiDelete(deletePathForResource(resource, row)),
    onSuccess: async () => {
      setDeleteMessage("削除しました。");
      await queryClient.invalidateQueries({ queryKey: ["resource", resource.path] });
    },
    onError: (error) => setDeleteMessage(resourceDeleteErrorMessage(error)),
  });

  return (
    <Card>
      <CardHeader className="gap-2 sm:flex-row sm:items-start sm:justify-between">
        <div>
          <CardTitle>{resource.title}</CardTitle>
          <CardDescription>{resource.description}</CardDescription>
        </div>
        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm" onClick={() => query.refetch()}>
            <RefreshCcw className="size-4" />
            更新
          </Button>
        </div>
      </CardHeader>
      <CardContent className="space-y-4">
        {resource.form === "security-settings" ? <SecuritySettingsEditor resource={resource} data={query.data} loading={query.isLoading} /> : null}
        {resource.form && resource.form !== "security-settings" ? <CreateResourceForm resource={resource} /> : null}
        {deleteMessage ? <p className="text-sm text-muted-foreground">{deleteMessage}</p> : null}
        {query.isLoading ? (
          <Skeleton className="h-48 w-full" />
        ) : (
          <ResourceTable
            rows={rows}
            columns={columns}
            resource={resource}
            deletePending={deleteMutation.isPending}
            onDelete={(row) => {
              setDeleteMessage("");
              deleteMutation.mutate(row);
            }}
          />
        )}
      </CardContent>
    </Card>
  );
}

type ResourceRow = Record<string, unknown>;
type SelectOption = { value: string; label: string; description?: string; group?: string };
type SubmitOptions = {
  path?: string;
  invalidatePath?: string;
  successMessage?: string;
  redirectToAuthorizationURL?: boolean;
};
type Submission = {
  path: string;
  payload: Record<string, unknown>;
  invalidatePath: string;
  successMessage: string;
  redirectToAuthorizationURL?: boolean;
};
type SubmitResource = (payload: Record<string, unknown>, options?: SubmitOptions) => void;

const noneValue = "__none__";

function CreateResourceForm({ resource }: { resource: ResourceDefinition }) {
  const queryClient = useQueryClient();
  const [open, setOpen] = useState(false);
  const [message, setMessage] = useState("");
  const mutation = useMutation<unknown, Error, Submission>({
    mutationFn: async (submission) => apiPost(submission.path, submission.payload),
    onSuccess: async (data, submission) => {
      setMessage(submission.successMessage);
      await queryClient.invalidateQueries({ queryKey: ["resource", submission.invalidatePath] });
      if (submission.redirectToAuthorizationURL) {
        const authorizationURL = isRecord(data) && typeof data.authorization_url === "string" ? data.authorization_url : "";
        if (authorizationURL && typeof window !== "undefined") {
          window.location.assign(authorizationURL);
        } else {
          setMessage("OAuth認可URLを取得できませんでした。プロバイダ設定を確認してください。");
        }
      }
    },
    onError: (error) => setMessage(`作成に失敗しました。入力内容と権限を確認してください。${error.message ? ` (${error.message})` : ""}`),
  });

  const submit: SubmitResource = (payload, options) => {
    setMessage("");
    mutation.mutate({
      path: options?.path || resource.path,
      payload,
      invalidatePath: options?.invalidatePath || resource.path,
      successMessage: options?.successMessage || "作成しました。",
      redirectToAuthorizationURL: options?.redirectToAuthorizationURL,
    });
  };

  return (
    <div className="rounded-md border bg-muted/20 p-3">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <div className="font-medium">新規作成</div>
          <p className="text-sm text-muted-foreground">必要項目をフォームで入力して作成します。秘密情報はAPI側のシークレットストアに保存されます。</p>
        </div>
        <Button variant="outline" size="sm" onClick={() => setOpen((value) => !value)}>
          <Plus className="size-4" />
          {open ? "閉じる" : "開く"}
        </Button>
      </div>
      {open ? (
        <div className="mt-3 space-y-3">
          <ResourceFormFields resource={resource} disabled={mutation.isPending} submit={submit} />
          {message ? <p className="text-sm text-muted-foreground">{message}</p> : null}
        </div>
      ) : null}
    </div>
  );
}

function ResourceFormFields({ resource, disabled, submit }: { resource: ResourceDefinition; disabled: boolean; submit: SubmitResource }) {
  switch (resource.form) {
    case "encoder-profile":
      return <EncoderProfileForm disabled={disabled} submit={submit} />;
    case "discord-config":
      return <DiscordConfigForm disabled={disabled} submit={submit} />;
    case "youtube-output":
      return <YouTubeOutputForm disabled={disabled} submit={submit} />;
    case "caption-profile":
      return <CaptionProfileForm disabled={disabled} submit={submit} />;
    case "overlay-profile":
      return <OverlayProfileForm disabled={disabled} submit={submit} />;
    case "archive-profile":
      return <ArchiveProfileForm disabled={disabled} submit={submit} />;
    case "drive-destination":
      return <DriveDestinationForm disabled={disabled} submit={submit} />;
    case "oauth-provider":
      return <OAuthProviderForm disabled={disabled} submit={submit} />;
    case "oauth-account-connect":
      return <OAuthAccountConnectForm disabled={disabled} submit={submit} />;
    case "user":
      return <UserForm disabled={disabled} submit={submit} />;
    case "role":
      return <RoleForm disabled={disabled} submit={submit} />;
    case "notification-channel":
      return <NotificationChannelForm disabled={disabled} submit={submit} />;
    case "security-settings":
      return null;
    default:
      return <p className="text-sm text-muted-foreground">このリソースは一覧確認のみ対応しています。</p>;
  }
}

function EncoderProfileForm({ disabled, submit }: { disabled: boolean; submit: SubmitResource }) {
  const [name, setName] = useState("1080p60");
  const [width, setWidth] = useState("1920");
  const [height, setHeight] = useState("1080");
  const [fps, setFps] = useState("60");
  const [videoBitrate, setVideoBitrate] = useState("8000");
  const [audioBitrate, setAudioBitrate] = useState("192");

  return (
    <form
      className="space-y-3"
      onSubmit={(event) => {
        event.preventDefault();
        submit({
          name,
          config: {
            width: numberValue(width, 1920),
            height: numberValue(height, 1080),
            fps: numberValue(fps, 60),
            video_bitrate_kbps: numberValue(videoBitrate, 8000),
            audio_bitrate_kbps: numberValue(audioBitrate, 192),
          },
        });
      }}
    >
      <div className="grid gap-3 md:grid-cols-2">
        <TextField label="プロファイル名" value={name} onChange={setName} required />
        <NumberField label="映像ビットレート (kbps)" value={videoBitrate} onChange={setVideoBitrate} min={1} required />
        <NumberField label="横解像度" value={width} onChange={setWidth} min={1} required />
        <NumberField label="縦解像度" value={height} onChange={setHeight} min={1} required />
        <NumberField label="フレームレート" value={fps} onChange={setFps} min={1} required />
        <NumberField label="音声ビットレート (kbps)" value={audioBitrate} onChange={setAudioBitrate} min={1} required />
      </div>
      <FormActions disabled={disabled} />
    </form>
  );
}

function DiscordConfigForm({ disabled, submit }: { disabled: boolean; submit: SubmitResource }) {
  const discordNodes = useRegisteredNodeOptions("discord_bot");
  const [name, setName] = useState("main-discord-bot");
  const [serviceID, setServiceID] = useState(noneValue);
  const [botToken, setBotToken] = useState("");
  const [reconnectEnabled, setReconnectEnabled] = useState(true);
  const [reconnectMaxAttempts, setReconnectMaxAttempts] = useState("5");
  const [audioForwardEnabled, setAudioForwardEnabled] = useState(true);
  const effectiveServiceID = serviceID === noneValue && discordNodes[0]?.value ? discordNodes[0].value : serviceID;

  return (
    <form
      className="space-y-3"
      onSubmit={(event) => {
        event.preventDefault();
        submit(
          compactRecord({
            name,
            service_id: effectiveServiceID === noneValue ? "" : effectiveServiceID,
            bot_token: botToken,
            reconnect_enabled: reconnectEnabled,
            reconnect_max_attempts: numberValue(reconnectMaxAttempts, 5),
            reconnect_base_delay: "2s",
            reconnect_max_delay: "30s",
            audio_forward_enabled: audioForwardEnabled,
          }),
        );
      }}
    >
      <div className="grid gap-3 md:grid-cols-2">
        <TextField label="BOT設定名" value={name} onChange={setName} required />
        <SelectField
          key={discordNodes.map((node) => node.value).join("|") || "no-discord-nodes"}
          label="Discord BOT Node"
          value={effectiveServiceID}
          onChange={setServiceID}
          options={[{ value: noneValue, label: "未選択" }, ...discordNodes]}
        />
        <TextField label="Bot Token" value={botToken} onChange={setBotToken} type="password" description="入力した場合のみ保存します。" />
        <NumberField label="再接続最大回数" value={reconnectMaxAttempts} onChange={setReconnectMaxAttempts} min={0} />
      </div>
      {discordNodes.length === 0 ? <p className="text-sm text-muted-foreground">先にNode登録でDiscord Bot Nodeを登録してください。</p> : null}
      <div className="grid gap-3 md:grid-cols-2">
        <SwitchField label="音声転送" checked={audioForwardEnabled} onCheckedChange={setAudioForwardEnabled} />
        <SwitchField label="自動再接続" checked={reconnectEnabled} onCheckedChange={setReconnectEnabled} />
      </div>
      <FormActions disabled={disabled || effectiveServiceID === noneValue} />
    </form>
  );
}

function YouTubeOutputForm({ disabled, submit }: { disabled: boolean; submit: SubmitResource }) {
  const oauthAccounts = useResourceOptions("/integrations/oauth-accounts", ["id"], ["account_label", "email", "id"], ["email", "provider_type"]);
  const [name, setName] = useState("public-live");
  const [mode, setMode] = useState("live_api_dry_run");
  const [rtmpURL, setRTMPURL] = useState("rtmps://a.rtmps.youtube.com/live2");
  const [streamKey, setStreamKey] = useState("");
  const [oauthAccountID, setOAuthAccountID] = useState(noneValue);
  const [privacyStatus, setPrivacyStatus] = useState("public");
  const [latencyPreference, setLatencyPreference] = useState("low");
  const [titleTemplate, setTitleTemplate] = useState("{{program_title}}");
  const [description, setDescription] = useState("");
  const [autoStart, setAutoStart] = useState(true);
  const [autoStop, setAutoStop] = useState(true);
  const [completeOnStop, setCompleteOnStop] = useState(true);
  const requiresOAuth = mode === "live_api" || mode === "live_api_dry_run";
  const effectiveOAuthAccountID = requiresOAuth && oauthAccountID === noneValue && oauthAccounts[0]?.value ? oauthAccounts[0].value : oauthAccountID;

  return (
    <form
      className="space-y-3"
      onSubmit={(event) => {
        event.preventDefault();
        submit(
          compactRecord({
            name,
            mode,
            rtmp_url: rtmpURL,
            stream_key: streamKey,
            oauth_account_id: effectiveOAuthAccountID === noneValue ? "" : effectiveOAuthAccountID,
            broadcast_title_template: titleTemplate,
            broadcast_description: description,
            privacy_status: privacyStatus,
            latency_preference: latencyPreference,
            enable_auto_start: autoStart,
            enable_auto_stop: autoStop,
            complete_on_stop: completeOnStop,
          }),
        );
      }}
    >
      <div className="grid gap-3 md:grid-cols-2">
        <TextField label="出力名" value={name} onChange={setName} required />
        <SelectField
          label="出力方式"
          value={mode}
          onChange={setMode}
          options={[
            { value: "live_api_dry_run", label: "YouTube Live API検証" },
            { value: "live_api", label: "YouTube Live API本番" },
            { value: "stream_key", label: "既存ストリームキー" },
          ]}
        />
        <TextField label="RTMP URL" value={rtmpURL} onChange={setRTMPURL} required />
        <SelectField label="接続済みGoogleアカウント" value={effectiveOAuthAccountID} onChange={setOAuthAccountID} options={[{ value: noneValue, label: "未選択" }, ...oauthAccounts]} />
        <TextField label="ストリームキー" value={streamKey} onChange={setStreamKey} type="password" description="既存ストリームキー方式で使う場合だけ入力します。" />
        <TextField label="番組タイトルテンプレート" value={titleTemplate} onChange={setTitleTemplate} />
        <SelectField
          label="公開範囲"
          value={privacyStatus}
          onChange={setPrivacyStatus}
          options={[
            { value: "public", label: "公開" },
            { value: "unlisted", label: "限定公開" },
            { value: "private", label: "非公開" },
          ]}
        />
        <SelectField
          label="遅延設定"
          value={latencyPreference}
          onChange={setLatencyPreference}
          options={[
            { value: "normal", label: "標準" },
            { value: "low", label: "低遅延" },
            { value: "ultra_low", label: "超低遅延" },
          ]}
        />
      </div>
      <Field label="説明">
        <Textarea value={description} onChange={(event) => setDescription(event.target.value)} className="min-h-24" />
      </Field>
      <div className="grid gap-3 md:grid-cols-3">
        <SwitchField label="自動開始" checked={autoStart} onCheckedChange={setAutoStart} />
        <SwitchField label="自動停止" checked={autoStop} onCheckedChange={setAutoStop} />
        <SwitchField label="停止時に完了扱い" checked={completeOnStop} onCheckedChange={setCompleteOnStop} />
      </div>
      {requiresOAuth && oauthAccounts.length === 0 ? <p className="text-sm text-muted-foreground">YouTube Live APIを使うには、先にOAuth接続アカウントでGoogleアカウントを接続してください。</p> : null}
      <FormActions disabled={disabled || (requiresOAuth && effectiveOAuthAccountID === noneValue)} />
    </form>
  );
}

function CaptionProfileForm({ disabled, submit }: { disabled: boolean; submit: SubmitResource }) {
  const [name, setName] = useState("日本語ライブ字幕");
  const [language, setLanguage] = useState("ja-JP");
  const [provider, setProvider] = useState("deepgram");
  const [delayMs, setDelayMs] = useState("800");

  return (
    <form
      className="space-y-3"
      onSubmit={(event) => {
        event.preventDefault();
        submit({ name, config: { language, provider, delay_ms: numberValue(delayMs, 800) } });
      }}
    >
      <div className="grid gap-3 md:grid-cols-2">
        <TextField label="プロファイル名" value={name} onChange={setName} required />
        <SelectField
          label="言語"
          value={language}
          onChange={setLanguage}
          options={[
            { value: "ja-JP", label: "日本語" },
            { value: "en-US", label: "英語" },
          ]}
        />
        <SelectField
          label="プロバイダ"
          value={provider}
          onChange={setProvider}
          options={[
            { value: "deepgram", label: "Deepgram" },
            { value: "manual", label: "手動字幕" },
          ]}
        />
        <NumberField label="遅延補正 (ms)" value={delayMs} onChange={setDelayMs} min={0} />
      </div>
      <FormActions disabled={disabled} />
    </form>
  );
}

function OverlayProfileForm({ disabled, submit }: { disabled: boolean; submit: SubmitResource }) {
  const [name, setName] = useState("station-logo");
  const [watermarkEnabled, setWatermarkEnabled] = useState(true);
  const [watermarkImage, setWatermarkImage] = useState("");
  const [watermarkFileName, setWatermarkFileName] = useState("");
  const [fileMessage, setFileMessage] = useState("");

  return (
    <form
      className="space-y-3"
      onSubmit={(event) => {
        event.preventDefault();
        submit({
          name,
          config: compactRecord({
            watermark_enabled: watermarkEnabled,
            watermark_image_data_url: watermarkEnabled ? watermarkImage : "",
            watermark_file_name: watermarkEnabled ? watermarkFileName : "",
            watermark_canvas_width: watermarkEnabled ? watermarkCanvasWidth : undefined,
            watermark_canvas_height: watermarkEnabled ? watermarkCanvasHeight : undefined,
            watermark_fit_mode: watermarkEnabled ? "scale_to_output" : "",
          }),
        });
      }}
    >
      <TextField label="プロファイル名" value={name} onChange={setName} required />
      <div className="grid gap-3 md:grid-cols-2">
        <SwitchField label="ウォーターマークを表示" checked={watermarkEnabled} onCheckedChange={setWatermarkEnabled} />
        {watermarkEnabled ? (
          <>
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
            <Field label="合成サイズ" description="ウォーターマーク画像は配信映像全体に重ねる1920x1080固定です。">
              <div className="rounded-md border bg-muted/40 px-3 py-2 text-sm">1920x1080 / 自動フィット</div>
            </Field>
          </>
        ) : null}
      </div>
      {watermarkEnabled ? <WatermarkPreview image={watermarkImage} /> : null}
      <FormActions disabled={disabled || (watermarkEnabled && !watermarkImage)} />
    </form>
  );
}

function WatermarkPreview({ image }: { image: string }) {
  return (
    <div className="space-y-2">
      <div className="text-sm font-medium">プレビュー</div>
      <div className="relative aspect-video overflow-hidden rounded-md border bg-slate-950">
        <div className="absolute inset-0 bg-[linear-gradient(135deg,rgba(255,255,255,.08)_25%,transparent_25%,transparent_50%,rgba(255,255,255,.08)_50%,rgba(255,255,255,.08)_75%,transparent_75%,transparent)] bg-[length:24px_24px]" />
        {image ? (
          <Image
            src={image}
            alt="ウォーターマークプレビュー"
            width={watermarkCanvasWidth}
            height={watermarkCanvasHeight}
            unoptimized
            className="absolute inset-0 h-full w-full object-contain"
          />
        ) : (
          <div className="absolute inset-0 flex items-center justify-center text-center text-sm text-slate-300">1920x1080の画像を選択するとプレビューされます。</div>
        )}
      </div>
    </div>
  );
}

function ArchiveProfileForm({ disabled, submit }: { disabled: boolean; submit: SubmitResource }) {
  const driveDestinations = useResourceOptions("/archive/destinations", ["id"], ["name", "id"]);
  const [name, setName] = useState("shared-drive");
  const [format, setFormat] = useState("mp4");
  const [retentionDays, setRetentionDays] = useState("180");
  const [uploadEnabled, setUploadEnabled] = useState(true);
  const [driveDestinationID, setDriveDestinationID] = useState(noneValue);
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
      <FormActions disabled={disabled} />
    </form>
  );
}

function DriveDestinationForm({ disabled, submit }: { disabled: boolean; submit: SubmitResource }) {
  const oauthAccounts = useResourceOptions("/integrations/oauth-accounts", ["id"], ["account_label", "email", "id"], ["email", "provider_type"]);
  const [name, setName] = useState("archive-drive");
  const [oauthAccountID, setOAuthAccountID] = useState(noneValue);
  const [folderID, setFolderID] = useState("");
  const [sharedDrive, setSharedDrive] = useState(false);
  const [basePath, setBasePath] = useState("autostream");
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
            base_path: basePath,
          }),
        );
      }}
    >
      <div className="grid gap-3 md:grid-cols-2">
        <TextField label="保存先名" value={name} onChange={setName} required />
        <SelectField label="OAuthアカウント" value={effectiveOAuthAccountID} onChange={setOAuthAccountID} options={[{ value: noneValue, label: "未選択" }, ...oauthAccounts]} />
        <TextField label={sharedDrive ? "共有ドライブ配下のフォルダID" : "DriveフォルダID"} value={folderID} onChange={setFolderID} required description="URLのfolders/以降にあるIDを入力します。" />
        <TextField label="保存先パス" value={basePath} onChange={setBasePath} />
      </div>
      <SwitchField label="共有ドライブを使う" checked={sharedDrive} onCheckedChange={setSharedDrive} />
      {oauthAccounts.length === 0 ? <p className="text-sm text-muted-foreground">先にOAuth接続アカウントでGoogleアカウントを接続してください。</p> : null}
      <FormActions disabled={disabled || effectiveOAuthAccountID === noneValue} />
    </form>
  );
}

function OAuthProviderForm({ disabled, submit }: { disabled: boolean; submit: SubmitResource }) {
  const roles = useResourceOptions("/roles", ["id"], ["name", "id"], ["permissions"]);
  const defaultRedirectURI = typeof window === "undefined" ? "https://control.example.jp/auth/oauth/callback" : `${window.location.origin}/auth/oauth/callback`;
  const [providerType, setProviderType] = useState("google");
  const [name, setName] = useState("Google Workspace");
  const [enabled, setEnabled] = useState(true);
  const [clientID, setClientID] = useState("");
  const [clientSecret, setClientSecret] = useState("");
  const [redirectURI, setRedirectURI] = useState(defaultRedirectURI);
  const [allowedDomains, setAllowedDomains] = useState("");
  const [autoProvision, setAutoProvision] = useState(false);
  const [defaultRoleIDs, setDefaultRoleIDs] = useState<string[]>([]);

  return (
    <form
      className="space-y-3"
      onSubmit={(event) => {
        event.preventDefault();
        submit({
          provider_type: providerType,
          name,
          enabled,
          client_id: clientID,
          client_secret: clientSecret,
          redirect_uri: redirectURI,
          allowed_domains: splitList(allowedDomains),
          auto_provision: autoProvision,
          default_role_ids: defaultRoleIDs,
        });
      }}
    >
      <div className="grid gap-3 md:grid-cols-2">
        <SelectField
          label="プロバイダ"
          value={providerType}
          onChange={setProviderType}
          options={[
            { value: "google", label: "Google" },
            { value: "github", label: "GitHub" },
            { value: "discord", label: "Discord" },
          ]}
        />
        <TextField label="表示名" value={name} onChange={setName} required />
        <TextField label="Client ID" value={clientID} onChange={setClientID} required />
        <TextField label="Client Secret" value={clientSecret} onChange={setClientSecret} type="password" description="入力値は保存後に再表示しません。" />
        <TextField label="Redirect URI" value={redirectURI} onChange={setRedirectURI} required />
      </div>
      <Field label="許可ドメイン" description="複数ある場合は改行またはカンマで区切ります。空なら制限しません。">
        <Textarea value={allowedDomains} onChange={(event) => setAllowedDomains(event.target.value)} className="min-h-20" placeholder="example.jp" />
      </Field>
      <div className="grid gap-3 md:grid-cols-2">
        <SwitchField label="有効化" checked={enabled} onCheckedChange={setEnabled} />
        <SwitchField label="初回ログイン時に自動ユーザー作成" checked={autoProvision} onCheckedChange={setAutoProvision} />
      </div>
      {autoProvision ? <CheckboxList label="自動作成ユーザーのロール" values={defaultRoleIDs} onChange={setDefaultRoleIDs} items={roles} emptyText="ロールがありません。" /> : null}
      <FormActions disabled={disabled || (autoProvision && defaultRoleIDs.length === 0)} />
    </form>
  );
}

function OAuthAccountConnectForm({ disabled, submit }: { disabled: boolean; submit: SubmitResource }) {
  const providerRows = useResourceRows("/integrations/oauth-providers");
  const providerOptions = useMemo(
    () =>
      providerRows
        .filter((row) => rowString(row, ["provider_type"]) === "google")
        .map((row) => ({
          value: rowString(row, ["id"]),
          label: firstNonEmpty(rowString(row, ["name"]), rowString(row, ["id"])),
          description: rowString(row, ["redirect_uri"]),
        }))
        .filter((option) => option.value),
    [providerRows],
  );
  const [providerID, setProviderID] = useState(noneValue);
  const [accountLabel, setAccountLabel] = useState("配信・アーカイブ用Google");
  const [accountPurpose, setAccountPurpose] = useState("drive_youtube");
  const effectiveProviderID = providerID === noneValue && providerOptions[0]?.value ? providerOptions[0].value : providerID;

  return (
    <form
      className="space-y-3"
      onSubmit={(event) => {
        event.preventDefault();
        submit(
          {
            provider_id: effectiveProviderID === noneValue ? "" : effectiveProviderID,
            account_label: accountLabel,
            account_purpose: accountPurpose,
            redirect_after: "/admin/integrations/",
          },
          {
            path: "/integrations/oauth-accounts/start",
            invalidatePath: "/integrations/oauth-accounts",
            successMessage: "OAuth認可画面へ移動します。",
            redirectToAuthorizationURL: true,
          },
        );
      }}
    >
      <div className="grid gap-3 md:grid-cols-2">
        <SelectField label="Google OAuthプロバイダ" value={effectiveProviderID} onChange={setProviderID} options={[{ value: noneValue, label: "未選択" }, ...providerOptions]} />
        <TextField label="アカウント表示名" value={accountLabel} onChange={setAccountLabel} required />
        <SelectField
          label="用途"
          value={accountPurpose}
          onChange={setAccountPurpose}
          options={[
            { value: "drive_youtube", label: "YouTubeとDrive" },
            { value: "youtube", label: "YouTube Liveのみ" },
            { value: "drive", label: "Archive保存のみ" },
          ]}
        />
      </div>
      {providerOptions.length === 0 ? <p className="text-sm text-muted-foreground">先にOAuthログインプロバイダでGoogleプロバイダを登録し、有効化してください。</p> : null}
      <FormActions label="OAuth接続を開始" disabled={disabled || effectiveProviderID === noneValue} />
    </form>
  );
}

function UserForm({ disabled, submit }: { disabled: boolean; submit: SubmitResource }) {
  const roles = useResourceOptions("/roles", ["id"], ["name", "id"], ["permissions"]);
  const [username, setUsername] = useState("operator");
  const [email, setEmail] = useState("operator@example.jp");
  const [temporaryPassword, setTemporaryPassword] = useState("");
  const [roleIDs, setRoleIDs] = useState<string[]>([]);
  const [sendWelcomeEmail, setSendWelcomeEmail] = useState(false);

  return (
    <form
      className="space-y-3"
      onSubmit={(event) => {
        event.preventDefault();
        submit({ username, email, temporary_password: temporaryPassword, role_ids: roleIDs, send_welcome_email: sendWelcomeEmail });
      }}
    >
      <div className="grid gap-3 md:grid-cols-2">
        <TextField label="ユーザー名" value={username} onChange={setUsername} required />
        <TextField label="メールアドレス" value={email} onChange={setEmail} type="email" description="登録完了メールと本人確認用の連絡先です。" required />
        <TextField label="初期パスワード" value={temporaryPassword} onChange={setTemporaryPassword} type="password" required description="ログイン後に変更してもらう一時パスワードです。" />
      </div>
      <label className="flex items-center gap-2 text-sm">
        <Switch checked={sendWelcomeEmail} onCheckedChange={setSendWelcomeEmail} />
        登録完了メールを送る
      </label>
      <CheckboxList label="付与するロール" values={roleIDs} onChange={setRoleIDs} items={roles} emptyText="ロールがありません。" />
      <FormActions disabled={disabled || email.trim() === ""} />
    </form>
  );
}

function RoleForm({ disabled, submit }: { disabled: boolean; submit: SubmitResource }) {
  const permissionRows = useResourceRows("/permissions");
  const permissionOptions = useMemo(() => permissionRows.map(permissionOptionFromRow).filter((option) => option.value), [permissionRows]);
  const [name, setName] = useState("operator");
  const [permissions, setPermissions] = useState<string[]>(["streams.read", "streams.start", "streams.stop"]);

  return (
    <form
      className="space-y-3"
      onSubmit={(event) => {
        event.preventDefault();
        submit({ name, permissions });
      }}
    >
      <TextField label="ロール名" value={name} onChange={setName} required />
      <GroupedCheckboxList label="許可する操作" values={permissions} onChange={setPermissions} items={permissionOptions} emptyText="権限一覧を取得できませんでした。" />
      <FormActions disabled={disabled || permissions.length === 0} />
    </form>
  );
}

function NotificationChannelForm({ disabled, submit }: { disabled: boolean; submit: SubmitResource }) {
  const [name, setName] = useState("ops-discord");
  const [type, setType] = useState("discord");
  const [webhookURL, setWebhookURL] = useState("");
  const [emailRecipients, setEmailRecipients] = useState("ops@example.jp");
  const [smtpHost, setSMTPHost] = useState("");
  const [smtpPort, setSMTPPort] = useState("587");
  const [smtpTLS, setSMTPTLS] = useState(true);
  const [smtpFrom, setSMTPFrom] = useState("");
  const [smtpUsername, setSMTPUsername] = useState("");
  const [smtpPassword, setSMTPPassword] = useState("");
  const [severityFilter, setSeverityFilter] = useState<string[]>(["critical", "error", "warning"]);
  const [eventTypeFilter, setEventTypeFilter] = useState<string[]>(["incident.opened"]);
  const [enabled, setEnabled] = useState(true);
  const emailRecipientList = splitList(emailRecipients);
  const webhookRequired = type === "discord" || type === "slack";
  const emailRequired = type === "email";

  return (
    <form
      className="space-y-3"
      onSubmit={(event) => {
        event.preventDefault();
        submit(
          compactRecord({
            name,
            type,
            webhook_url: webhookRequired ? webhookURL : "",
            email_recipients: emailRequired ? emailRecipientList : undefined,
            smtp_host: emailRequired ? smtpHost : "",
            smtp_port: emailRequired ? numberValue(smtpPort, 587) : undefined,
            smtp_tls: emailRequired ? smtpTLS : undefined,
            smtp_from: emailRequired ? smtpFrom : "",
            smtp_username: emailRequired ? smtpUsername : "",
            smtp_password: emailRequired ? smtpPassword : "",
            severity_filter: severityFilter,
            event_type_filter: eventTypeFilter,
            enabled,
          }),
        );
      }}
    >
      <div className="grid gap-3 md:grid-cols-2">
        <TextField label="通知先名" value={name} onChange={setName} required />
        <SelectField
          label="通知方式"
          value={type}
          onChange={setType}
          options={[
            { value: "discord", label: "Discord Webhook" },
            { value: "slack", label: "Slack Webhook" },
            { value: "email", label: "メール" },
          ]}
        />
        {webhookRequired ? (
          <TextField
            label="Webhook URL"
            value={webhookURL}
            onChange={setWebhookURL}
            type="password"
            description={type === "slack" ? "Slack は hooks.slack.com のIncoming Webhook URLを指定します。" : "保存後はURLの実値を表示しません。"}
            required
          />
        ) : null}
      </div>
      {emailRequired ? (
        <div className="grid gap-3 md:grid-cols-2">
          <Field label="送信先メール" description="複数指定する場合は改行またはカンマで区切ります。">
            <Textarea value={emailRecipients} onChange={(event) => setEmailRecipients(event.target.value)} className="min-h-20" required />
          </Field>
          <TextField label="SMTP Host" value={smtpHost} onChange={setSMTPHost} placeholder="smtp.example.jp" required />
          <NumberField label="SMTP Port" value={smtpPort} onChange={setSMTPPort} min={1} required />
          <TextField label="From" value={smtpFrom} onChange={setSMTPFrom} type="email" placeholder="autostream@example.jp" required />
          <TextField label="SMTP Username" value={smtpUsername} onChange={setSMTPUsername} />
          <TextField label="SMTP Password" value={smtpPassword} onChange={setSMTPPassword} type="password" description="保存後は再表示されません。" />
          <div className="md:col-span-2">
            <SwitchField label="STARTTLSを使用する" checked={smtpTLS} onCheckedChange={setSMTPTLS} />
          </div>
        </div>
      ) : null}
      <CheckboxList
        label="通知する重要度"
        values={severityFilter}
        onChange={setSeverityFilter}
        items={[
          { value: "critical", label: "Critical" },
          { value: "error", label: "Error" },
          { value: "warning", label: "Warning" },
          { value: "info", label: "Info" },
        ]}
      />
      <CheckboxList
        label="通知するイベント"
        values={eventTypeFilter}
        onChange={setEventTypeFilter}
        items={[
          { value: "incident.opened", label: "Incident opened" },
          { value: "incident.updated", label: "Incident updated" },
          { value: "incident.resolved", label: "Incident resolved" },
          { value: "diagnostic.created", label: "Diagnostic created" },
          { value: "remediation.pending_approval", label: "Remediation pending approval" },
          { value: "remediation.executed", label: "Remediation executed" },
        ]}
      />
      <SwitchField label="有効化" checked={enabled} onCheckedChange={setEnabled} />
      <FormActions disabled={disabled || (webhookRequired && webhookURL.trim() === "") || (emailRequired && (emailRecipientList.length === 0 || smtpHost.trim() === "" || smtpFrom.trim() === ""))} />
    </form>
  );
}

type SecuritySettingsPayload = {
  password_min_length: number;
  password_hash: "argon2id";
  login_lockout_threshold: number;
  session_idle_timeout_min: number;
  session_absolute_lifetime_h: number;
  remember_me_enabled: false;
  mfa_mode: string;
  mfa_required_roles: string[];
};

function SecuritySettingsEditor({ resource, data, loading }: { resource: ResourceDefinition; data: unknown; loading: boolean }) {
  const queryClient = useQueryClient();
  const roles = useResourceOptions("/roles", ["name"], ["name"], ["permissions"]);
  const [passwordMinLength, setPasswordMinLength] = useState("12");
  const [loginLockoutThreshold, setLoginLockoutThreshold] = useState("5");
  const [sessionIdleTimeout, setSessionIdleTimeout] = useState("30");
  const [sessionAbsoluteLifetime, setSessionAbsoluteLifetime] = useState("12");
  const [mfaMode, setMFAMode] = useState("disabled");
  const [mfaRequiredRoles, setMFARequiredRoles] = useState<string[]>([]);
  const [message, setMessage] = useState("");
  const save = useMutation<unknown, Error, SecuritySettingsPayload>({
    mutationFn: (payload) => apiPut(resource.path, payload),
    onSuccess: async () => {
      setMessage("セキュリティ設定を保存しました。");
      await queryClient.invalidateQueries({ queryKey: ["resource", resource.path] });
      await queryClient.invalidateQueries({ queryKey: ["auth", "mfa", "status"] });
    },
    onError: (error) => {
      const code = error instanceof APIError ? error.code || "" : "";
      const details: Record<string, string> = {
        invalid_security_settings: "入力値がセキュリティポリシーを満たしていません。",
        production_mfa_required: "本番環境ではMFAを無効化できません。adminまたはsuper_adminをMFA対象にしてください。",
        forbidden: "セキュリティ設定を更新する権限がありません。",
        csrf_failed: "ログイン状態が古くなっています。再読み込みしてから保存してください。",
      };
      setMessage(details[code] || "セキュリティ設定を保存できませんでした。");
    },
  });

  useEffect(() => {
    if (!isRecord(data)) return;
    const handle = window.setTimeout(() => {
      setPasswordMinLength(String(numberSetting(data.password_min_length, 12)));
      setLoginLockoutThreshold(String(numberSetting(data.login_lockout_threshold, 5)));
      setSessionIdleTimeout(String(numberSetting(data.session_idle_timeout_min, 30)));
      setSessionAbsoluteLifetime(String(numberSetting(data.session_absolute_lifetime_h, 12)));
      setMFAMode(stringSetting(data.mfa_mode, "disabled"));
      setMFARequiredRoles(Array.isArray(data.mfa_required_roles) ? data.mfa_required_roles.map(String) : []);
    }, 0);
    return () => window.clearTimeout(handle);
  }, [data]);

  const roleItems = roles.length > 0 ? roles : [{ value: "super_admin", label: "super_admin" }, { value: "admin", label: "admin" }];

  return (
    <div className="rounded-md border bg-muted/20 p-3">
      <div className="mb-3">
        <div className="font-medium">設定を変更</div>
        <p className="text-sm text-muted-foreground">ログイン保護、セッション期限、MFA適用範囲を保存できます。パスワードハッシュはArgon2id固定です。</p>
      </div>
      {loading ? (
        <Skeleton className="h-36 w-full" />
      ) : (
        <form
          className="space-y-3"
          onSubmit={(event) => {
            event.preventDefault();
            save.mutate({
              password_min_length: numberValue(passwordMinLength, 12),
              password_hash: "argon2id",
              login_lockout_threshold: numberValue(loginLockoutThreshold, 5),
              session_idle_timeout_min: numberValue(sessionIdleTimeout, 30),
              session_absolute_lifetime_h: numberValue(sessionAbsoluteLifetime, 12),
              remember_me_enabled: false,
              mfa_mode: mfaMode,
              mfa_required_roles: mfaRequiredRoles,
            });
          }}
        >
          <div className="grid gap-3 md:grid-cols-2">
            <NumberField label="最小パスワード長" value={passwordMinLength} onChange={setPasswordMinLength} min={12} required />
            <NumberField label="ロックまでの失敗回数" value={loginLockoutThreshold} onChange={setLoginLockoutThreshold} min={3} required />
            <NumberField label="アイドルタイムアウト (分)" value={sessionIdleTimeout} onChange={setSessionIdleTimeout} min={5} required />
            <NumberField label="絶対セッション期限 (時間)" value={sessionAbsoluteLifetime} onChange={setSessionAbsoluteLifetime} min={1} required />
            <SelectField
              label="MFAポリシー"
              value={mfaMode}
              onChange={setMFAMode}
              options={[
                { value: "disabled", label: "無効" },
                { value: "totp", label: "TOTPを要求" },
                { value: "passkey", label: "Passkeyを要求" },
              ]}
            />
            <Field label="固定ポリシー">
              <div className="rounded-md border bg-background px-3 py-2 text-sm text-muted-foreground">Argon2id / Remember me無効 / Passkey利用可能</div>
            </Field>
          </div>
          <CheckboxList
            label="MFAを要求するロール"
            values={mfaRequiredRoles}
            onChange={setMFARequiredRoles}
            items={roleItems}
            emptyText="ロールがまだ登録されていません。空のまま保存すると全ユーザーにMFAを要求します。"
          />
          <p className="text-xs text-muted-foreground">MFAポリシー有効時にロールを未選択で保存すると、全ユーザーが対象になります。</p>
          <Button type="submit" disabled={save.isPending}>
            保存
          </Button>
          {message ? <p className="text-sm text-muted-foreground">{message}</p> : null}
        </form>
      )}
    </div>
  );
}

function Field({ label, description, children }: { label: string; description?: string; children: ReactNode }) {
  return (
    <div className="space-y-1.5">
      <div className="text-sm font-medium">{label}</div>
      {children}
      {description ? <p className="text-xs text-muted-foreground">{description}</p> : null}
    </div>
  );
}

function TextField({
  label,
  value,
  onChange,
  description,
  placeholder,
  type = "text",
  required,
}: {
  label: string;
  value: string;
  onChange: (value: string) => void;
  description?: string;
  placeholder?: string;
  type?: string;
  required?: boolean;
}) {
  return (
    <Field label={label} description={description}>
      <Input value={value} onChange={(event) => onChange(event.target.value)} placeholder={placeholder} type={type} required={required} />
    </Field>
  );
}

function NumberField({
  label,
  value,
  onChange,
  min,
  required,
}: {
  label: string;
  value: string;
  onChange: (value: string) => void;
  min?: number;
  required?: boolean;
}) {
  return (
    <Field label={label}>
      <Input value={value} onChange={(event) => onChange(event.target.value)} type="number" min={min} required={required} />
    </Field>
  );
}

function SelectField({ label, value, onChange, options }: { label: string; value: string; onChange: (value: string) => void; options: SelectOption[] }) {
  return (
    <Field label={label}>
      <Select value={value} onValueChange={onChange}>
        <SelectTrigger className="w-full">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {options.map((option) => (
            <SelectItem key={option.value} value={option.value}>
              {option.label}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </Field>
  );
}

function SwitchField({ label, checked, onCheckedChange }: { label: string; checked: boolean; onCheckedChange: (checked: boolean) => void }) {
  return (
    <label className="flex items-center justify-between gap-3 rounded-md border bg-background px-3 py-2 text-sm">
      <span className="font-medium">{label}</span>
      <Switch checked={checked} onCheckedChange={(value) => onCheckedChange(Boolean(value))} />
    </label>
  );
}

function CheckboxList({
  label,
  values,
  onChange,
  items,
  emptyText = "選択肢がありません。",
}: {
  label: string;
  values: string[];
  onChange: (values: string[]) => void;
  items: SelectOption[];
  emptyText?: string;
}) {
  return (
    <Field label={label}>
      {items.length === 0 ? (
        <div className="rounded-md border border-dashed p-3 text-sm text-muted-foreground">{emptyText}</div>
      ) : (
        <div className="grid gap-2 md:grid-cols-2">
          {items.map((item) => (
            <label key={item.value} className="flex items-start gap-2 rounded-md border bg-background p-3 text-sm">
              <Checkbox checked={values.includes(item.value)} onCheckedChange={(checked) => onChange(toggleListValue(values, item.value, Boolean(checked)))} />
              <span>
                <span className="block font-medium">{item.label}</span>
                {item.description ? <span className="block text-xs text-muted-foreground">{item.description}</span> : null}
              </span>
            </label>
          ))}
        </div>
      )}
    </Field>
  );
}

function GroupedCheckboxList(props: { label: string; values: string[]; onChange: (values: string[]) => void; items: SelectOption[]; emptyText?: string }) {
	const groups = useMemo(() => {
		const grouped = new Map<string, SelectOption[]>();
		for (const item of props.items) {
			const group = item.group || permissionGroupLabel(item.value === "*" ? "all" : item.value.split(".")[0] || "other");
			grouped.set(group, [...(grouped.get(group) || []), item]);
		}
		return [...grouped.entries()];
  }, [props.items]);

  return (
    <Field label={props.label}>
      {props.items.length === 0 ? (
        <div className="rounded-md border border-dashed p-3 text-sm text-muted-foreground">{props.emptyText || "選択肢がありません。"}</div>
      ) : (
        <div className="space-y-3">
          {groups.map(([group, items]) => (
            <div key={group} className="space-y-2">
              <div className="text-xs font-medium uppercase text-muted-foreground">{group}</div>
              <div className="grid gap-2 md:grid-cols-2">
                {items.map((item) => (
                  <label key={item.value} className="flex items-start gap-2 rounded-md border bg-background p-3 text-sm">
                    <Checkbox checked={props.values.includes(item.value)} onCheckedChange={(checked) => props.onChange(toggleListValue(props.values, item.value, Boolean(checked)))} />
                    <span>
                      <span className="block font-medium">{item.label}</span>
                      {item.description ? <span className="block text-xs text-muted-foreground">{item.description}</span> : null}
                    </span>
                  </label>
                ))}
              </div>
            </div>
          ))}
        </div>
      )}
    </Field>
  );
}

function FormActions({ disabled, label = "作成" }: { disabled: boolean; label?: string }) {
  return (
    <div className="flex justify-end">
      <Button type="submit" size="sm" disabled={disabled}>
        {label}
      </Button>
    </div>
  );
}

function useResourceRows(path: string) {
  const query = useResourceData<unknown>(path);
  return useMemo(() => normalizeRows(query.data), [query.data]);
}

function useResourceOptions(path: string, valueKeys: string[], labelKeys: string[], detailKeys: string[] = []) {
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

function useRegisteredNodeOptions(serviceType: string) {
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

function permissionOptionFromRow(row: ResourceRow): SelectOption {
	const value = rowString(row, ["id", "name", "value"]);
	return {
		value,
		label: permissionLabel(value),
		description: rowString(row, ["description"]) || permissionDescription(value),
		group: permissionGroupForValue(value),
	};
}

const permissionGroupLabels: Record<string, string> = {
	all: "管理者",
	users: "ユーザー管理",
	roles: "ロール管理",
	streams: "配信運用",
	encoder_profiles: "エンコーダー設定",
	archive_profiles: "録画/アーカイブ設定",
	caption_profiles: "字幕/STT設定",
	overlay_profiles: "ウォーターマーク設定",
	discord_configs: "Discord設定",
	youtube_outputs: "YouTube出力",
	services: "サービス割り当て",
	workers: "Worker管理",
	archives: "録画ファイル",
	logs: "ログ",
	audit_logs: "監査ログ",
	secrets: "シークレット",
	api_tokens: "Node/API token",
	system_settings: "システム設定",
	incidents: "インシデント",
	diagnostics: "診断",
	remediation: "復旧操作",
	notification_channels: "通知設定",
	integrations: "外部連携",
	metrics: "メトリクス",
	service_health: "Nodeヘルス",
	other: "その他",
};

function permissionGroupForValue(value: string) {
	if (value === "*") return permissionGroupLabels.all;
	const group = value.split(".")[0] || "other";
	return permissionGroupLabel(group);
}

function permissionGroupLabel(group: string) {
	return permissionGroupLabels[group] || humanizePermissionText(group);
}

function permissionLabel(value: string) {
	if (value === "*") return "すべての操作を許可";
	const dot = value.lastIndexOf(".");
	if (dot < 0) return humanizePermissionText(value);
	const groupKey = value.slice(0, dot);
	const action = value.slice(dot + 1);
	const subject = permissionGroupLabel(groupKey);
	switch (action) {
		case "read":
			return `${subject}を見る`;
		case "create":
			return `${subject}を作成`;
		case "update":
			return `${subject}を編集`;
		case "delete":
			return `${subject}を削除`;
		case "disable":
			return `${subject}を無効化`;
		case "assign":
			return `${subject}を割り当て`;
		case "unassign":
			return `${subject}の割り当て解除`;
		case "restart":
			return `${subject}を再起動`;
		case "start":
			return `${subject}を開始`;
		case "stop":
			return `${subject}を停止`;
		case "retry_upload":
			return "録画アップロードを再試行";
		case "download":
			return `${subject}をダウンロード`;
		case "export":
			return `${subject}を書き出し`;
		case "revoke":
			return `${subject}を失効`;
		case "read_status":
			return `${subject}の状態を見る`;
		case "reset_password":
			return "ユーザーのパスワードを再設定";
		case "force_password_change":
			return "ユーザーにパスワード変更を要求";
		case "manage_mfa":
			return "ユーザーのMFAを管理";
		case "acknowledge":
			return "インシデントを確認済みにする";
		case "resolve":
			return "インシデントを解決済みにする";
		case "run":
			return "診断を実行";
		case "approve":
			return "復旧操作を承認";
		case "execute":
			return "復旧操作を実行";
		case "test":
			return "通知テストを送信";
		default:
			return `${subject}: ${humanizePermissionText(action)}`;
	}
}

function permissionDescription(value: string) {
	if (value === "*") return "全画面と全操作を許可します。管理者ロールだけに付与します。";
	const group = permissionGroupForValue(value);
	return `${group}に関する操作権限です。`;
}

function humanizePermissionText(value: string) {
	return value
		.split(/[_\-.]+/)
		.filter(Boolean)
		.map((part) => part.charAt(0).toUpperCase() + part.slice(1))
		.join(" ");
}

function toggleListValue(values: string[], value: string, checked: boolean) {
  if (checked) return values.includes(value) ? values : [...values, value];
  return values.filter((item) => item !== value);
}

function numberValue(value: string, fallback: number) {
  const parsed = Number(value);
  return Number.isFinite(parsed) ? parsed : fallback;
}

function numberSetting(value: unknown, fallback: number) {
  return typeof value === "number" && Number.isFinite(value) ? value : fallback;
}

function stringSetting(value: unknown, fallback: string) {
  return typeof value === "string" && value.trim() !== "" ? value : fallback;
}

function splitList(value: string) {
  return value
    .split(/[,\n]/)
    .map((item) => item.trim())
    .filter(Boolean);
}

function compactRecord(record: Record<string, unknown>) {
  return Object.fromEntries(Object.entries(record).filter(([, value]) => value !== "" && value !== undefined));
}

function rowString(row: ResourceRow, keys: string[]) {
  for (const key of keys) {
    const value = nestedRowValue(row, key);
    if (typeof value === "string" && value.trim() !== "") return value;
    if (typeof value === "number") return String(value);
    if (Array.isArray(value) && value.length > 0) return value.map((item) => String(item)).join(", ");
  }
  return "";
}

function firstNonEmpty(...values: string[]) {
  return values.find((value) => value.trim() !== "") || "";
}

function ResourceTable({
  rows,
  columns,
  resource,
  deletePending,
  onDelete,
}: {
  rows: ResourceRow[];
  columns: string[];
  resource: ResourceDefinition;
  deletePending: boolean;
  onDelete: (row: ResourceRow) => void;
}) {
  if (rows.length === 0) {
    return <div className="rounded-md border border-dashed p-6 text-sm text-muted-foreground">データがありません。</div>;
  }
  const showDelete = Boolean(resource.deletable);

  return (
    <div className="overflow-x-auto rounded-md border">
      <Table className="min-w-[720px]">
        <TableHeader>
          <TableRow>
            {columns.map((column) => (
              <TableHead key={column}>{columnLabel(column)}</TableHead>
            ))}
            {showDelete ? <TableHead className="w-28 min-w-28 text-right">操作</TableHead> : null}
          </TableRow>
        </TableHeader>
        <TableBody>
          {rows.map((row, index) => (
            <TableRow key={String(row.id || row.name || index)}>
              {columns.map((column) => (
                <TableCell key={column} className="max-w-[240px] overflow-hidden text-ellipsis">
                  {formatCell(row[column], column)}
                </TableCell>
              ))}
              {showDelete ? (
                <TableCell className="w-28 min-w-28 text-right">
                  <DeleteResourceButton row={row} disabled={deletePending || !resourceRowID(row)} onDelete={onDelete} />
                </TableCell>
              ) : null}
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  );
}

function DeleteResourceButton({ row, disabled, onDelete }: { row: ResourceRow; disabled: boolean; onDelete: (row: ResourceRow) => void }) {
  const label = resourceRowLabel(row);
  return (
    <DangerConfirm title={`${label} を削除しますか`} description="削除後は元に戻せません。参照中の設定は削除できない場合があります。" actionLabel="削除" onConfirm={() => onDelete(row)}>
      <Button variant="destructive" size="icon-sm" disabled={disabled} aria-label={`${label} を削除`}>
        <Trash2 className="size-4" />
      </Button>
    </DangerConfirm>
  );
}

function resourceDeleteErrorMessage(error: Error) {
  if (error instanceof APIError) {
    const messages: Record<string, string> = {
      profile_in_use: "削除できません。この設定を参照している配信枠があります。先にStreamsで別の設定へ変更してください。",
      drive_destination_in_use: "削除できません。この保存先を参照している配信枠またはArchive profileがあります。先にStreamsやArchive Settingsで別の保存先へ変更してください。",
      oauth_account_in_use: "削除できません。このOAuth accountを参照している保存先、YouTube Output、または配信枠があります。先に参照を外してください。",
      oauth_provider_in_use: "削除できません。このOAuth providerに接続済みaccountやログイン連携があります。先に関連する連携を削除してください。",
      cannot_delete_self: "ログイン中の自分自身は削除できません。別の管理者アカウントで操作してください。",
      cannot_delete_super_admin: "super_adminユーザーはsuper_adminだけが削除できます。",
      last_super_admin: "最後の有効なsuper_adminは削除できません。先に別のsuper_adminを有効化してください。",
      permission_escalation: "自分より強い権限を持つユーザーは削除できません。",
      not_found: "削除対象が見つかりませんでした。画面を更新してください。",
      csrf_failed: "ログイン状態またはCSRF tokenが古くなっています。ページを再読み込みしてから再実行してください。",
      forbidden: "削除権限がありません。",
    };
    return messages[error.code || ""] || `削除に失敗しました。参照中の設定や権限を確認してください。${error.code ? ` (${error.code})` : ""}`;
  }
  return `削除に失敗しました。参照中の設定や権限を確認してください。${error.message ? ` (${error.message})` : ""}`;
}

function normalizeRows(data: unknown): Record<string, unknown>[] {
  if (!data) return [];
  if (Array.isArray(data)) return data.map((item) => normalizeRow(item));
  if (isRecord(data)) {
    for (const key of ["items", "data", "results"]) {
      const value = data[key];
      if (Array.isArray(value)) return value.map((item) => normalizeRow(item));
    }
    return Object.entries(data).map(([key, value]) => ({ name: key, value }));
  }
  return [{ value: data }];
}

function normalizeRow(item: unknown): Record<string, unknown> {
  if (isRecord(item)) {
    const row: Record<string, unknown> = {};
    for (const [key, value] of Object.entries(item)) row[key] = value;
    return row;
  }
  return { value: item };
}

function enrichResourceRow(resource: ResourceDefinition, row: ResourceRow): ResourceRow {
  if (resource.path.startsWith("/profiles/")) {
    return { ...row, profile_summary: profileSummary(resource.path, row) };
  }
  if (resource.path === "/discord/configs") {
    return { ...row, bot_summary: compactList([enabledLabel("音声転送", rowValue(row, ["audio_forward_enabled", "config.audio_forward_enabled"])), enabledLabel("自動再接続", rowValue(row, ["reconnect_enabled", "config.reconnect_enabled"])), rowString(row, ["config.reconnect_max_attempts"]) ? `再接続 ${rowString(row, ["config.reconnect_max_attempts"])}回` : ""]) };
  }
  if (resource.path === "/youtube/outputs") {
    return { ...row, output_summary: compactList([labelValue("方式", rowString(row, ["mode", "config.mode"])), labelValue("公開", rowString(row, ["privacy_status", "config.privacy_status"])), enabledLabel("自動開始", rowValue(row, ["enable_auto_start", "config.enable_auto_start"]))]) };
  }
  if (resource.path === "/archive/destinations") {
    return { ...row, destination_summary: compactList([rowValue(row, ["shared_drive"]) === true ? "共有ドライブ" : "マイドライブ", rowValue(row, ["folder_id_configured"]) === true ? "Folder設定済み" : "Folder未設定"]) };
  }
  return row;
}

function profileSummary(path: string, row: ResourceRow) {
  if (path === "/profiles/encoder") {
    const width = rowString(row, ["width", "config.width"]);
    const height = rowString(row, ["height", "config.height"]);
    return compactList([width && height ? `${width}x${height}` : "", rowString(row, ["fps", "config.fps"]) ? `${rowString(row, ["fps", "config.fps"])}fps` : "", rowString(row, ["video_bitrate_kbps", "bitrate_kbps", "config.video_bitrate_kbps"]) ? `${rowString(row, ["video_bitrate_kbps", "bitrate_kbps", "config.video_bitrate_kbps"])}kbps` : "", rowString(row, ["audio_bitrate_kbps", "config.audio_bitrate_kbps"]) ? `音声 ${rowString(row, ["audio_bitrate_kbps", "config.audio_bitrate_kbps"])}kbps` : ""]);
  }
  if (path === "/profiles/caption") {
    return compactList([labelValue("言語", rowString(row, ["language", "config.language"])), labelValue("方式", rowString(row, ["provider", "config.provider"])), rowString(row, ["delay_ms", "config.delay_ms"]) ? `遅延 ${rowString(row, ["delay_ms", "config.delay_ms"])}ms` : ""]);
  }
  if (path === "/profiles/overlay") {
    return compactList([
      enabledLabel("ウォーターマーク", rowValue(row, ["watermark_enabled", "config.watermark_enabled"])),
      "1920x1080",
      "自動フィット",
      rowString(row, ["watermark_image_name", "config.watermark_image_name", "watermark_image_url", "config.watermark_image_url"]) ? "画像あり" : "",
    ]);
  }
  if (path === "/profiles/archive") {
    return compactList([labelValue("形式", rowString(row, ["format", "config.format"])), rowString(row, ["retention_days", "config.retention_days"]) ? `${rowString(row, ["retention_days", "config.retention_days"])}日保持` : "", enabledLabel("Upload", rowValue(row, ["upload_enabled", "config.upload_enabled"])), rowString(row, ["drive_destination_id", "config.drive_destination_id"]) ? "Drive保存先あり" : ""]);
  }
  return [];
}

function visibleColumns(rows: Record<string, unknown>[], resource: ResourceDefinition) {
  const resourcePreferred = resourcePreferredColumns(resource);
  if (resourcePreferred.length > 0) {
    return resourcePreferred.filter((column) => rows.some((row) => row[column] !== undefined));
  }
  const preferred = ["id", "name", "username", "service_id", "service_name", "service_type", "type", "status", "health_status", "title", "action", "target", "updated_at", "created_at"];
  const seen = new Set<string>();
  for (const key of preferred) {
    if (rows.some((row) => row[key] !== undefined)) seen.add(key);
  }
  for (const row of rows) {
    for (const key of Object.keys(row)) {
      if (seen.size >= 8) break;
      seen.add(key);
    }
    if (seen.size >= 8) break;
  }
  return [...seen];
}

function resourcePreferredColumns(resource: ResourceDefinition) {
  if (resource.path.startsWith("/profiles/")) return ["id", "name", "profile_summary", "updated_at", "created_at"];
  if (resource.path === "/discord/configs") return ["id", "name", "service_id", "bot_summary", "updated_at"];
  if (resource.path === "/integrations/oauth-providers") return ["id", "name", "provider_type", "enabled", "client_secret_configured", "updated_at"];
  if (resource.path === "/youtube/outputs") return ["id", "name", "output_summary", "updated_at"];
  if (resource.path === "/archive/destinations") return ["id", "name", "destination_summary", "updated_at"];
  if (resource.path === "/users") return ["id", "username", "email", "status", "roles", "last_login_at"];
  return [];
}

function formatCell(value: unknown, key = ""): ReactNode {
  if (value === null || value === undefined || value === "") return "-";
  if (typeof value === "boolean") return <Badge variant={value ? "default" : "secondary"}>{value ? "有効" : "無効"}</Badge>;
  if (typeof value === "string" || typeof value === "number") return formatScalarValue(key, value);
  if (Array.isArray(value)) {
    if (value.length === 0) return "-";
    return (
      <div className="flex flex-wrap gap-1">
        {value.slice(0, 6).map((item, index) => (
          <Badge key={index} variant="secondary" className="max-w-full text-xs">
            {formatNestedValue(key, item)}
          </Badge>
        ))}
        {value.length > 6 ? <Badge variant="outline">+{value.length - 6}</Badge> : null}
      </div>
    );
  }
  if (isRecord(value)) {
    const entries = Object.entries(value).filter(([, entryValue]) => entryValue !== "" && entryValue !== undefined && entryValue !== null);
    if (entries.length === 0) return "-";
    return (
      <div className="space-y-1 text-xs">
        {entries.slice(0, 5).map(([key, entryValue]) => (
          <div key={key} className="grid grid-cols-[minmax(72px,0.45fr)_minmax(0,1fr)] gap-2">
            <span className="text-muted-foreground">{columnLabel(key)}</span>
            <span className="min-w-0 truncate">{formatNestedValue(key, entryValue)}</span>
          </div>
        ))}
        {entries.length > 5 ? <div className="text-muted-foreground">ほか {entries.length - 5} 件</div> : null}
      </div>
    );
  }
  return String(value);
}

function formatNestedValue(key: string, value: unknown): string {
  if (isSensitiveKey(key)) return value ? "設定済み" : "-";
  if (value === null || value === undefined || value === "") return "-";
  if (typeof value === "boolean") return value ? "有効" : "無効";
  if (typeof value === "string" || typeof value === "number") return formatScalarValue(key, value);
  if (Array.isArray(value)) return value.length === 0 ? "-" : value.map((item) => formatNestedValue("", item)).join(", ");
  if (isRecord(value)) return "設定あり";
  return String(value);
}

function formatScalarValue(key: string, value: string | number) {
  const raw = String(value);
  if ((key === "id" || key === "name") && columnLabels[raw]) return columnLabels[raw];
  const status = valueLabels[raw] || valueLabels[raw.toLowerCase()];
  if (status) return status;
  if (key.endsWith("_at") && !Number.isNaN(Date.parse(raw))) return raw.replace("T", " ").replace(/Z$/, " UTC");
  return raw;
}

function rowValue(row: ResourceRow, keys: string[]) {
  for (const key of keys) {
    const value = nestedRowValue(row, key);
    if (value !== undefined && value !== null && value !== "") return value;
  }
  return undefined;
}

function nestedRowValue(row: ResourceRow, key: string): unknown {
  const parts = key.split(".");
  let current: unknown = row;
  for (const part of parts) {
    if (!isRecord(current)) return undefined;
    current = current[part];
  }
  return current;
}

function labelValue(label: string, value: string) {
  return value ? `${label}: ${value}` : "";
}

function enabledLabel(label: string, value: unknown) {
  if (value === undefined || value === null || value === "") return "";
  return `${label}: ${value === true ? "有効" : value === false ? "無効" : String(value)}`;
}

function compactList(values: string[]) {
  return values.map((value) => value.trim()).filter(Boolean);
}

function isSensitiveKey(key: string) {
  return /(secret|token|password|credential|private|key)/i.test(key);
}

function humanizeKey(key: string) {
  const known = columnLabels[key];
  if (known) return known;
  return key
    .replace(/_/g, " ")
    .replace(/\b\w/g, (letter) => letter.toUpperCase());
}

const valueLabels: Record<string, string> = {
  discord_bot: "Discord Bot",
  encoder_recorder: "Encoder/Recorder",
  observability: "Observability",
  worker: "Worker",
  online: "オンライン",
  offline: "オフライン",
  healthy: "正常",
  degraded: "注意",
  unhealthy: "異常",
  created: "作成済み",
  scheduled: "予約",
  ready: "待機中",
  draft: "下書き",
  starting: "開始中",
  live: "配信中",
  stopping: "停止中",
  stopped: "停止",
  completed: "完了",
  failed: "失敗",
  error: "エラー",
  public: "公開",
  unlisted: "限定公開",
  private: "非公開",
  stream_key: "既存ストリームキー",
  live_api: "YouTube Live API本番",
  live_api_dry_run: "YouTube Live API検証",
  top_left: "左上",
  top_right: "右上",
  bottom_left: "左下",
  bottom_right: "右下",
  disabled: "無効",
  totp: "TOTP",
  passkey: "Passkey",
  "Password minimum length": "最小パスワード長",
  "MFA mode": "MFAポリシー",
  "Session idle timeout": "アイドルタイムアウト",
  "Session absolute lifetime": "絶対セッション期限",
  "Login lockout threshold": "ロックまでの失敗回数",
  "Remember me enabled": "Remember me",
  "Passkey status": "Passkey状態",
};

const columnLabels: Record<string, string> = {
  id: "ID",
  name: "名前",
  username: "ユーザー名",
  email: "メール",
  service_id: "Node ID",
  service_name: "Node名",
  service_type: "種別",
  provider_type: "プロバイダ",
  type: "種別",
  status: "状態",
  health_status: "ヘルス",
  title: "タイトル",
  action: "操作",
  target: "対象",
  updated_at: "更新日時",
  created_at: "作成日時",
  last_login_at: "最終ログイン",
  profile_summary: "設定内容",
  bot_summary: "BOT設定",
  output_summary: "出力設定",
  destination_summary: "保存先",
  enabled: "有効",
  configured: "設定済み",
  client_secret_configured: "Client Secret",
  password_min_length: "最小パスワード長",
  password_hash: "パスワードハッシュ",
  login_lockout_threshold: "ロックまでの失敗回数",
  session_idle_timeout_min: "アイドル期限(分)",
  session_absolute_lifetime_h: "絶対期限(時間)",
  remember_me_enabled: "Remember me",
  mfa_mode: "MFAポリシー",
  mfa_required_roles: "MFA対象ロール",
  mfa_supported_methods: "対応MFA",
  passkey_status: "Passkey状態",
  fingerprint: "指紋",
  value: "値",
};

function columnLabel(column: string) {
  return columnLabels[column] || humanizeKey(column);
}

function resourceRowID(row: ResourceRow) {
  return rowString(row, ["id"]);
}

function resourceRowLabel(row: ResourceRow) {
  return firstNonEmpty(rowString(row, ["name", "service_name", "username", "account_label", "provider_type", "id"]), "この項目");
}

function deletePathForResource(resource: ResourceDefinition, row: ResourceRow) {
  const id = resourceRowID(row);
  if (!id) throw new Error("delete id is missing");
  return `${resource.path}/${encodeURIComponent(id)}`;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
