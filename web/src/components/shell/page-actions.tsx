"use client";

import type { ReactNode } from "react";
import { MoreHorizontal } from "lucide-react";
import { Button } from "@/components/ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { cn } from "@/lib/utils";

type PageActionsProps = {
  primary?: ReactNode;
  secondary?: ReactNode;
  overflow?: ReactNode;
  highRisk?: ReactNode;
  className?: string;
};

export function PageActions({ primary, secondary, overflow, highRisk, className }: PageActionsProps) {
  if (!primary && !secondary && !overflow && !highRisk) return null;

  return (
    <div data-slot="page-actions" className={cn("flex w-full flex-wrap items-center gap-2 sm:w-auto sm:justify-end", className)}>
      <div className="flex min-w-0 flex-1 flex-wrap items-center gap-2 sm:flex-initial">
        {primary ? <div data-slot="page-actions-primary" className="flex shrink-0 items-center">{primary}</div> : null}
        {secondary ? <div data-slot="page-actions-secondary" className="flex min-w-0 flex-wrap items-center gap-2">{secondary}</div> : null}
        {overflow ? (
          <div data-slot="page-actions-overflow" className="flex shrink-0 items-center">
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button variant="outline" size="icon-sm" className="size-11 sm:size-8" aria-label="その他の操作">
                  <MoreHorizontal />
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end" className="min-w-48">{overflow}</DropdownMenuContent>
            </DropdownMenu>
          </div>
        ) : null}
      </div>
      {highRisk ? (
        <div data-slot="page-actions-high-risk" className="flex w-full items-center gap-2 border-t border-status-critical-border pt-2 sm:w-auto sm:border-t-0 sm:border-l sm:pt-0 sm:pl-2">
          {highRisk}
        </div>
      ) : null}
    </div>
  );
}
