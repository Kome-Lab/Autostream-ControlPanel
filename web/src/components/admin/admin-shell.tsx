"use client";

import type { ComponentType, ReactNode } from "react";
import { useEffect, useState } from "react";
import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import { useMutation } from "@tanstack/react-query";
import {
  Activity,
  AlertTriangle,
  Archive,
  BarChart3,
  Bell,
  Captions,
  ChevronDown,
  ClipboardList,
  FileText,
  Gauge,
  HardDrive,
  Info,
  KeyRound,
  Languages,
  Layers,
  LineChart,
  LogOut,
  Menu,
  MessageCircle,
  Moon,
  Network,
  PlaySquare,
  Plug,
  Plus,
  RadioTower,
  ServerCog,
  Settings,
  Shield,
  Sun,
  User,
  Users,
  Video,
  Wrench,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { Sheet, SheetClose, SheetContent, SheetTitle, SheetTrigger } from "@/components/ui/sheet";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Separator } from "@/components/ui/separator";
import { Skeleton } from "@/components/ui/skeleton";
import { cn } from "@/lib/utils";
import { useI18n } from "@/components/admin/i18n-provider";
import { useTheme } from "@/components/admin/theme-provider";
import { apiGet, apiPost, clearCSRFToken } from "@/lib/api/client";
import { hasAnyPermission, hasPermission } from "@/lib/auth/permissions";
import { useAppSettings, useCurrentUser, useServiceHealth, useVersion } from "@/features/queries";
import type { CurrentUser, Locale, SetupStatus } from "@/types/domain";
import type { TranslationKey } from "@/lib/i18n";

type NavItem = {
  href: string;
  key: TranslationKey;
  icon: ComponentType<{ className?: string }>;
  permissions?: string[];
  description: Record<Locale, string>;
};

type NavSection = {
  key: TranslationKey;
  items: NavItem[];
};

function navItem(href: string, key: TranslationKey, icon: NavItem["icon"], permissions: string[], ja: string, en: string): NavItem {
  return { href, key, icon, permissions, description: { ja, en } };
}

