"use client";

import type { ComponentType, ReactNode } from "react";
import { useEffect } from "react";
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
  ClipboardList,
  FileText,
  Gauge,
  HardDrive,
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
import { Sheet, SheetContent, SheetTitle, SheetTrigger } from "@/components/ui/sheet";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Separator } from "@/components/ui/separator";
import { Skeleton } from "@/components/ui/skeleton";
import { cn } from "@/lib/utils";
import { useI18n } from "@/components/admin/i18n-provider";
import { useTheme } from "@/components/admin/theme-provider";
import { apiGet, apiPost, clearCSRFToken } from "@/lib/api/client";
import { useAppSettings, useCurrentUser, useVersion } from "@/features/queries";
import type { Locale, SetupStatus } from "@/types/domain";
import type { TranslationKey } from "@/lib/i18n";

type NavItem = {
  href: string;
  key: TranslationKey;
  icon: ComponentType<{ className?: string }>;
};

type NavSection = {
  key: TranslationKey;
  items: NavItem[];
};

const navSections: NavSection[] = [
  {
    key: "navOperations",
    items: [
      { href: "/admin/", key: "dashboard", icon: BarChart3 },
      { href: "/admin/streams/", key: "streams", icon: PlaySquare },
      { href: "/admin/workers/", key: "workers", icon: ServerCog },
      { href: "/admin/service-health/", key: "serviceHealth", icon: Activity },
      { href: "/admin/logs/", key: "logs", icon: FileText },
    ],
  },
  {
    key: "navProfiles",
    items: [
      { href: "/admin/encoder/", key: "encoder", icon: Gauge },
      { href: "/admin/discord/", key: "discord", icon: MessageCircle },
      { href: "/admin/youtube/", key: "youtube", icon: Video },
      { href: "/admin/caption/", key: "caption", icon: Captions },
      { href: "/admin/overlay/", key: "overlay", icon: Layers },
      { href: "/admin/archive/", key: "archive", icon: Archive },
    ],
  },
  {
    key: "navMonitoring",
    items: [
      { href: "/admin/monitoring/", key: "monitoring", icon: LineChart },
      { href: "/admin/incidents/", key: "incidents", icon: AlertTriangle },
      { href: "/admin/diagnostics/", key: "diagnostics", icon: Wrench },
      { href: "/admin/remediation/", key: "remediation", icon: HardDrive },
      { href: "/admin/notifications/", key: "notifications", icon: Bell },
      { href: "/admin/metrics/", key: "metrics", icon: BarChart3 },
    ],
  },
  {
    key: "navAdministration",
    items: [
      { href: "/admin/users/", key: "users", icon: Users },
      { href: "/admin/roles/", key: "roles", icon: Shield },
      { href: "/admin/audit-logs/", key: "auditLogs", icon: ClipboardList },
      { href: "/admin/security/", key: "security", icon: KeyRound },
      { href: "/admin/nodes/", key: "nodeRegistration", icon: Network },
      { href: "/admin/integrations/", key: "integrations", icon: Plug },
      { href: "/admin/settings/", key: "settings", icon: Settings },
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
        <div className="w-full max-w-md rounded-md border bg-card p-5 shadow-sm">
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

  const nav = <Navigation pathname={pathname} />;
  const appName = appSettings.data?.app_name || t("appName");
  const versionLabel = appVersion.data?.version || "dev";
  const updateAvailable = appVersion.data?.update_available && appVersion.data.latest_version;
  const updateCheckFailed = !updateAvailable && appVersion.data?.update_check_error;

  return (
    <div className="min-h-screen bg-background">
      <aside className="fixed inset-y-0 left-0 z-30 hidden w-64 border-r bg-sidebar text-sidebar-foreground lg:block">
        <div className="flex h-16 items-center gap-3 border-b border-sidebar-border px-5">
          <div className="flex size-9 items-center justify-center rounded-md bg-sidebar-primary text-sidebar-primary-foreground">
            <RadioTower className="size-5" />
          </div>
          <div>
            <div className="font-semibold">{appName}</div>
            <div className="text-xs text-sidebar-foreground/70">Control Panel {versionLabel}</div>
            {updateAvailable ? <div className="text-xs font-medium text-amber-300">新しいバージョン {appVersion.data?.latest_version}</div> : null}
            {updateCheckFailed ? <div className="text-xs font-medium text-amber-300">バージョン確認失敗</div> : null}
          </div>
        </div>
        <ScrollArea className="h-[calc(100vh-4rem)] px-3 py-4">{nav}</ScrollArea>
      </aside>

      <div className="lg:pl-64">
        <header className="sticky top-0 z-20 flex h-16 items-center justify-between border-b bg-background/95 px-4 backdrop-blur lg:px-6">
          <div className="flex items-center gap-2">
            <Sheet>
              <SheetTrigger asChild>
                <Button variant="outline" size="icon-sm" className="lg:hidden" aria-label="Open navigation">
                  <Menu />
                </Button>
              </SheetTrigger>
              <SheetContent side="left" className="w-72 p-0">
                <SheetTitle className="sr-only">Navigation</SheetTitle>
                <div className="flex h-16 items-center gap-3 border-b px-5">
                  <RadioTower className="size-5" />
                  <span className="font-semibold">{appName}</span>
                </div>
                <div className="p-3">{nav}</div>
              </SheetContent>
            </Sheet>
            <div>
              <div className="text-sm text-muted-foreground">{t("liveOperations")}</div>
              <div className="flex flex-wrap items-center gap-2">
                <div className="text-lg font-semibold leading-tight">{activeTitle(pathname, t)}</div>
                <span className="rounded-md border px-2 py-0.5 text-xs text-muted-foreground">Control Panel {versionLabel}</span>
                {updateAvailable ? (
                  <span className="inline-flex items-center gap-1 rounded-md border border-amber-300 bg-amber-50 px-2 py-0.5 text-xs font-medium text-amber-800">
                    <AlertTriangle className="size-3" />
                    新しいバージョン {appVersion.data?.latest_version}
                  </span>
                ) : null}
                {updateCheckFailed ? (
                  <span className="inline-flex items-center gap-1 rounded-md border border-amber-300 bg-amber-50 px-2 py-0.5 text-xs font-medium text-amber-800" title={appVersion.data?.update_check_error}>
                    <AlertTriangle className="size-3" />
                    バージョン確認失敗
                  </span>
                ) : null}
              </div>
            </div>
          </div>
          <div className="flex items-center gap-2">
            <Select value={locale} onValueChange={(value) => setLocale(value as Locale)}>
              <SelectTrigger className="h-9 w-28">
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
            <Separator orientation="vertical" className="hidden h-8 sm:block" />
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button variant="ghost" className="h-11 gap-2 px-2">
                  <span className="flex size-8 items-center justify-center rounded-full bg-primary/10 text-sm font-semibold text-primary">
                    {userInitial(username)}
                  </span>
                  <span className="hidden min-w-0 text-left sm:block">
                    <span className="block max-w-32 truncate text-sm font-medium">{username}</span>
                    <span className="block text-xs text-muted-foreground">{t("currentUser")}</span>
                  </span>
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end" className="w-56">
                <DropdownMenuLabel className="flex items-center gap-2">
                  <span className="flex size-8 items-center justify-center rounded-full bg-primary/10 text-sm font-semibold text-primary">
                    {userInitial(username)}
                  </span>
                  <span className="min-w-0">
                    <span className="block truncate">{username}</span>
                    <span className="block text-xs font-normal text-muted-foreground">{currentUser.data.user.email || "メール未設定"}</span>
                  </span>
                </DropdownMenuLabel>
                <DropdownMenuSeparator />
                <DropdownMenuItem asChild>
                  <Link href="/admin/account/">
                    <User className="size-4" />
                    ユーザー情報
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
        <main className="mx-auto w-full max-w-7xl space-y-6 p-4 lg:p-6">{children}</main>
      </div>
    </div>
  );
}

function userInitial(username: string) {
  return (username.trim().charAt(0) || "U").toUpperCase();
}

function Navigation({ pathname }: { pathname: string }) {
  const { t } = useI18n();
  return (
    <nav className="space-y-5">
      {navSections.map((section) => (
        <div key={section.key} className="space-y-1">
          <div className="px-3 text-[0.68rem] font-semibold uppercase tracking-[0.08em] text-sidebar-foreground/45">{t(section.key)}</div>
          {section.items.map((item) => {
            const Icon = item.icon;
            const active = pathname === item.href || (item.href !== "/admin/" && pathname.startsWith(item.href));
            return (
              <Link
                key={item.href}
                href={item.href}
                className={cn(
                  "flex items-center gap-3 rounded-md px-3 py-2 text-sm font-medium transition-colors",
                  active
                    ? "bg-sidebar-accent text-sidebar-accent-foreground"
                    : "text-sidebar-foreground/78 hover:bg-sidebar-accent/70 hover:text-sidebar-accent-foreground",
                )}
              >
                <Icon className="size-4" />
                {t(item.key)}
              </Link>
            );
          })}
        </div>
      ))}
    </nav>
  );
}

function activeTitle(pathname: string, t: ReturnType<typeof useI18n>["t"]) {
  const item = navItems.find((navItem) => pathname === navItem.href || (navItem.href !== "/admin/" && pathname.startsWith(navItem.href)));
  return item ? t(item.key) : t("dashboard");
}
