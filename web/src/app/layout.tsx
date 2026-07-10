import type { Metadata } from "next";
import "./globals.css";
import { I18nProvider } from "@/components/admin/i18n-provider";
import { QueryProvider } from "@/components/admin/query-provider";
import { ThemeProvider } from "@/components/admin/theme-provider";
import { TooltipProvider } from "@/components/ui/tooltip";

export const metadata: Metadata = {
  title: "AutoStream Control Panel",
  description: "Control Panel for Discord-triggered live streams, workers, audit logs, and node registration.",
  icons: [{ rel: "icon", url: "/favicon.svg", type: "image/svg+xml" }],
};

export default function RootLayout({ children }: Readonly<{ children: React.ReactNode }>) {
  return (
    <html lang="ja" suppressHydrationWarning>
      <body>
        <ThemeProvider>
          <I18nProvider>
            <QueryProvider>
              <TooltipProvider delayDuration={250}>{children}</TooltipProvider>
            </QueryProvider>
          </I18nProvider>
        </ThemeProvider>
      </body>
    </html>
  );
}