const navSections: NavSection[] = [
  {
    key: "navOperations",
    items: [
      navItem("/admin/", "dashboard", BarChart3, [], "本日の配信、要対応、基盤状態をまとめて確認", "Review today's streams, action items, and platform health"),
      navItem("/admin/streams/", "streams", PlaySquare, ["streams.read"], "配信枠の予約、開始、停止、録画設定", "Schedule, start, stop, and configure recording"),
      navItem("/admin/service-health/", "serviceHealth", Activity, ["service_health.read"], "Nodeとサービスの接続状態を確認", "Review node and service availability"),
      navItem("/admin/incidents/", "incidents", AlertTriangle, ["incidents.read"], "障害の検知、確認、解決を追跡", "Track detection, acknowledgement, and resolution"),
      navItem("/admin/archive/", "archive", Archive, ["archives.read", "archive_profiles.read", "integrations.read"], "録画成果物の確認、保存、ダウンロード", "Manage recordings, retention, and downloads"),
      navItem("/admin/logs/", "logs", FileText, ["logs.read"], "配信とシステムの記録を確認", "Inspect stream and system records"),
    ],
  },
  {
    key: "navProfiles",
    items: [
      navItem("/admin/workers/", "workers", ServerCog, ["workers.read"], "WorkerとEncoderの割り当て・操作", "Assign and operate Workers and Encoders"),
      navItem("/admin/encoder/", "encoder", Gauge, ["encoder_profiles.read"], "配信品質の標準設定", "Standardize encoding quality"),
      navItem("/admin/discord/", "discord", MessageCircle, ["discord_configs.read"], "配信起動に使うDiscord BOT", "Configure Discord bots used for stream automation"),
      navItem("/admin/youtube/", "youtube", Video, ["youtube_outputs.read"], "YouTube出力と公開設定", "Configure YouTube outputs and visibility"),
      navItem("/admin/caption/", "caption", Captions, ["caption_profiles.read"], "字幕生成の標準設定", "Standardize caption generation"),
      navItem("/admin/overlay/", "overlay", Layers, ["overlay_profiles.read"], "案件ごとのウォーターマーク", "Manage stream watermarks"),
      navItem("/admin/integrations/", "integrations", Plug, ["integrations.read"], "OAuthと外部サービス接続", "Manage OAuth and external connections"),
    ],
  },
  {
    key: "navMonitoring",
    items: [
      navItem("/admin/monitoring/", "monitoring", LineChart, ["incidents.read", "service_health.read"], "運用状況と障害を横断監視", "Monitor operations and incidents"),
      navItem("/admin/diagnostics/", "diagnostics", Wrench, ["diagnostics.read"], "配信経路とサービスの診断結果", "Review stream-path and service diagnostics"),
      navItem("/admin/remediation/", "remediation", HardDrive, ["remediation.read"], "承認制の復旧操作", "Review and approve recovery actions"),
      navItem("/admin/notifications/", "notifications", Bell, ["notification_channels.read"], "通知履歴と連絡先", "Manage delivery history and destinations"),
      navItem("/admin/metrics/", "metrics", BarChart3, ["metrics.read"], "Nodeと配信基盤の時系列指標", "Inspect time-series platform metrics"),
    ],
  },
  {
    key: "navAdministration",
    items: [
      navItem("/admin/users/", "users", Users, ["users.read"], "担当者アカウントと利用状態", "Manage operator accounts and access state"),
      navItem("/admin/roles/", "roles", Shield, ["roles.read"], "役割ごとの操作権限", "Manage role-based permissions"),
      navItem("/admin/audit-logs/", "auditLogs", ClipboardList, ["audit_logs.read"], "誰が何をしたかを確認", "Review who changed what and when"),
      navItem("/admin/security/", "security", KeyRound, ["secrets.read_status", "system_settings.read"], "ログイン・MFA・シークレット設定", "Manage login, MFA, and secret settings"),
      navItem("/admin/nodes/", "nodeRegistration", Network, ["api_tokens.create"], "新しいNodeと登録トークンを発行", "Issue nodes and registration tokens"),
      navItem("/admin/registered-nodes/", "registeredNodes", ServerCog, ["api_tokens.create"], "登録済みNodeを編集・削除", "Edit and remove registered nodes"),
      navItem("/admin/application/", "applicationInfo", Info, ["system_settings.read"], "各サービスのバージョンを確認", "Review service versions"),
      navItem("/admin/settings/", "settings", Settings, ["system_settings.read"], "表示、時刻、メールサーバー設定", "Manage display, time, and mail settings"),
    ],
  },
];

const navItems = navSections.flatMap((section) => section.items);

