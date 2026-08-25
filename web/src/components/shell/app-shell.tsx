"use client";

import type { ReactNode } from "react";
import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import { useMutation } from "@tanstack/react-query";
import { useI18n } from "@/components/admin/i18n-provider";
import { AppSidebar } from "@/components/shell/app-sidebar";
import { MobileNavigation } from "@/components/shell/mobile-navigation";
import { TopBar } from "@/components/shell/top-bar";
import { useNavigationSections } from "@/components/shell/use-navigation-sections";
import { useShellSessionGuard } from "@/components/shell/use-shell-session-guard";
import { formatVersion } from "@/components/status/update-indicator";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { useAppSettings, useCurrentUser, useServiceHealth, useVersion } from "@/features/queries";
import { apiPost, clearCSRFToken } from "@/lib/api/client";
import { hasPermission } from "@/lib/auth/permissions";
import { activeNavigationItem, activeNavigationSectionKey, isSuperAdmin } from "@/lib/navigation";

export function AppShell({ children }: { children: ReactNode }) {
  const pathname = usePathname();
  const router = useRouter();
  const { locale, t } = useI18n();
  const currentUser = useCurrentUser();
  const appSettings = useAppSettings();
  const appVersion = useVersion();
  const superAdmin = isSuperAdmin(currentUser.data);
  const canViewHealth = superAdmin || hasPermission(currentUser.data, "service_health.read");
  const serviceHealth = useServiceHealth(canViewHealth);
  const activeSectionKey = activeNavigationSectionKey(pathname);
  const { synchronizedNavigationSectionsState, toggleNavigationSectionByKey } = useNavigationSections(activeSectionKey);
  const { sessionExpired } = useShellSessionGuard(currentUser);
  const logout = useMutation({
    mutationFn: () => apiPost<{ status: string }>("/auth/logout"),
    onSettled: () => {
      clearCSRFToken();
      router.replace("/login");
    },
  });

  if (currentUser.isLoading) return <ShellLoading />;

  if ((currentUser.isError && (!currentUser.data || sessionExpired)) || !currentUser.data) {
    return <ShellAuthenticationPending />;
  }

  const appName = appSettings.data?.app_name || t("appName");
  const versionLabel = formatVersion(appVersion.data?.version);
  const activeItem = activeNavigationItem(pathname);
  const accountPage = pathname.startsWith("/admin/account");
  const pageSection = accountPage ? "個人設定" : t("liveOperations");
  const pageTitle = accountPage ? "アカウント設定" : activeItem ? t(activeItem.key) : t("dashboard");
  const pageDescription = accountPage ? "プロフィールとログインセキュリティを管理" : activeItem?.description[locale];
  const canCreateStream = superAdmin || hasPermission(currentUser.data, "streams.create");
  const healthRows = serviceHealth.data || [];

  const mobileNavigation = (
    <MobileNavigation
      appName={appName}
      versionLabel={versionLabel}
      version={appVersion.data}
      pathname={pathname}
      currentUser={currentUser.data}
      sectionState={synchronizedNavigationSectionsState}
      onToggleSection={toggleNavigationSectionByKey}
      canCreateStream={canCreateStream}
      canViewHealth={canViewHealth}
      healthRows={healthRows}
    />
  );

  return (
    <div className="min-h-screen overflow-x-clip bg-background">
      <AppSidebar
        appName={appName}
        versionLabel={versionLabel}
        version={appVersion.data}
        pathname={pathname}
        currentUser={currentUser.data}
        sectionState={synchronizedNavigationSectionsState}
        onToggleSection={toggleNavigationSectionByKey}
      />
      <div className="min-w-0 lg:pl-[15.5rem]">
        <TopBar
          mobileNavigation={mobileNavigation}
          pageSection={pageSection}
          pageTitle={pageTitle}
          pageDescription={pageDescription}
          pathname={pathname}
          currentUser={currentUser.data}
          canCreateStream={canCreateStream}
          canViewHealth={canViewHealth}
          healthRows={healthRows}
          onLogout={() => logout.mutate()}
          logoutPending={logout.isPending}
        />
        <main className="mx-auto w-full max-w-[1600px] min-w-0 space-y-5 p-4 md:p-5 xl:p-6">{children}</main>
      </div>
    </div>
  );
}

function ShellLoading() {
  return (
    <main className="flex min-h-screen items-center justify-center bg-background p-6">
      <div className="w-full max-w-md space-y-4">
        <Skeleton className="h-10 w-48" />
        <Skeleton className="h-36 w-full" />
      </div>
    </main>
  );
}

function ShellAuthenticationPending() {
  return (
    <main className="flex min-h-screen items-center justify-center bg-background p-6">
      <div className="w-full max-w-md rounded-lg border bg-card p-5 shadow-sm">
        <h1 className="text-lg font-semibold">ログイン状態を確認しています</h1>
        <p className="mt-2 text-sm text-muted-foreground">セッションが切れている場合はログイン画面へ移動します。</p>
        <div className="mt-4 flex justify-end">
          <Button asChild variant="outline"><Link href="/login">ログインへ</Link></Button>
        </div>
      </div>
    </main>
  );
}
