"use client";

import type { ReactNode } from "react";
import { AccountMenu } from "@/components/shell/account-menu";
import { LocaleControl, ThemeControl } from "@/components/shell/display-controls";
import { GlobalCommandBoundary } from "@/components/shell/global-command-boundary";
import { StreamCreateAction } from "@/components/shell/stream-create-action";
import { ServiceHealthSummary, type ServiceHealthStatus } from "@/components/status/service-health-summary";
import { Separator } from "@/components/ui/separator";
import type { CurrentUser } from "@/types/domain";

type TopBarProps = {
  mobileNavigation: ReactNode;
  pageSection: string;
  pageTitle: string;
  pageDescription?: string;
  pathname: string;
  currentUser: CurrentUser;
  canCreateStream: boolean;
  canViewHealth: boolean;
  healthStatus: ServiceHealthStatus;
  onLogout: () => void;
  logoutPending: boolean;
};

export function TopBar(props: TopBarProps) {
  return (
    <header className="sticky top-0 z-20 flex min-h-[4.5rem] items-center justify-between gap-2 border-b bg-background/95 px-3 backdrop-blur sm:gap-3 sm:px-4 md:px-5 xl:px-6">
      <div className="flex min-w-0 items-center gap-3">
        {props.mobileNavigation}
        <div className="min-w-0">
          <div className="text-[0.7rem] font-semibold text-primary">{props.pageSection}</div>
          <div className="truncate text-base font-semibold leading-tight sm:text-lg">{props.pageTitle}</div>
          <div className="block max-w-2xl truncate text-xs text-muted-foreground max-xl:hidden">{props.pageDescription}</div>
        </div>
      </div>
      <div className="flex shrink-0 items-center gap-1.5 sm:gap-2">
        {props.canViewHealth ? <ServiceHealthSummary status={props.healthStatus} className="max-xl:hidden" /> : null}
        <GlobalCommandBoundary />
        {props.canCreateStream ? <StreamCreateAction pathname={props.pathname} className="max-md:hidden" /> : null}
        <LocaleControl />
        <ThemeControl />
        <Separator orientation="vertical" className="block h-8 max-md:hidden" />
        <AccountMenu user={props.currentUser.user} onLogout={props.onLogout} logoutPending={props.logoutPending} />
      </div>
    </header>
  );
}
