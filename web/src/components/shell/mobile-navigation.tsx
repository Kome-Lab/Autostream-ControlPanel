"use client";

import { Menu } from "lucide-react";
import { AppNavigation } from "@/components/shell/app-navigation";
import { LocaleControl } from "@/components/shell/display-controls";
import { ShellBrand } from "@/components/shell/shell-brand";
import { StreamCreateAction } from "@/components/shell/stream-create-action";
import { ServiceHealthSummary } from "@/components/status/service-health-summary";
import { UpdateIndicator } from "@/components/status/update-indicator";
import { Button } from "@/components/ui/button";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Sheet, SheetContent, SheetTitle, SheetTrigger } from "@/components/ui/sheet";
import type { NavigationSectionsState } from "@/lib/navigation-section-state";
import type { AppVersion, CurrentUser, WorkerNode } from "@/types/domain";

type MobileNavigationProps = {
  appName: string;
  versionLabel: string;
  version?: AppVersion;
  pathname: string;
  currentUser: CurrentUser;
  sectionState: NavigationSectionsState;
  onToggleSection: (sectionKey: string) => void;
  canCreateStream: boolean;
  canViewHealth: boolean;
  healthRows?: WorkerNode[];
};

export function MobileNavigation(props: MobileNavigationProps) {
  return (
    <Sheet>
      <SheetTrigger asChild>
        <Button variant="outline" size="icon-sm" className="lg:hidden" aria-label="ナビゲーションを開く">
          <Menu aria-hidden="true" />
        </Button>
      </SheetTrigger>
      <SheetContent side="left" className="w-[min(20rem,calc(100vw-2rem))] gap-0 border-sidebar-border bg-sidebar p-0 text-sidebar-foreground sm:max-w-[20rem]">
        <SheetTitle className="sr-only">ナビゲーション</SheetTitle>
        <div className="flex min-h-[4.5rem] shrink-0 items-center border-b border-sidebar-border px-4 py-2 pr-12">
          <ShellBrand appName={props.appName} versionLabel={props.versionLabel} />
        </div>
        <ScrollArea className="min-h-0 flex-1">
          <div className="space-y-3 p-2.5">
            <div className="space-y-2 rounded-lg border border-sidebar-border bg-sidebar-accent/35 p-2.5">
              {props.canCreateStream ? <StreamCreateAction pathname={props.pathname} mobile /> : null}
              {props.canViewHealth ? <ServiceHealthSummary rows={props.healthRows} className="w-full justify-center" /> : null}
              <LocaleControl mobile />
              <UpdateIndicator version={props.version} />
            </div>
            <AppNavigation
              pathname={props.pathname}
              currentUser={props.currentUser}
              sectionState={props.sectionState}
              onToggleSection={props.onToggleSection}
              mobile
            />
          </div>
        </ScrollArea>
      </SheetContent>
    </Sheet>
  );
}
