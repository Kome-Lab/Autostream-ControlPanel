import type { Metadata } from "next";
import Script from "next/script";
import "./globals.css";
import { I18nProvider } from "@/components/admin/i18n-provider";
import { GoogleAnalytics } from "@/components/admin/google-analytics";
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
				<Script src="/theme-bootstrap.js" strategy="beforeInteractive" />
        <QueryProvider>
          <ThemeProvider>
            <I18nProvider>
              <GoogleAnalytics />
              <TooltipProvider delayDuration={250}>{children}</TooltipProvider>
            </I18nProvider>
          </ThemeProvider>
        </QueryProvider>
      </body>
    </html>
  );
}
