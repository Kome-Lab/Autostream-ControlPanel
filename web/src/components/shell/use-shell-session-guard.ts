"use client";

import { useEffect, useRef } from "react";
import { useRouter } from "next/navigation";
import { APIError, apiGet, apiPost, clearCSRFToken } from "@/lib/api/client";
import { loginPathForLocation } from "@/lib/auth/post-login-redirect";
import type { CurrentUser, SetupStatus } from "@/types/domain";

type CurrentUserQueryState = {
  data?: CurrentUser;
  error: unknown;
  isError: boolean;
};

export function useShellSessionGuard(currentUser: CurrentUserQueryState) {
  const router = useRouter();
  const authenticatedSessionSeen = useRef(false);
  const authenticated = Boolean(currentUser.data);
  const sessionExpired = currentUser.error instanceof APIError
    && currentUser.error.status === 401
    && currentUser.error.code === "unauthorized";

  useEffect(() => {
    if (currentUser.data) authenticatedSessionSeen.current = true;
  }, [currentUser.data]);

  useEffect(() => {
    if (!authenticated) return;
    let lastRefreshAt = 0;
    let refreshInFlight = false;
    const refreshAfterActivity = () => {
      if (document.visibilityState !== "visible" || refreshInFlight) return;
      const now = Date.now();
      if (now - lastRefreshAt < 60_000) return;
      lastRefreshAt = now;
      refreshInFlight = true;
      void apiPost<{ status: string }>("/auth/session/refresh")
        .catch((error) => {
          if (error instanceof APIError && error.status === 401) {
            clearCSRFToken();
            window.location.replace(loginPathForLocation(window.location, true));
          }
        })
        .finally(() => {
          refreshInFlight = false;
        });
    };
    const activityEvents: Array<keyof WindowEventMap> = ["pointerdown", "keydown", "scroll", "focus"];
    for (const eventName of activityEvents) window.addEventListener(eventName, refreshAfterActivity, { passive: true });
    const onVisibilityChange = () => {
      if (document.visibilityState === "visible") refreshAfterActivity();
    };
    document.addEventListener("visibilitychange", onVisibilityChange);
    return () => {
      for (const eventName of activityEvents) window.removeEventListener(eventName, refreshAfterActivity);
      document.removeEventListener("visibilitychange", onVisibilityChange);
    };
  }, [authenticated]);

  useEffect(() => {
    if (!currentUser.isError) return;
    if (sessionExpired) clearCSRFToken();
    if (authenticatedSessionSeen.current) {
      if (sessionExpired) window.location.replace(loginPathForLocation(window.location, true));
      return;
    }
    let active = true;
    apiGet<SetupStatus>("/setup/status")
      .then((status) => {
        if (active) router.replace(status.setup_required ? "/setup" : loginPathForLocation(window.location));
      })
      .catch(() => {
        if (active) router.replace(loginPathForLocation(window.location));
      });
    return () => {
      active = false;
    };
  }, [currentUser.isError, router, sessionExpired]);

  return { authenticated, sessionExpired };
}
