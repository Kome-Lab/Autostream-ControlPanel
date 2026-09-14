"use client";

import { useMemo } from "react";
import { useI18n } from "@/components/admin/i18n-provider";
import { createUICopy } from "./copy";

export function useUICopy() {
  const { locale } = useI18n();
  return useMemo(() => createUICopy(locale), [locale]);
}
