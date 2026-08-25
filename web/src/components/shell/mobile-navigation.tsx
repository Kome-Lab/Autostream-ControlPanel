"use client";

import { useEffect, useRef, useState } from "react";
import { Menu, X } from "lucide-react";
import { useI18n } from "@/components/admin/i18n-provider";
import { AppNavigation } from "@/components/shell/app-navigation";
import { LocaleControl } from "@/components/shell/display-controls";
import { ShellBrand } from "@/components/shell/shell-brand";
import { StreamCreateAction } from "@/components/shell/stream-create-action";
import { ServiceHealthSummary, type ServiceHealthStatus } from "@/components/status/service-health-summary";
import { UpdateIndicator, type UpdateStatus } from "@/components/status/update-indicator";
import { Button } from "@/components/ui/button";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Sheet, SheetClose, SheetContent, SheetTitle, SheetTrigger } from "@/components/ui/sheet";
import type { NavigationSectionsState } from "@/lib/navigation-section-state";
import type { CurrentUser } from "@/types/domain";

type MobileNavigationProps = {
  appName: string;
  versionLabel: string;
  updateStatus: UpdateStatus;
  pathname: string;
  currentUser: CurrentUser;
  sectionState: NavigationSectionsState;
  onToggleSection: (sectionKey: string) => void;
  canCreateStream: boolean;
  canViewHealth: boolean;
  healthStatus: ServiceHealthStatus;
};

export function MobileNavigation(props: MobileNavigationProps) {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const pendingNavigationRef = useRef<(() => void) | null>(null);
  const createCloseObserverRef = useRef<MutationObserver | null>(null);

  useEffect(() => () => createCloseObserverRef.current?.disconnect(), []);

  const restoreTriggerWhenCreateCloses = () => {
    createCloseObserverRef.current?.disconnect();
    const observer = new MutationObserver(() => {
      const createDialog = document.querySelector("#create-stream")?.closest('[role="dialog"]');
      if (window.location.hash === "#create-stream" || createDialog) return;
      observer.disconnect();
      createCloseObserverRef.current = null;
      triggerRef.current?.focus();
    });
    createCloseObserverRef.current = observer;
    observer.observe(document.body, { childList: true, subtree: true });
  };

  const navigateAfterClose = (navigate: () => void) => {
    pendingNavigationRef.current = navigate;
    setOpen(false);
  };

  return (
    <Sheet
      open={open}
      onOpenChange={(nextOpen) => {
        if (nextOpen) pendingNavigationRef.current = null;
        setOpen(nextOpen);
      }}
    >
      <SheetTrigger asChild>
        <Button ref={triggerRef} variant="outline" size="icon-sm" className="lg:hidden" aria-label={t("navigationOpen")}>
          <Menu aria-hidden="true" />
        </Button>
      </SheetTrigger>
      <SheetContent
        side="left"
        showCloseButton={false}
        className="mobile-navigation-sheet w-[min(20rem,calc(100vw-2rem))] gap-0 border-sidebar-border bg-sidebar p-0 text-sidebar-foreground sm:max-w-[20rem]"
        onCloseAutoFocus={(event) => {
          const navigate = pendingNavigationRef.current;
          if (!navigate) return;
          pendingNavigationRef.current = null;
          event.preventDefault();
          triggerRef.current?.focus();
          restoreTriggerWhenCreateCloses();
          navigate();
        }}
      >
        <SheetTitle className="sr-only">{t("navigationTitle")}</SheetTitle>
        <div className="flex min-h-[4.5rem] shrink-0 items-center border-b border-sidebar-border px-4 py-2 pr-12">
          <ShellBrand appName={props.appName} versionLabel={props.versionLabel} />
        </div>
        <ScrollArea className="min-h-0 flex-1">
          <div className="space-y-3 p-2.5">
            <div className="space-y-2 rounded-lg border border-sidebar-border bg-sidebar-accent/35 p-2.5">
              {props.canCreateStream ? <StreamCreateAction pathname={props.pathname} mobile onNavigateAfterClose={navigateAfterClose} /> : null}
              {props.canViewHealth ? <ServiceHealthSummary status={props.healthStatus} className="w-full justify-center" /> : null}
              <LocaleControl mobile />
              <UpdateIndicator status={props.updateStatus} />
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
        <SheetClose asChild>
          <Button
            variant="ghost"
            size="icon-sm"
            className="absolute top-4 right-4 text-sidebar-foreground/75 hover:bg-sidebar-accent hover:text-sidebar-accent-foreground"
            aria-label={t("navigationClose")}
          >
            <X aria-hidden="true" />
          </Button>
        </SheetClose>
      </SheetContent>
    </Sheet>
  );
}
