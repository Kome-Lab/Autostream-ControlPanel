"use client";

import { useId, type ReactNode } from "react";
import { cn } from "@/lib/utils";

export function DetailSection({ title, description, actions, children, id, className, danger = false }: {
  title: ReactNode; description?: ReactNode; actions?: ReactNode; children: ReactNode;
  id?: string; className?: string; danger?: boolean;
}) {
  const heading = useId();
  return <section id={id} tabIndex={-1} aria-labelledby={heading} data-slot="detail-section"
    className={cn("min-w-0 scroll-mt-24 space-y-4 border-t py-5 first:border-t-0", danger && "border-status-critical-border", className)}>
    <div className="flex min-w-0 flex-wrap items-start justify-between gap-3">
      <div className="min-w-0">
        <h2 id={heading} className={cn("text-base font-semibold leading-6", danger && "text-status-critical")}>{title}</h2>
        {description ? <div className="mt-1 max-w-3xl text-sm leading-6 text-muted-foreground">{description}</div> : null}
      </div>
      {actions ? <div className="flex flex-wrap items-center gap-2">{actions}</div> : null}
    </div>
    <div className="min-w-0">{children}</div>
  </section>;
}

export function SectionNavigation({ label, items }: { label: string; items: readonly { id: string; label: string }[] }) {
  return <nav aria-label={label} data-slot="section-navigation" className="flex min-w-0 flex-wrap gap-x-4 gap-y-2 border-b py-3 text-sm">
    {items.map((item) => <button key={item.id} type="button" aria-controls={item.id} onClick={() => {
      const section = document.getElementById(item.id);
      section?.scrollIntoView({ block: "start" });
      section?.focus({ preventScroll: true });
    }} className="min-h-11 py-3 text-primary underline-offset-4 hover:underline focus-visible:outline-2 focus-visible:outline-ring">{item.label}</button>)}
  </nav>;
}
