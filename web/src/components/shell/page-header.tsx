import type { ReactNode } from "react";
import { Breadcrumbs, type BreadcrumbItem } from "@/components/shell/breadcrumbs";
import { cn } from "@/lib/utils";

type PageHeaderProps = {
  title: string;
  description?: ReactNode;
  breadcrumbs?: readonly BreadcrumbItem[];
  eyebrow?: ReactNode;
  actions?: ReactNode;
  className?: string;
};

export function PageHeader({ title, description, breadcrumbs, eyebrow, actions, className }: PageHeaderProps) {
  return (
    <header data-slot="page-header" className={cn("min-w-0 border-b pb-5", className)}>
      {breadcrumbs?.length ? <Breadcrumbs items={breadcrumbs} className="mb-2" /> : null}
      <div className="flex min-w-0 flex-col gap-3 sm:flex-row sm:items-end sm:justify-between">
        <div className="min-w-0">
          {eyebrow ? <div className="mb-1 flex items-center gap-2 text-sm font-medium text-primary">{eyebrow}</div> : null}
          <h1 className="text-xl font-semibold leading-tight tracking-normal sm:text-2xl">{title}</h1>
          {description ? <div className="mt-1.5 max-w-3xl text-sm leading-6 text-muted-foreground">{description}</div> : null}
        </div>
        {actions ? <div className="flex w-full shrink-0 sm:w-auto">{actions}</div> : null}
      </div>
    </header>
  );
}
