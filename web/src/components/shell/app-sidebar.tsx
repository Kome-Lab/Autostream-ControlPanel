"use client";

import { AppNavigation } from "@/components/shell/app-navigation";
import { ShellBrand } from "@/components/shell/shell-brand";
import { UpdateIndicator } from "@/components/status/update-indicator";
import { ScrollArea } from "@/components/ui/scroll-area";
import type { NavigationSectionsState } from "@/lib/navigation-section-state";
import type { AppVersion, CurrentUser } from "@/types/domain";

type AppSidebarProps = {
  appName: string;
  versionLabel: string;
  version?: AppVersion;
  pathname: string;
  currentUser: CurrentUser;
  sectionState: NavigationSectionsState;
  onToggleSection: (sectionKey: string) => void;
};

export function AppSidebar({ appName, versionLabel, version, pathname, currentUser, sectionState, onToggleSection }: AppSidebarProps) {
  return (
    <aside className="fixed inset-y-0 left-0 z-30 flex w-[15.5rem] flex-col border-r border-sidebar-border bg-sidebar text-sidebar-foreground max-lg:hidden">
      <div className="flex min-h-[4.5rem] shrink-0 items-center border-b border-sidebar-border px-4 py-2">
        <ShellBrand appName={appName} versionLabel={versionLabel} />
      </div>
      <ScrollArea className="min-h-0 flex-1">
        <div className="px-2.5 py-3">
          <AppNavigation pathname={pathname} currentUser={currentUser} sectionState={sectionState} onToggleSection={onToggleSection} />
        </div>
      </ScrollArea>
      <div className="shrink-0 border-t border-sidebar-border px-4 py-3 text-xs text-sidebar-foreground/58">
        <div className="flex items-center justify-between gap-2">
          <span>Control Panel</span>
          <span className="font-medium text-sidebar-foreground/80">{versionLabel}</span>
        </div>
        <UpdateIndicator version={version} />
      </div>
    </aside>
  );
}
