"use client";

import { useEffect, useRef } from "react";
import { usePathname } from "next/navigation";
import { useAppSettings } from "@/features/queries";
import {
  createGoogleTagCommandQueue,
  googleAnalyticsPageLocation,
  type GoogleTagCommand,
  isGoogleAnalyticsPathAllowed,
  normalizeGoogleAnalyticsMeasurementID,
  shouldSendGoogleAnalyticsPageView,
} from "@/lib/google-analytics";

declare global {
  interface Window {
    dataLayer?: unknown[];
    gtag?: GoogleTagCommand;
    __AUTOSTREAM_GOOGLE_ANALYTICS_ID__?: string;
    __AUTOSTREAM_GOOGLE_ANALYTICS_PAGE_VIEW__?: string;
  }
}

const scriptID = "autostream-google-analytics";
const bootstrapScriptID = "autostream-google-analytics-bootstrap";

export function GoogleAnalytics() {
  const settings = useAppSettings();
  const pathname = usePathname();
  const lastPageView = useRef("");
  const measurementID = isGoogleAnalyticsPathAllowed(pathname) && settings.data?.google_analytics_enabled
    ? normalizeGoogleAnalyticsMeasurementID(settings.data.google_analytics_measurement_id)
    : "";

  useEffect(() => {
    if (settings.isPending) return;

    if (!measurementID) {
      window.gtag?.("consent", "update", { analytics_storage: "denied" });
      document.getElementById(scriptID)?.remove();
      document.getElementById(bootstrapScriptID)?.remove();
      delete window.__AUTOSTREAM_GOOGLE_ANALYTICS_ID__;
      delete window.__AUTOSTREAM_GOOGLE_ANALYTICS_PAGE_VIEW__;
      lastPageView.current = "";
      return;
    }

    window.dataLayer = window.dataLayer || [];
    if (!window.gtag) {
      window.gtag = createGoogleTagCommandQueue(window.dataLayer);
      window.gtag("consent", "default", {
        analytics_storage: "denied",
        ad_storage: "denied",
        ad_user_data: "denied",
        ad_personalization: "denied",
      });
      window.gtag("js", new Date());
    }
    window.gtag("consent", "update", { analytics_storage: "granted" });

    if (window.__AUTOSTREAM_GOOGLE_ANALYTICS_ID__ !== measurementID) {
      window.gtag("config", measurementID, {
        send_page_view: false,
        allow_google_signals: false,
        allow_ad_personalization_signals: false,
        cookie_flags: "SameSite=Strict;Secure",
      });
      window.__AUTOSTREAM_GOOGLE_ANALYTICS_ID__ = measurementID;
    }

    const existingScript = document.getElementById(scriptID) as HTMLScriptElement | null;
    const existingMeasurementID = existingScript
      ? existingScript.dataset.measurementId || new URL(existingScript.src).searchParams.get("id") || ""
      : "";
    if (existingScript && existingMeasurementID !== measurementID) {
      existingScript.remove();
    }
    if (!document.getElementById(scriptID)) {
      const source = new URL("/gtag/js", "https://www.googletagmanager.com");
      source.searchParams.set("id", measurementID);
      const script = document.createElement("script");
      script.id = scriptID;
      script.dataset.measurementId = measurementID;
      script.async = true;
      script.src = source.toString();
      document.head.appendChild(script);
    }
    lastPageView.current = "";

    return () => {
      window.gtag?.("consent", "update", { analytics_storage: "denied" });
      document.getElementById(scriptID)?.remove();
      document.getElementById(bootstrapScriptID)?.remove();
      delete window.__AUTOSTREAM_GOOGLE_ANALYTICS_ID__;
      delete window.__AUTOSTREAM_GOOGLE_ANALYTICS_PAGE_VIEW__;
      lastPageView.current = "";
    };
  }, [measurementID, settings.isPending]);

  useEffect(() => {
    if (!measurementID || !window.gtag || !pathname) return;
    const pageLocation = googleAnalyticsPageLocation(window.location.origin, pathname);
    const pageViewKey = `${measurementID}:${pageLocation}`;
    if (!shouldSendGoogleAnalyticsPageView(pageViewKey, lastPageView.current, window.__AUTOSTREAM_GOOGLE_ANALYTICS_PAGE_VIEW__)) {
      lastPageView.current = pageViewKey;
      return;
    }
    lastPageView.current = pageViewKey;
    window.__AUTOSTREAM_GOOGLE_ANALYTICS_PAGE_VIEW__ = pageViewKey;
    window.gtag("event", "page_view", {
      page_location: pageLocation,
      page_path: pathname,
      page_title: document.title,
    });
  }, [measurementID, pathname]);

  return null;
}
