"use client";

import { useEffect, useRef } from "react";
import { usePathname } from "next/navigation";
import { useAppSettings } from "@/features/queries";
import {
  googleAnalyticsPageLocation,
  isGoogleAnalyticsPathAllowed,
  normalizeGoogleAnalyticsMeasurementID,
} from "@/lib/google-analytics";

type GTag = (...args: unknown[]) => void;

declare global {
  interface Window {
    dataLayer?: unknown[];
    gtag?: GTag;
  }
}

const scriptID = "autostream-google-analytics";

export function GoogleAnalytics() {
  const settings = useAppSettings();
  const pathname = usePathname();
  const lastPageView = useRef("");
  const measurementID = isGoogleAnalyticsPathAllowed(pathname) && settings.data?.google_analytics_enabled
    ? normalizeGoogleAnalyticsMeasurementID(settings.data.google_analytics_measurement_id)
    : "";

  useEffect(() => {
    if (!measurementID) {
      window.gtag?.("consent", "update", { analytics_storage: "denied" });
      document.getElementById(scriptID)?.remove();
      lastPageView.current = "";
      return;
    }

    window.dataLayer = window.dataLayer || [];
    if (!window.gtag) {
      window.gtag = (...args: unknown[]) => window.dataLayer?.push(args);
      window.gtag("consent", "default", {
        analytics_storage: "denied",
        ad_storage: "denied",
        ad_user_data: "denied",
        ad_personalization: "denied",
      });
      window.gtag("js", new Date());
    }
    window.gtag("consent", "update", { analytics_storage: "granted" });
    window.gtag("config", measurementID, {
      send_page_view: false,
      allow_google_signals: false,
      allow_ad_personalization_signals: false,
      cookie_flags: "SameSite=Strict;Secure",
    });

    if (!document.getElementById(scriptID)) {
      const source = new URL("/gtag/js", "https://www.googletagmanager.com");
      source.searchParams.set("id", measurementID);
      const script = document.createElement("script");
      script.id = scriptID;
      script.async = true;
      script.src = source.toString();
      document.head.appendChild(script);
    }
    lastPageView.current = "";

    return () => {
      window.gtag?.("consent", "update", { analytics_storage: "denied" });
      document.getElementById(scriptID)?.remove();
      lastPageView.current = "";
    };
  }, [measurementID]);

  useEffect(() => {
    if (!measurementID || !window.gtag || !pathname) return;
    const pageLocation = googleAnalyticsPageLocation(window.location.origin, pathname);
    const pageViewKey = `${measurementID}:${pageLocation}`;
    if (lastPageView.current === pageViewKey) return;
    lastPageView.current = pageViewKey;
    window.gtag("event", "page_view", {
      page_location: pageLocation,
      page_path: pathname,
      page_title: document.title,
    });
  }, [measurementID, pathname]);

  return null;
}
