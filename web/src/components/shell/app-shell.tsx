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
import type { ServiceHealthStatus } from "@/components/status/service-health-summary";
import { formatVersion, type UpdateStatus } from "@/components/status/update-indicator";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { useAppSettings, useCurrentUser, useServiceHealth, useVersion } from "@/features/queries";
import { apiPost, clearCSRFToken } from "@/lib/api/client";
import { hasPermission } from "@/lib/auth/permissions";
import { activeNavigationItem, activeNavigationSectionKey, isSuperAdmin } from "@/lib/navigation";
import type { AppVersion, WorkerNode } from "@/types/domain";

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
  const healthStatus = serviceHealthQueryStatus(serviceHealth);
  const updateStatus = versionQueryStatus(appVersion);
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
  const pageSection = accountPage ? t("profileSection") : t("liveOperations");
  const pageTitle = accountPage ? t("accountSettings") : activeItem ? t(activeItem.key) : t("dashboard");
  const pageDescription = accountPage ? t("accountDescription") : activeItem?.description[locale];
  const canCreateStream = superAdmin || hasPermission(currentUser.data, "streams.create");

  const mobileNavigation = (
    <MobileNavigation
      appName={appName}
      versionLabel={versionLabel}
      updateStatus={updateStatus}
      pathname={pathname}
      currentUser={currentUser.data}
      sectionState={synchronizedNavigationSectionsState}
      onToggleSection={toggleNavigationSectionByKey}
      canCreateStream={canCreateStream}
      canViewHealth={canViewHealth}
      healthStatus={healthStatus}
    />
  );

  return (
    <div className="min-h-screen overflow-x-clip bg-background">
      <AppSidebar
        appName={appName}
        versionLabel={versionLabel}
        updateStatus={updateStatus}
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
          healthStatus={healthStatus}
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
  const { t } = useI18n();

  return (
    <main className="flex min-h-screen items-center justify-center bg-background p-6">
      <div className="w-full max-w-md rounded-lg border bg-card p-5 shadow-sm">
        <h1 className="text-lg font-semibold">{t("authenticationPendingTitle")}</h1>
        <p className="mt-2 text-sm text-muted-foreground">{t("authenticationPendingDescription")}</p>
        <div className="mt-4 flex justify-end">
          <Button asChild variant="outline"><Link href="/login">{t("goToLogin")}</Link></Button>
        </div>
      </div>
    </main>
  );
}

type RemoteQueryState<T> = {
  data: T | undefined;
  isError: boolean;
  isFetching: boolean;
  isPending: boolean;
};

function serviceHealthQueryStatus(query: RemoteQueryState<WorkerNode[]>): ServiceHealthStatus {
  if (query.isError) return query.data === undefined ? { kind: "error" } : { kind: "stale", rows: query.data };
  if (query.isPending || query.data === undefined) return { kind: "loading" };
  if (query.data.length === 0) return { kind: "empty", refreshing: query.isFetching };
  return { kind: "ready", rows: query.data, refreshing: query.isFetching };
}

function versionQueryStatus(query: RemoteQueryState<AppVersion>): UpdateStatus {
  if (query.isError) return query.data === undefined ? { kind: "error" } : { kind: "stale", version: query.data };
  if (query.isPending || query.data === undefined) return { kind: "loading" };
  return { kind: "ready", version: query.data, refreshing: query.isFetching };
}
