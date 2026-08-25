"use client";

import { Languages, Moon, Sun } from "lucide-react";
import { useI18n } from "@/components/admin/i18n-provider";
import { useTheme } from "@/components/admin/theme-provider";
import { Button } from "@/components/ui/button";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { cn } from "@/lib/utils";
import type { Locale } from "@/types/domain";

export function LocaleControl({ mobile = false }: { mobile?: boolean }) {
  const { locale, setLocale, t } = useI18n();
  return (
    <Select value={locale} onValueChange={(value) => setLocale(value as Locale)}>
      <SelectTrigger className={cn("h-9 w-28", mobile ? "min-h-11 w-full" : "flex max-sm:hidden")} aria-label={t("language")}>
        <Languages className="size-4" aria-hidden="true" />
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        <SelectItem value="ja">日本語</SelectItem>
        <SelectItem value="en">English</SelectItem>
      </SelectContent>
    </Select>
  );
}

export function ThemeControl() {
  const { t } = useI18n();
  const { dark, toggleTheme } = useTheme();
  return (
    <Button variant="outline" size="icon-sm" onClick={toggleTheme} aria-label={t("theme")}>
      {dark ? <Moon aria-hidden="true" /> : <Sun aria-hidden="true" />}
    </Button>
  );
}
