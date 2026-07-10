"use client";

import type { ComponentType } from "react";
import { useMemo } from "react";
import Link from "next/link";
import {
  AlertTriangle,
  Archive,
  ArrowRight,
  CalendarDays,
  CheckCircle2,
  ClipboardList,
  Clock3,
  Plus,
  RadioTower,
  RefreshCcw,
  ServerCog,
  ShieldCheck,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { StatusBadge, statusDescriptor } from "@/components/admin/status-badge";
import { useAppSettings, useCurrentUser, useServiceHealth, useStreams } from "@/features/queries";
import { hasPermission } from "@/lib/auth/permissions";
import { recordingDescriptor, safeDisplayURL } from "@/lib/stream-presentation";
import { formatDateTimeInTimeZone, normalizeTimeZone } from "@/lib/timezone";
import { cn } from "@/lib/utils";
import type { CurrentUser, Stream, WorkerNode } from "@/types/domain";

export function DashboardView() {
  const currentUser = useCurrentUser();
  const superAdmin = isSuperAdmin(currentUser.data);
  const canReadStreams = superAdmin || hasPermission(currentUser.data, "streams.read");
  const canCreateStreams = superAdmin || hasPermission(currentUser.data, "streams.create");
  const canReadServices = superAdmin || hasPermission(currentUser.data, "service_health.read");
  const streams = useStreams(canReadStreams);
  const services = useServiceHealth(canReadServices);
  const appSettings = useAppSettings();
  const timezone = normalizeTimeZone(appSettings.data?.timezone);

  const streamRows = useMemo(() => [...(streams.data || [])].sort(compareStreams), [streams.data]);
  const serviceRows = useMemo(() => services.data || [], [services.data]);
  const statusCounts = useMemo(() => countStreamStatus(streamRows), [streamRows]);
  const serviceNameByID = useMemo(
    () => new Map(serviceRows.flatMap((row) => compactValues([row.id, row.service_id]).map((id) => [id, row.service_name || id] as const))),
    [serviceRows],
  );
  const servicesByStream = useMemo(() => {
    const grouped = new Map<string, string[]>();
    for (const service of serviceRows) {
      const streamID = service.current_stream_id?.trim();
      if (!streamID) continue;
      grouped.set(streamID, [...(grouped.get(streamID) || []), service.service_name || service.service_id || service.id]);
    }
    return grouped;
  }, [serviceRows]);
  const streamNameByID = useMemo(() => new Map(streamRows.map((stream) => [stream.id, stream.name])), [streamRows]);
  const scheduledToday = useMemo(() => streamRows.filter((stream) => isSameDay(stream.scheduled_start_at, new Date(), timezone) && !isFinished(stream.status)), [streamRows, timezone]);
  const scheduleRows = (scheduledToday.length > 0 ? scheduledToday : streamRows.filter((stream) => !isFinished(stream.status))).slice(0, 6);
  const scheduleTitle = scheduledToday.length > 0 ? "本日の配信予定" : "直近の配信予定";
  const availableServices = serviceRows.filter(isAvailableService).length;
  const streamIssues = streamRows.filter((stream) => ["failed", "error"].includes(String(stream.status).toLowerCase()));
  const serviceIssues = serviceRows.filter((service) => !isAvailableService(service));
  const refreshing = streams.isFetching || services.isFetching;

  if (currentUser.isLoading || ((streams.isLoading || services.isLoading) && streamRows.length === 0 && serviceRows.length === 0)) {
    return <DashboardSkeleton />;
  }

  return (
    <div className="space-y-5">
      <section className="flex flex-col gap-3 border-b pb-5 sm:flex-row sm:items-end sm:justify-between">
        <div>
          <div className="flex items-center gap-2 text-sm font-medium text-primary">
            <CalendarDays className="size-4" />
            {formatToday(timezone)}
          </div>
          <h1 className="mt-1 text-xl font-semibold">本日の配信オペレーション</h1>
          <p className="mt-1 text-sm text-muted-foreground">予定、録画、要対応、配信基盤を一画面で確認できます。</p>
        </div>
        <div className="flex flex-wrap gap-2">
          <Button
            variant="outline"
            size="sm"
            disabled={refreshing}
            onClick={() => {
              if (canReadStreams) void streams.refetch();
              if (canReadServices) void services.refetch();
            }}
          >
            <RefreshCcw className={cn("size-4", refreshing && "animate-spin")} />
            最新状態に更新
          </Button>
          {canCreateStreams ? (
            <Button asChild size="sm">
              <Link href="/admin/streams/#create-stream">
                <Plus className="size-4" />
                配信枠を作成
              </Link>
            </Button>
          ) : null}
        </div>
      </section>

      {streams.isError || services.isError ? (
        <QueryWarning
          streamsFailed={canReadStreams && streams.isError}
          servicesFailed={canReadServices && services.isError}
          retry={() => {
            if (canReadStreams) void streams.refetch();
            if (canReadServices) void services.refetch();
          }}
        />
      ) : null}

      <section className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4" aria-label="運用サマリー">
        <OperationMetric icon={RadioTower} label="配信中" value={canReadStreams ? statusCounts.live : "-"} detail={canReadStreams ? "映像と録画を監視中" : "配信の参照権限がありません"} tone={statusCounts.live > 0 ? "ok" : "default"} />
        <OperationMetric icon={Clock3} label="開始待ち" value={canReadStreams ? statusCounts.waiting : "-"} detail={canReadStreams ? "予約・待機中の配信枠" : "管理者へ権限を確認してください"} />
        <OperationMetric icon={AlertTriangle} label="要対応" value={canReadStreams || canReadServices ? statusCounts.attention + serviceIssues.length : "-"} detail={canReadStreams || canReadServices ? "配信と基盤の確認項目" : "確認できる対象がありません"} tone={statusCounts.attention + serviceIssues.length > 0 ? "danger" : "ok"} />
        <OperationMetric
          icon={ServerCog}
          label="サービス稼働"
          value={canReadServices ? `${availableServices}/${serviceRows.length}` : "-"}
          detail={canReadServices ? (serviceRows.length > 0 && availableServices === serviceRows.length ? "すべて正常" : "未接続・警告を確認") : "サービス状態の参照権限がありません"}
          tone={canReadServices && serviceRows.length > 0 && availableServices === serviceRows.length ? "ok" : canReadServices ? "warning" : "default"}
        />
      </section>

      <section className="grid items-start gap-4 xl:grid-cols-[minmax(0,1fr)_22rem]">
        {canReadStreams ? (
          <Card className="min-w-0">
            <CardHeader className="border-b">
              <div className="flex flex-wrap items-start justify-between gap-3">
                <div>
                  <CardTitle>{scheduleTitle}</CardTitle>
                  <CardDescription>{scheduledToday.length > 0 ? `${scheduledToday.length}件の配信枠があります。` : "今日の予定がないため、次に確認する配信枠を表示しています。"}</CardDescription>
                </div>
                <Button asChild variant="ghost" size="sm">
                  <Link href="/admin/streams/">
                    すべての配信枠
                    <ArrowRight className="size-4" />
                  </Link>
                </Button>
              </div>
            </CardHeader>
            <CardContent className="p-0">
              {scheduleRows.length > 0 ? (
                <div className="divide-y">
                  <div className="hidden grid-cols-[6rem_7.25rem_minmax(0,1fr)_8rem_11rem_2rem] gap-3 bg-muted/45 px-4 py-2 text-xs font-medium text-muted-foreground lg:grid">
                    <span>予定</span>
                    <span>状態</span>
                    <span>配信枠</span>
                    <span>録画</span>
                    <span>担当Node</span>
                    <span className="sr-only">詳細</span>
                  </div>
                  {scheduleRows.map((stream) => (
                    <div key={stream.id} className="grid gap-3 px-4 py-3 transition-colors hover:bg-muted/25 lg:grid-cols-[6rem_7.25rem_minmax(0,1fr)_8rem_11rem_2rem] lg:items-center">
                      <div>
                        <div className="text-sm font-semibold tabular-nums">{formatTime(stream.scheduled_start_at, timezone)}</div>
                        <div className="mt-0.5 text-xs text-muted-foreground">{formatDay(stream.scheduled_start_at, timezone)}</div>
                      </div>
                      <StatusBadge status={stream.status} />
                      <div className="min-w-0">
                        <div className="truncate text-sm font-medium">{stream.name}</div>
                        <div className="mt-1 flex min-w-0 flex-wrap gap-x-3 gap-y-1 text-xs text-muted-foreground">
                          <span className="truncate">入力: {safeDisplayURL(stream.encoder_input_url || stream.input_source) || "未設定"}</span>
                          <span className="truncate">出力: {stream.output_target || (stream.youtube_output_id ? "YouTube" : "未設定")}</span>
                        </div>
                      </div>
                      <RecordingBadge stream={stream} />
                      <div className="text-xs leading-5 text-muted-foreground">
                        {assignedNodeLabels(stream, serviceNameByID, servicesByStream).map((label) => (
                          <div key={label} className="truncate">{label}</div>
                        ))}
                      </div>
                      <Button asChild variant="ghost" size="icon-sm">
                        <Link href="/admin/streams/" aria-label={`${stream.name}を配信一覧で確認`}>
                          <ArrowRight className="size-4" />
                        </Link>
                      </Button>
                    </div>
                  ))}
                </div>
              ) : (
                <EmptyState icon={CalendarDays} title="配信枠はまだありません" description="配信枠を作成すると、開始時刻・録画・状態がここに並びます。" href="/admin/streams/#create-stream" action="最初の配信枠を作成" />
              )}
            </CardContent>
          </Card>
        ) : (
          <PermissionPanel title="配信予定を表示できません" description="このアカウントには配信枠を参照する権限がありません。管理者に「配信の閲覧」権限を依頼してください。" />
        )}

        <Card>
          <CardHeader className="border-b">
            <div className="flex items-center justify-between gap-3">
              <div>
                <CardTitle>要対応</CardTitle>
                <CardDescription>優先して確認する項目</CardDescription>
              </div>
              <span className={cn("rounded-md border px-2 py-1 text-xs font-semibold", streamIssues.length + serviceIssues.length > 0 ? "border-red-200 bg-red-50 text-red-700 dark:border-red-900 dark:bg-red-950/35 dark:text-red-200" : "border-emerald-200 bg-emerald-50 text-emerald-700 dark:border-emerald-900 dark:bg-emerald-950/35 dark:text-emerald-200")}>{streamIssues.length + serviceIssues.length}件</span>
            </div>
          </CardHeader>
          <CardContent className="p-0">
            {streamIssues.length + serviceIssues.length > 0 ? (
              <div className="divide-y">
                {streamIssues.slice(0, 3).map((stream) => (
                  <IssueRow key={stream.id} href="/admin/streams/" title={stream.name} detail={statusDescriptor(stream.status).detail} tone="danger" />
                ))}
                {serviceIssues.slice(0, 4).map((service) => (
                  <IssueRow key={service.id || service.service_id} href="/admin/service-health/" title={service.service_name || service.service_id || service.id} detail={`${serviceTypeLabel(service.service_type)} / ${statusDescriptor(service.health_status || service.status).label}`} tone={String(service.status).toLowerCase() === "online" ? "warning" : "danger"} />
                ))}
              </div>
            ) : (
              <div className="flex min-h-48 flex-col items-center justify-center px-6 py-8 text-center">
                <span className="flex size-10 items-center justify-center rounded-full bg-emerald-100 text-emerald-700 dark:bg-emerald-950/50 dark:text-emerald-300">
                  <CheckCircle2 className="size-5" />
                </span>
                <div className="mt-3 text-sm font-semibold">対応待ちはありません</div>
                <div className="mt-1 text-xs text-muted-foreground">確認できる配信とサービスは正常です。</div>
              </div>
            )}
          </CardContent>
        </Card>
      </section>

      <section className="grid items-start gap-4 xl:grid-cols-[minmax(0,1.35fr)_minmax(18rem,0.65fr)]">
        {canReadServices ? (
          <Card>
            <CardHeader className="border-b">
              <div className="flex flex-wrap items-start justify-between gap-3">
                <div>
                  <CardTitle>配信基盤</CardTitle>
                  <CardDescription>Nodeの接続状態と現在の担当配信</CardDescription>
                </div>
                <Button asChild variant="ghost" size="sm">
                  <Link href="/admin/service-health/">
                    稼働状況を開く
                    <ArrowRight className="size-4" />
                  </Link>
                </Button>
              </div>
            </CardHeader>
            <CardContent className="p-0">
              {serviceRows.length > 0 ? (
                <div className="divide-y">
                  {serviceRows.slice(0, 6).map((service) => (
                    <div key={service.id || service.service_id} className="grid gap-2 px-4 py-3 sm:grid-cols-[minmax(0,1fr)_9rem_11rem] sm:items-center">
                      <div className="min-w-0">
                        <div className="truncate text-sm font-medium">{service.service_name || service.service_id || service.id}</div>
                        <div className="mt-0.5 text-xs text-muted-foreground">{serviceTypeLabel(service.service_type)}</div>
                      </div>
                      <StatusBadge status={service.health_status || service.status} />
                      <div className="truncate text-xs text-muted-foreground">{service.current_stream_id ? streamNameByID.get(service.current_stream_id) || service.current_stream_id : "待機中"}</div>
                    </div>
                  ))}
                </div>
              ) : (
                <EmptyState icon={ServerCog} title="登録済みNodeがありません" description="Nodeを登録すると稼働状況を確認できます。" href="/admin/nodes/" action="Node登録を開く" />
              )}
            </CardContent>
          </Card>
        ) : (
          <PermissionPanel title="配信基盤を表示できません" description="Nodeとサービス状態を参照する権限がありません。" />
        )}

        <Card>
          <CardHeader className="border-b">
            <CardTitle>運用証跡</CardTitle>
            <CardDescription>確認・報告に使う管理情報</CardDescription>
          </CardHeader>
          <CardContent className="p-0">
            {superAdmin || hasPermission(currentUser.data, "audit_logs.read") ? <QuickLink href="/admin/audit-logs/" icon={ClipboardList} title="操作監査" detail="担当者の操作履歴" /> : null}
            {superAdmin || hasPermission(currentUser.data, "archives.read") ? <QuickLink href="/admin/archive/" icon={Archive} title="録画・アーカイブ" detail="成果物と保存状態" /> : null}
            {superAdmin || hasPermission(currentUser.data, "system_settings.read") ? <QuickLink href="/admin/security/" icon={ShieldCheck} title="セキュリティ" detail="MFAと運用ポリシー" /> : null}
          </CardContent>
        </Card>
      </section>
    </div>
  );
}

function DashboardSkeleton() {
  return (
    <div className="space-y-4">
      <Skeleton className="h-14 w-full" />
      <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        {Array.from({ length: 4 }).map((_, index) => <Skeleton key={index} className="h-28 w-full" />)}
      </div>
      <Skeleton className="h-[420px] w-full" />
    </div>
  );
}

function OperationMetric({ icon: Icon, label, value, detail, tone = "default" }: { icon: ComponentType<{ className?: string }>; label: string; value: string | number; detail: string; tone?: "default" | "ok" | "warning" | "danger" }) {
  return (
    <Card className="gap-0 py-0">
      <CardContent className="flex items-start gap-3 p-4">
        <span className={cn("flex size-9 shrink-0 items-center justify-center rounded-lg bg-muted text-muted-foreground", tone === "ok" && "bg-emerald-100 text-emerald-700 dark:bg-emerald-950/45 dark:text-emerald-300", tone === "warning" && "bg-amber-100 text-amber-700 dark:bg-amber-950/45 dark:text-amber-300", tone === "danger" && "bg-red-100 text-red-700 dark:bg-red-950/45 dark:text-red-300")}>
          <Icon className="size-4" />
        </span>
        <div className="min-w-0">
          <div className="text-xs font-medium text-muted-foreground">{label}</div>
          <div className={cn("mt-0.5 text-2xl font-semibold tabular-nums", tone === "ok" && "text-emerald-700 dark:text-emerald-300", tone === "warning" && "text-amber-700 dark:text-amber-300", tone === "danger" && "text-red-700 dark:text-red-300")}>{value}</div>
          <div className="mt-0.5 text-xs text-muted-foreground">{detail}</div>
        </div>
      </CardContent>
    </Card>
  );
}

function RecordingBadge({ stream }: { stream: Stream }) {
  const recording = recordingDescriptor(stream);
  return <span className={cn("inline-flex w-fit items-center rounded-md border px-2 py-1 text-xs font-medium", recording.className)}>{recording.label}</span>;
}

function IssueRow({ href, title, detail, tone }: { href: string; title: string; detail: string; tone: "warning" | "danger" }) {
  return (
    <Link href={href} className="flex items-start gap-3 px-4 py-3 transition-colors hover:bg-muted/35">
      <span className={cn("mt-0.5 flex size-7 shrink-0 items-center justify-center rounded-md", tone === "danger" ? "bg-red-100 text-red-700 dark:bg-red-950/45 dark:text-red-300" : "bg-amber-100 text-amber-700 dark:bg-amber-950/45 dark:text-amber-300")}><AlertTriangle className="size-3.5" /></span>
      <span className="min-w-0 flex-1"><span className="block truncate text-sm font-medium">{title}</span><span className="mt-0.5 block text-xs text-muted-foreground">{detail}</span></span>
      <ArrowRight className="mt-1 size-4 shrink-0 text-muted-foreground" />
    </Link>
  );
}

function QuickLink({ href, icon: Icon, title, detail }: { href: string; icon: ComponentType<{ className?: string }>; title: string; detail: string }) {
  return (
    <Link href={href} className="flex items-center gap-3 border-b px-4 py-3 transition-colors last:border-b-0 hover:bg-muted/35">
      <span className="flex size-8 shrink-0 items-center justify-center rounded-lg bg-muted text-muted-foreground"><Icon className="size-4" /></span>
      <span className="min-w-0 flex-1"><span className="block text-sm font-medium">{title}</span><span className="block truncate text-xs text-muted-foreground">{detail}</span></span>
      <ArrowRight className="size-4 text-muted-foreground" />
    </Link>
  );
}

function EmptyState({ icon: Icon, title, description, href, action }: { icon: ComponentType<{ className?: string }>; title: string; description: string; href: string; action: string }) {
  return (
    <div className="flex min-h-56 flex-col items-center justify-center px-6 py-10 text-center">
      <span className="flex size-10 items-center justify-center rounded-lg bg-muted text-muted-foreground"><Icon className="size-5" /></span>
      <div className="mt-3 text-sm font-semibold">{title}</div><p className="mt-1 max-w-sm text-xs text-muted-foreground">{description}</p>
      <Button asChild variant="outline" size="sm" className="mt-4"><Link href={href}>{action}</Link></Button>
    </div>
  );
}

function PermissionPanel({ title, description }: { title: string; description: string }) {
  return <Card><CardContent className="flex min-h-48 flex-col justify-center p-6"><ShieldCheck className="size-5 text-muted-foreground" /><div className="mt-3 font-semibold">{title}</div><p className="mt-1 max-w-xl text-sm text-muted-foreground">{description}</p></CardContent></Card>;
}

function QueryWarning({ streamsFailed, servicesFailed, retry }: { streamsFailed: boolean; servicesFailed: boolean; retry: () => void }) {
  const targets = [streamsFailed ? "配信予定" : "", servicesFailed ? "サービス状態" : ""].filter(Boolean).join("と");
  return (
    <div className="flex flex-col gap-3 rounded-lg border border-amber-300 bg-amber-50 p-4 text-amber-900 dark:border-amber-900 dark:bg-amber-950/35 dark:text-amber-100 sm:flex-row sm:items-center sm:justify-between">
      <div className="flex gap-3"><AlertTriangle className="mt-0.5 size-5 shrink-0" /><div><div className="text-sm font-semibold">{targets}を取得できませんでした</div><p className="mt-0.5 text-xs opacity-85">通信状態を確認して再試行してください。直前の表示がある場合は更新前の情報です。</p></div></div>
      <Button type="button" variant="outline" size="sm" onClick={retry}><RefreshCcw className="size-4" />再試行</Button>
    </div>
  );
}

function countStreamStatus(streams: Stream[]) {
  return streams.reduce((counts, stream) => {
    const status = String(stream.status).toLowerCase();
    if (["live", "starting"].includes(status)) counts.live += 1;
    else if (["created", "scheduled", "ready", "draft"].includes(status)) counts.waiting += 1;
    else if (["failed", "error"].includes(status)) counts.attention += 1;
    else counts.done += 1;
    return counts;
  }, { live: 0, waiting: 0, attention: 0, done: 0 });
}

function compareStreams(left: Stream, right: Stream) {
  const priority = (stream: Stream) => {
    const status = String(stream.status).toLowerCase();
    if (["failed", "error"].includes(status)) return 0;
    if (["live", "starting"].includes(status)) return 1;
    if (["ready", "scheduled", "created", "draft"].includes(status)) return 2;
    return 3;
  };
  return priority(left) - priority(right) || dateValue(left.scheduled_start_at) - dateValue(right.scheduled_start_at);
}

function isFinished(status?: string) { return ["completed", "stopped"].includes(String(status || "").toLowerCase()); }
function isAvailableService(service: WorkerNode) { const status = String(service.status || "").toLowerCase(); const health = String(service.health_status || "").toLowerCase(); return status === "online" && (!health || ["healthy", "ok", "online"].includes(health)); }
function isSuperAdmin(currentUser?: CurrentUser) { return currentUser?.user.roles?.includes("super_admin") === true; }

function assignedNodeLabels(stream: Stream, labels: Map<string, string>, servicesByStream: Map<string, string[]>) {
  const ids = compactValues([stream.assigned_worker_id, stream.assigned_encoder_id]);
  const resolved = [...ids.map((id) => labels.get(id) || id), ...(servicesByStream.get(stream.id) || [])];
  const unique = Array.from(new Set(resolved));
  return unique.length > 0 ? unique.slice(0, 2) : ["未割当"];
}

function serviceTypeLabel(serviceType?: string) { const labels: Record<string, string> = { encoder_recorder: "Encoder / Recorder", discord_bot: "Discord Bot", observability: "Observability", worker: "Worker" }; return labels[String(serviceType || "").toLowerCase()] || serviceType || "Service"; }
function compactValues(values: Array<string | undefined>) { return values.map((value) => value?.trim() || "").filter(Boolean); }
function dateValue(value?: string) { const time = value ? new Date(value).getTime() : Number.MAX_SAFE_INTEGER; return Number.isNaN(time) ? Number.MAX_SAFE_INTEGER : time; }
function dateKey(value: Date, timezone: string) { return new Intl.DateTimeFormat("en-CA", { year: "numeric", month: "2-digit", day: "2-digit", timeZone: timezone }).format(value); }
function isSameDay(value: string | undefined, comparison: Date, timezone: string) { if (!value) return false; const date = new Date(value); return !Number.isNaN(date.getTime()) && dateKey(date, timezone) === dateKey(comparison, timezone); }
function formatToday(timezone: string) { return new Intl.DateTimeFormat("ja-JP", { dateStyle: "full", timeZone: timezone }).format(new Date()); }
function formatTime(value: string | undefined, timezone: string) { return formatDateTimeInTimeZone(value, timezone, { hour: "2-digit", minute: "2-digit" }); }
function formatDay(value: string | undefined, timezone: string) { return formatDateTimeInTimeZone(value, timezone, { month: "short", day: "numeric", weekday: "short" }); }
