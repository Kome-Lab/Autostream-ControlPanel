"use client";

import Link from "next/link";
import { LogOut, User } from "lucide-react";
import { useI18n } from "@/components/admin/i18n-provider";
import { AccountAvatar } from "@/components/ui/account-avatar";
import { Button } from "@/components/ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import type { CurrentUser } from "@/types/domain";

export function AccountMenu({ user, onLogout, logoutPending }: { user: CurrentUser["user"]; onLogout: () => void; logoutPending: boolean }) {
  const { t } = useI18n();
  const username = user.username || "";

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" className="h-10 gap-2 px-1.5" aria-label={t("accountMenu")}>
          <AccountAvatar name={username} src={user.avatar_url} className="size-8 border-0" sizes="32px" />
          <span className="block min-w-0 text-left max-xl:hidden">
            <span className="block max-w-28 truncate text-sm font-medium">{username}</span>
            <span className="block text-xs text-muted-foreground">{t("currentUser")}</span>
          </span>
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-64">
        <DropdownMenuLabel className="flex items-center gap-3">
          <AccountAvatar name={username} src={user.avatar_url} className="size-9" sizes="36px" />
          <span className="min-w-0">
            <span className="block truncate">{username}</span>
            <span className="block truncate text-xs font-normal text-muted-foreground">{user.email || t("emailNotSet")}</span>
          </span>
        </DropdownMenuLabel>
        <DropdownMenuSeparator />
        <DropdownMenuItem asChild>
          <Link href="/admin/account/">
            <User className="size-4" aria-hidden="true" />
            {t("accountSettings")}
          </Link>
        </DropdownMenuItem>
        <DropdownMenuItem
          onSelect={(event) => {
            event.preventDefault();
            onLogout();
          }}
          disabled={logoutPending}
        >
          <LogOut className="size-4" aria-hidden="true" />
          {t("logout")}
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
