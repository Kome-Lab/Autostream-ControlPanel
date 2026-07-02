"use client";

import { ReactNode } from "react";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { Button } from "@/components/ui/button";
import { useI18n } from "@/components/admin/i18n-provider";

type RoleGuardProps = {
  allowed: boolean;
  children: ReactNode;
};

export function RoleGuard({ allowed, children }: RoleGuardProps) {
  const { t } = useI18n();
  if (allowed) return <>{children}</>;
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span className="inline-flex cursor-not-allowed">{children}</span>
      </TooltipTrigger>
      <TooltipContent>{t("roleLimited")}</TooltipContent>
    </Tooltip>
  );
}

export function guardedButtonProps(allowed: boolean) {
  return {
    disabled: !allowed,
    "aria-disabled": !allowed,
  } satisfies React.ComponentProps<typeof Button>;
}
