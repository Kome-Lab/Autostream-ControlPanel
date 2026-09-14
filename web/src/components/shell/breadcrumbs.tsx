"use client";

import { useI18n } from "@/components/admin/i18n-provider";
import Link from "next/link";
import { ChevronRight } from "lucide-react";
import { cn } from "@/lib/utils";

export type BreadcrumbItem = {
  label: string;
  href?: string;
};

export function Breadcrumbs({ items, ariaLabel, className }: { items: readonly BreadcrumbItem[]; ariaLabel?: string; className?: string }) {
  const { locale } = useI18n();
  if (items.length === 0) return null;

  return (
    <nav aria-label={ariaLabel ?? (locale === "ja" ? "パンくず" : "Breadcrumbs")} className={cn("min-w-0", className)}>
      <ol className="flex min-w-0 flex-wrap items-center gap-x-1 gap-y-0.5 text-xs text-muted-foreground">
        {items.map((item, index) => {
          const current = index === items.length - 1;
          return (
            <li key={`${item.href || "current"}-${item.label}`} className="flex min-w-0 items-center gap-1">
              {index > 0 ? <ChevronRight className="size-3.5 shrink-0" aria-hidden="true" /> : null}
              {item.href && !current ? (
                <Link href={item.href} className="max-w-48 truncate rounded-sm outline-none hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring">
                  {item.label}
                </Link>
              ) : (
                <span aria-current="page" className={cn("max-w-56 truncate", current && "font-medium text-foreground")}>
                  {item.label}
                </span>
              )}
            </li>
          );
        })}
      </ol>
    </nav>
  );
}
