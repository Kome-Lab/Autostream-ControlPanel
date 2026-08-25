"use client";

import type { ComponentType } from "react";
import Link from "next/link";
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
  Layers,
  LineChart,
  MessageCircle,
  Network,
  PlaySquare,
  Plug,
  ServerCog,
  Settings,
  Shield,
  Users,
  Video,
  Wrench,
} from "lucide-react";
import { useI18n } from "@/components/admin/i18n-provider";
import { SheetClose } from "@/components/ui/sheet";
import { cn } from "@/lib/utils";
import { isActivePath, type NavigationIconName, type NavigationSection, visibleNavigationSections } from "@/lib/navigation";
import { isNavigationSectionOpen, navigationSectionStateKey, type NavigationSectionsState } from "@/lib/navigation-section-state";
import type { CurrentUser } from "@/types/domain";

const navigationIcons: Record<NavigationIconName, ComponentType<{ className?: string }>> = {
  activity: Activity,
  "alert-triangle": AlertTriangle,
  archive: Archive,
  "bar-chart": BarChart3,
  bell: Bell,
  captions: Captions,
  "clipboard-list": ClipboardList,
  "file-text": FileText,
  gauge: Gauge,
  "hard-drive": HardDrive,
  info: Info,
  "key-round": KeyRound,
  layers: Layers,
  "line-chart": LineChart,
  "message-circle": MessageCircle,
  network: Network,
  "play-square": PlaySquare,
  plug: Plug,
  "server-cog": ServerCog,
  settings: Settings,
  shield: Shield,
  users: Users,
  video: Video,
  wrench: Wrench,
};

export function AppNavigation({ pathname, currentUser, sectionState, onToggleSection, mobile = false }: { pathname: string; currentUser: CurrentUser; sectionState: NavigationSectionsState; onToggleSection: (sectionKey: string) => void; mobile?: boolean }) {
  const { t } = useI18n();
  const visibleSections = visibleNavigationSections(currentUser);

  return (
    <nav aria-label={t("navigationMenu")} className="space-y-1.5">
      {visibleSections.map((section) => (
        <NavigationSection
          key={navigationSectionStateKey(section.key)}
          section={section}
          pathname={pathname}
          mobile={mobile}
          open={isNavigationSectionOpen(sectionState, section.key)}
          onToggle={() => onToggleSection(section.key)}
        />
      ))}
    </nav>
  );
}

function NavigationSection({ section, pathname, mobile, open, onToggle }: { section: NavigationSection; pathname: string; mobile: boolean; open: boolean; onToggle: () => void }) {
  const { t } = useI18n();

  return (
    <div>
      <button
        type="button"
        className="flex min-h-10 w-full items-center justify-between rounded-md px-2.5 py-2 text-[0.7rem] font-semibold text-sidebar-foreground/56 transition-colors hover:bg-sidebar-accent/45 hover:text-sidebar-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-sidebar-ring"
        aria-expanded={open}
        onClick={onToggle}
      >
        <span>{t(section.key)}</span>
        <ChevronDown className={cn("size-3.5 transition-transform", open && "rotate-180")} aria-hidden="true" />
      </button>
      {open ? (
        <div className="mt-0.5 space-y-0.5 pb-1.5">
          {section.items.map((item) => {
            const Icon = navigationIcons[item.icon];
            const active = isActivePath(pathname, item.href);
            const link = (
              <Link
                href={item.href}
                aria-current={active ? "page" : undefined}
                className={cn(
                  "flex min-h-10 items-center gap-2.5 rounded-md border-l-2 border-transparent px-2.5 py-1.5 text-[0.82rem] font-medium outline-none transition-colors focus-visible:ring-2 focus-visible:ring-sidebar-ring",
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
              <SheetClose asChild key={item.href}>{link}</SheetClose>
            ) : (
              <div key={item.href}>{link}</div>
            );
          })}
        </div>
      ) : null}
    </div>
  );
}
