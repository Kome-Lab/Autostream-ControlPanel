"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { BarChart3, ClipboardList, Languages, Menu, Moon, Network, PlaySquare, RadioTower, ServerCog, Sun } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Sheet, SheetContent, SheetTitle, SheetTrigger } from "@/components/ui/sheet";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Separator } from "@/components/ui/separator";
import { cn } from "@/lib/utils";
import { useI18n } from "@/components/admin/i18n-provider";
import { useCurrentUser } from "@/features/queries";
import type { Locale } from "@/types/domain";
import { useEffect, useState } from "react";

const navItems = [
  { href: "/admin/", key: "dashboard", icon: BarChart3 },
  { href: "/admin/streams/", key: "streams", icon: PlaySquare },
  { href: "/admin/workers/", key: "workers", icon: ServerCog },
  { href: "/admin/audit-logs/", key: "auditLogs", icon: ClipboardList },
  { href: "/admin/nodes/", key: "nodeRegistration", icon: Network },
] as const;

export function AdminShell({ children }: { children: React.ReactNode }) {
	const pathname = usePathname();
	const { locale, setLocale, t } = useI18n();
	const currentUser = useCurrentUser();
	const [dark, setDark] = useState(() => {
		if (typeof window === "undefined") return false;
		const stored = window.localStorage.getItem("autostream.theme");
		return stored ? stored === "dark" : window.matchMedia("(prefers-color-scheme: dark)").matches;
	});

	useEffect(() => {
		document.documentElement.classList.toggle("dark", dark);
	}, [dark]);

  const toggleTheme = () => {
    const next = !dark;
    setDark(next);
    window.localStorage.setItem("autostream.theme", next ? "dark" : "light");
    document.documentElement.classList.toggle("dark", next);
  };

  const nav = <Navigation pathname={pathname} />;

  return (
    <div className="min-h-screen bg-background">
      <aside className="fixed inset-y-0 left-0 z-30 hidden w-64 border-r bg-sidebar text-sidebar-foreground lg:block">
        <div className="flex h-16 items-center gap-3 border-b border-sidebar-border px-5">
          <div className="flex size-9 items-center justify-center rounded-md bg-sidebar-primary text-sidebar-primary-foreground">
            <RadioTower className="size-5" />
          </div>
          <div>
            <div className="font-semibold">{t("appName")}</div>
            <div className="text-xs text-sidebar-foreground/70">Control Panel</div>
          </div>
        </div>
        <ScrollArea className="h-[calc(100vh-4rem)] px-3 py-4">{nav}</ScrollArea>
      </aside>

      <div className="lg:pl-64">
        <header className="sticky top-0 z-20 flex h-16 items-center justify-between border-b bg-background/95 px-4 backdrop-blur lg:px-6">
          <div className="flex items-center gap-2">
            <Sheet>
              <SheetTrigger asChild>
                <Button variant="outline" size="icon-sm" className="lg:hidden" aria-label="Open navigation">
                  <Menu />
                </Button>
              </SheetTrigger>
              <SheetContent side="left" className="w-72 p-0">
                <SheetTitle className="sr-only">Navigation</SheetTitle>
                <div className="flex h-16 items-center gap-3 border-b px-5">
                  <RadioTower className="size-5" />
                  <span className="font-semibold">{t("appName")}</span>
                </div>
                <div className="p-3">{nav}</div>
              </SheetContent>
            </Sheet>
            <div>
              <div className="text-sm text-muted-foreground">{t("liveOperations")}</div>
              <div className="text-lg font-semibold leading-tight">{activeTitle(pathname, t)}</div>
            </div>
          </div>
          <div className="flex items-center gap-2">
            <Select value={locale} onValueChange={(value) => setLocale(value as Locale)}>
              <SelectTrigger className="h-9 w-28">
                <Languages className="size-4" />
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="ja">日本語</SelectItem>
                <SelectItem value="en">English</SelectItem>
              </SelectContent>
            </Select>
            <Button variant="outline" size="icon-sm" onClick={toggleTheme} aria-label={t("theme")}>
              {dark ? <Moon /> : <Sun />}
            </Button>
            <Separator orientation="vertical" className="hidden h-8 sm:block" />
            <div className="hidden text-right text-sm sm:block">
              <div className="font-medium">{currentUser.data?.user.username || "demo-admin"}</div>
              <div className="text-xs text-muted-foreground">{t("currentUser")}</div>
            </div>
          </div>
        </header>
        <main className="mx-auto w-full max-w-7xl space-y-6 p-4 lg:p-6">{children}</main>
      </div>
    </div>
  );
}

function Navigation({ pathname }: { pathname: string }) {
  const { t } = useI18n();
  return (
    <nav className="space-y-1">
      {navItems.map((item) => {
        const Icon = item.icon;
        const active = pathname === item.href || (item.href !== "/admin/" && pathname.startsWith(item.href));
        return (
          <Link
            key={item.href}
            href={item.href}
            className={cn(
              "flex items-center gap-3 rounded-md px-3 py-2 text-sm font-medium transition-colors",
              active
                ? "bg-sidebar-accent text-sidebar-accent-foreground"
                : "text-sidebar-foreground/78 hover:bg-sidebar-accent/70 hover:text-sidebar-accent-foreground",
            )}
          >
            <Icon className="size-4" />
            {t(item.key)}
          </Link>
        );
      })}
    </nav>
  );
}

function activeTitle(pathname: string, t: ReturnType<typeof useI18n>["t"]) {
  const item = navItems.find((navItem) => pathname === navItem.href || (navItem.href !== "/admin/" && pathname.startsWith(navItem.href)));
  return item ? t(item.key) : t("dashboard");
}
