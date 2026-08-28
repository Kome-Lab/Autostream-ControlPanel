import { createElement, type ReactElement } from "react";
import {
  Archive,
  BadgeCheck,
  CalendarClock,
  CircleAlert,
  CircleCheck,
  CircleDot,
  CircleHelp,
  CircleSlash,
  CircleStop,
  CircleX,
  Clock3,
  ClockAlert,
  Download,
  Eye,
  FilePenLine,
  Hand,
  HeartPulse,
  Lightbulb,
  Link,
  Link2Off,
  LoaderCircle,
  Network,
  PackageCheck,
  PackageOpen,
  Play,
  Radio,
  RefreshCw,
  RotateCw,
  ScanSearch,
  Share2,
  ShieldAlert,
  ShieldCheck,
  Siren,
  SkipForward,
  Stethoscope,
  TriangleAlert,
  Undo2,
  Unplug,
  Wifi,
  WifiOff,
  type LucideIcon,
} from "lucide-react";

import type { TranslationKey } from "@/lib/i18n";
import type { DomainStatusPresentation } from "@/lib/foundation/status/contracts";

export type DomainStatusBadgeProps = Readonly<{
  presentation: DomainStatusPresentation;
  translate: (key: TranslationKey) => string;
  showDetail?: boolean;
}>;

const iconByName: Readonly<Record<string, LucideIcon>> = Object.freeze({
  archive: Archive,
  "badge-check": BadgeCheck,
  "calendar-clock": CalendarClock,
  "circle-alert": CircleAlert,
  "circle-check": CircleCheck,
  "circle-dot": CircleDot,
  "circle-help": CircleHelp,
  "circle-slash": CircleSlash,
  "circle-stop": CircleStop,
  "circle-x": CircleX,
  "clock-3": Clock3,
  "clock-alert": ClockAlert,
  download: Download,
  eye: Eye,
  "file-pen-line": FilePenLine,
  hand: Hand,
  "heart-pulse": HeartPulse,
  lightbulb: Lightbulb,
  link: Link,
  "link-2-off": Link2Off,
  "loader-circle": LoaderCircle,
  network: Network,
  "network-x": Unplug,
  "package-check": PackageCheck,
  "package-open": PackageOpen,
  play: Play,
  radio: Radio,
  "refresh-cw": RefreshCw,
  "rotate-cw": RotateCw,
  "scan-search": ScanSearch,
  "share-2": Share2,
  "shield-alert": ShieldAlert,
  "shield-check": ShieldCheck,
  siren: Siren,
  "skip-forward": SkipForward,
  stethoscope: Stethoscope,
  "triangle-alert": TriangleAlert,
  "undo-2": Undo2,
  wifi: Wifi,
  "wifi-off": WifiOff,
});

const toneClass: Readonly<Record<DomainStatusPresentation["tone"], string>> = Object.freeze({
  neutral: "border-status-offline-border bg-status-offline-subtle text-status-offline-foreground",
  info: "border-status-info-border bg-status-info-subtle text-status-info-foreground",
  success: "border-status-healthy-border bg-status-healthy-subtle text-status-healthy-foreground",
  warning: "border-status-warning-border bg-status-warning-subtle text-status-warning-foreground",
  critical: "border-status-critical-border bg-status-critical-subtle text-status-critical-foreground",
  unknown: "border-status-pending-border bg-status-pending-subtle text-status-pending-foreground",
});

export function DomainStatusBadge({
  presentation,
  translate,
  showDetail = false,
}: DomainStatusBadgeProps): ReactElement {
  const Icon = iconByName[presentation.icon] || CircleHelp;
  const label = translate(presentation.labelKey);
  const detail = showDetail && presentation.detailKey ? translate(presentation.detailKey) : undefined;
  return createElement(
    "span",
    {
      className: `forced-color-adjust-auto motion-reduce:transition-none inline-flex max-w-full items-start gap-1.5 rounded-md border px-2 py-1 text-xs font-medium ${toneClass[presentation.tone]}`,
      "data-status-known": presentation.known ? "true" : "false",
      "data-status-tone": presentation.tone,
    },
    createElement(
      "span",
      { "data-status-icon": presentation.icon },
      createElement(Icon, {
        "aria-hidden": "true",
        className: "mt-0.5 size-3.5 shrink-0",
      }),
    ),
    createElement(
      "span",
      { className: "min-w-0" },
      createElement("span", null, label),
      detail ? createElement("span", { className: "block font-normal opacity-90" }, detail) : null,
      presentation.diagnosticCode
        ? createElement("span", { className: "sr-only" }, ` (${presentation.diagnosticCode})`)
        : null,
    ),
  );
}