export function AdminShell({ children }: { children: ReactNode }) {
  const pathname = usePathname();
  const router = useRouter();
  const { locale, setLocale, t } = useI18n();
  const { dark, toggleTheme } = useTheme();
  const currentUser = useCurrentUser();
  const appSettings = useAppSettings();
  const appVersion = useVersion();
  const superAdmin = isSuperAdmin(currentUser.data);
  const canViewHealth = superAdmin || hasPermission(currentUser.data, "service_health.read");
  const serviceHealth = useServiceHealth(canViewHealth);
  const username = currentUser.data?.user.username || "";
  const logout = useMutation({
    mutationFn: () => apiPost<{ status: string }>("/auth/logout"),
    onSettled: () => {
      clearCSRFToken();
      router.replace("/login");
    },
  });

  useEffect(() => {
    if (!currentUser.isError) return;
    let active = true;
    apiGet<SetupStatus>("/setup/status")
      .then((status) => {
        if (!active) return;
        router.replace(status.setup_required ? "/setup" : "/login");
      })
      .catch(() => {
        if (active) router.replace("/login");
      });
    return () => {
      active = false;
    };
  }, [currentUser.isError, router]);

  if (currentUser.isLoading) {
    return (
      <main className="flex min-h-screen items-center justify-center bg-background p-6">
        <div className="w-full max-w-md space-y-4">
          <Skeleton className="h-10 w-48" />
          <Skeleton className="h-36 w-full" />
        </div>
      </main>
    );
  }

  if (currentUser.isError || !currentUser.data) {
    return (
      <main className="flex min-h-screen items-center justify-center bg-background p-6">
        <div className="w-full max-w-md rounded-lg border bg-card p-5 shadow-sm">
          <h1 className="text-lg font-semibold">ログイン状態を確認しています</h1>
          <p className="mt-2 text-sm text-muted-foreground">セッションが切れている場合はログイン画面へ移動します。</p>
          <div className="mt-4 flex justify-end">
            <Button asChild variant="outline">
              <Link href="/login">ログインへ</Link>
            </Button>
          </div>
        </div>
      </main>
    );
  }

  const appName = appSettings.data?.app_name || t("appName");
  const versionLabel = appVersion.data?.version || "dev";
  const updateAvailable = appVersion.data?.update_available && appVersion.data.latest_version;
  const updateCheckFailed = !updateAvailable && appVersion.data?.update_check_error;
  const activeItem = activeNavigationItem(pathname);
  const healthRows = serviceHealth.data || [];
  const healthyServices = healthRows.filter(isAvailableService).length;
  const healthIsGood = healthRows.length > 0 && healthyServices === healthRows.length;
  const canCreateStream = superAdmin || hasPermission(currentUser.data, "streams.create");

  return (
    <div className="min-h-screen bg-background">
      <aside className="fixed inset-y-0 left-0 z-30 hidden w-[15.5rem] flex-col border-r border-sidebar-border bg-sidebar text-sidebar-foreground lg:flex">
        <div className="flex h-[4.5rem] shrink-0 items-center gap-3 border-b border-sidebar-border px-4">
          <div className="flex size-9 items-center justify-center rounded-lg bg-sidebar-primary text-sidebar-primary-foreground shadow-sm">
            <RadioTower className="size-5" />
          </div>
          <div className="min-w-0">
            <div className="truncate text-sm font-semibold">{appName}</div>
            <div className="text-xs text-sidebar-foreground/58">Live Operations</div>
          </div>
        </div>
        <ScrollArea className="min-h-0 flex-1">
          <div className="px-2.5 py-3">
            <Navigation pathname={pathname} currentUser={currentUser.data} />
          </div>
        </ScrollArea>
        <div className="shrink-0 border-t border-sidebar-border px-4 py-3 text-xs text-sidebar-foreground/58">
          <div className="flex items-center justify-between gap-2">
            <span>Control Panel</span>
            <span className="font-medium text-sidebar-foreground/80">{withVersionPrefix(versionLabel)}</span>
          </div>
          {updateAvailable ? <div className="mt-1 text-amber-300">更新 {withVersionPrefix(appVersion.data?.latest_version)} を利用できます</div> : null}
          {updateCheckFailed ? <div className="mt-1 text-amber-300">更新情報を確認できません</div> : null}
        </div>
      </aside>

      <div className="lg:pl-[15.5rem]">
        <header className="sticky top-0 z-20 flex min-h-[4.5rem] items-center justify-between gap-3 border-b bg-background/95 px-4 backdrop-blur md:px-5 xl:px-6">
          <div className="flex min-w-0 items-center gap-3">
            <Sheet>
              <SheetTrigger asChild>
                <Button variant="outline" size="icon-sm" className="lg:hidden" aria-label="ナビゲーションを開く">
                  <Menu />
                </Button>
              </SheetTrigger>
              <SheetContent side="left" className="w-[18rem] border-sidebar-border bg-sidebar p-0 text-sidebar-foreground sm:max-w-[18rem]">
                <SheetTitle className="sr-only">ナビゲーション</SheetTitle>
                <div className="flex h-[4.5rem] items-center gap-3 border-b border-sidebar-border px-4">
                  <div className="flex size-9 items-center justify-center rounded-lg bg-sidebar-primary text-sidebar-primary-foreground">
                    <RadioTower className="size-5" />
                  </div>
                  <div className="min-w-0">
                    <div className="truncate text-sm font-semibold">{appName}</div>
                    <div className="text-xs text-sidebar-foreground/58">Live Operations</div>
                  </div>
                </div>
                <ScrollArea className="h-[calc(100vh-4.5rem)]">
                  <div className="p-2.5">
                    <Navigation pathname={pathname} currentUser={currentUser.data} mobile />
                  </div>
                </ScrollArea>
              </SheetContent>
            </Sheet>
            <div className="min-w-0">
              <div className="text-[0.7rem] font-semibold text-primary">{t("liveOperations")}</div>
              <div className="truncate text-lg font-semibold leading-tight">{activeItem ? t(activeItem.key) : t("dashboard")}</div>
              <div className="hidden max-w-2xl truncate text-xs text-muted-foreground xl:block">{activeItem?.description[locale]}</div>
            </div>
          </div>

          <div className="flex shrink-0 items-center gap-2">
            {canViewHealth ? (
              <Link
                href="/admin/service-health/"
                className={cn(
                  "hidden h-9 items-center gap-2 rounded-md border bg-card px-3 text-xs font-medium shadow-xs transition-colors hover:bg-accent xl:flex",
                  healthRows.length > 0 && !healthIsGood && "border-amber-300 bg-amber-50 text-amber-800 dark:border-amber-800 dark:bg-amber-950/30 dark:text-amber-200",
                )}
              >
                <span className={cn("size-2 rounded-full bg-muted-foreground", healthIsGood && "bg-emerald-500", healthRows.length > 0 && !healthIsGood && "bg-amber-500")} />
                {healthRows.length > 0 ? `${healthyServices}/${healthRows.length} サービス稼働` : "稼働状況を確認中"}
              </Link>
            ) : null}
            {canCreateStream ? (
              <Button asChild size="sm" className="hidden md:inline-flex">
                <Link
                  href="/admin/streams/#create-stream"
                  onClick={(event) => {
                    if (pathname.startsWith("/admin/streams")) {
                      event.preventDefault();
                      window.location.hash = "create-stream";
                    }
                  }}
                >
                  <Plus className="size-4" />
                  配信枠を作成
                </Link>
              </Button>
            ) : null}
            <Select value={locale} onValueChange={(value) => setLocale(value as Locale)}>
              <SelectTrigger className="hidden h-9 w-28 sm:flex" aria-label={t("language")}>
                <Languages className="size-4" />
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="ja">日本語</SelectItem>
                <SelectItem value="en">English</SelectItem>
              </SelectContent>
            </Select>
            <Button variant="outline" size="icon-sm" onClick={toggleTheme} aria-label={t("theme")}>
              {dark ? <Moon /> : <Sun />}
            </Button>
            <Separator orientation="vertical" className="hidden h-8 md:block" />
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button variant="ghost" className="h-10 gap-2 px-1.5" aria-label="アカウントメニュー">
                  <span className="flex size-8 items-center justify-center rounded-full bg-primary/12 text-sm font-semibold text-primary">
                    {userInitial(username)}
                  </span>
                  <span className="hidden min-w-0 text-left xl:block">
                    <span className="block max-w-28 truncate text-sm font-medium">{username}</span>
                    <span className="block text-xs text-muted-foreground">{t("currentUser")}</span>
                  </span>
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end" className="w-64">
                <DropdownMenuLabel className="flex items-center gap-3">
                  <span className="flex size-9 items-center justify-center rounded-full bg-primary/12 text-sm font-semibold text-primary">
                    {userInitial(username)}
                  </span>
                  <span className="min-w-0">
                    <span className="block truncate">{username}</span>
                    <span className="block truncate text-xs font-normal text-muted-foreground">{currentUser.data.user.email || "メール未設定"}</span>
                  </span>
                </DropdownMenuLabel>
                <DropdownMenuSeparator />
                <DropdownMenuItem asChild>
                  <Link href="/admin/account/">
                    <User className="size-4" />
                    アカウント設定
                  </Link>
                </DropdownMenuItem>
                <DropdownMenuItem
                  onSelect={(event) => {
                    event.preventDefault();
                    logout.mutate();
                  }}
                  disabled={logout.isPending}
                >
                  <LogOut className="size-4" />
                  ログアウト
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          </div>
        </header>
        <main className="mx-auto w-full max-w-[1600px] space-y-5 p-4 md:p-5 xl:p-6">{children}</main>
      </div>
    </div>
  );
}

