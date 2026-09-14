import type { ReactNode } from "react";
import { cn } from "@/lib/utils";

export function DefinitionList({ items, className }: {
  items: readonly { label: string; value: ReactNode }[]; className?: string;
}) {
  return <dl data-slot="definition-list" className={cn("grid min-w-0 gap-x-6 gap-y-4 sm:grid-cols-2", className)}>
    {items.map((item) => <div key={item.label} className="min-w-0">
      <dt className="text-xs leading-5 text-muted-foreground">{item.label}</dt>
      <dd className="mt-1 text-sm leading-6 [overflow-wrap:anywhere]">{item.value}</dd>
    </div>)}
  </dl>;
}