function Navigation({ pathname, currentUser, mobile = false }: { pathname: string; currentUser: CurrentUser; mobile?: boolean }) {
  const visibleSections = navSections
    .map((section) => ({ ...section, items: section.items.filter((item) => canSeeNavItem(item, currentUser)) }))
    .filter((section) => section.items.length > 0);

  return (
    <nav aria-label="管理メニュー" className="space-y-1.5">
      {visibleSections.map((section, sectionIndex) => (
        <NavigationSection key={section.key} section={section} pathname={pathname} mobile={mobile} initiallyOpen={sectionIndex === 0} />
      ))}
    </nav>
  );
}

function withVersionPrefix(value: string | null | undefined) {
  const normalized = String(value || "dev").trim();
  return normalized.toLowerCase().startsWith("v") ? normalized : `v${normalized}`;
}

function NavigationSection({ section, pathname, mobile, initiallyOpen }: { section: NavSection; pathname: string; mobile: boolean; initiallyOpen: boolean }) {
  const { t } = useI18n();
  const sectionActive = section.items.some((item) => isActivePath(pathname, item.href));
  const [userOpen, setUserOpen] = useState(initiallyOpen);
  const open = sectionActive || userOpen;

  return (
    <div>
      <button
        type="button"
        className="flex w-full items-center justify-between rounded-md px-2.5 py-2 text-[0.7rem] font-semibold text-sidebar-foreground/56 transition-colors hover:bg-sidebar-accent/45 hover:text-sidebar-foreground"
        aria-expanded={open}
        onClick={() => setUserOpen((value) => !value)}
      >
        <span>{t(section.key)}</span>
        <ChevronDown className={cn("size-3.5 transition-transform", open && "rotate-180")} />
      </button>
      {open ? (
        <div className="mt-0.5 space-y-0.5 pb-1.5">
          {section.items.map((item) => {
            const Icon = item.icon;
            const active = isActivePath(pathname, item.href);
            const link = (
              <Link
                href={item.href}
                aria-current={active ? "page" : undefined}
                className={cn(
                  "flex min-h-9 items-center gap-2.5 rounded-md border-l-2 border-transparent px-2.5 py-1.5 text-[0.82rem] font-medium transition-colors",
                  active
                    ? "border-sidebar-primary bg-sidebar-accent text-sidebar-accent-foreground"
                    : "text-sidebar-foreground/76 hover:bg-sidebar-accent/65 hover:text-sidebar-accent-foreground",
                )}
              >
                <Icon className="size-4" />
                <span className="min-w-0 truncate">{t(item.key)}</span>
              </Link>
            );
            return mobile ? (
              <SheetClose asChild key={item.href}>
                {link}
              </SheetClose>
            ) : (
              <div key={item.href}>{link}</div>
            );
          })}
        </div>
      ) : null}
    </div>
  );
}

function canSeeNavItem(item: NavItem, currentUser: CurrentUser) {
  if (!item.permissions || item.permissions.length === 0) return true;
  return isSuperAdmin(currentUser) || hasAnyPermission(currentUser, item.permissions);
}

function isSuperAdmin(currentUser?: CurrentUser) {
  return currentUser?.user.roles?.includes("super_admin") === true;
}

function userInitial(username: string) {
  return (username.trim().charAt(0) || "U").toUpperCase();
}

function activeNavigationItem(pathname: string) {
  return navItems.find((item) => isActivePath(pathname, item.href));
}

function isActivePath(pathname: string, href: string) {
  return pathname === href || (href !== "/admin/" && pathname.startsWith(href));
}

function isAvailableService(row: { status?: string; health_status?: string }) {
  const status = String(row.status || "").toLowerCase();
  const health = String(row.health_status || "").toLowerCase();
  return status === "online" && (!health || ["healthy", "ok", "online"].includes(health));
}
