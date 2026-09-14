import { messages as presentationLabels } from "./copy/presentation-labels";
import { messages as presentationMessages } from "./copy/presentation-messages";
import { messages as account } from "./copy/account";
import { messages as application } from "./copy/application";
import { messages as archive } from "./copy/archive";
import { messages as audit } from "./copy/audit";
import { messages as auth } from "./copy/auth";
import { messages as common } from "./copy/common";
import { messages as metrics } from "./copy/metrics";
import { messages as monitoring } from "./copy/monitoring";
import { messages as nodes } from "./copy/nodes";
import { messages as permissions } from "./copy/permissions";
import { messages as resource_display } from "./copy/resource-display";
import { messages as resource_feedback } from "./copy/resource-feedback";
import { messages as resource_forms } from "./copy/resource-forms";
import { messages as resources } from "./copy/resources";
import { messages as settings } from "./copy/settings";
import { messages as streams } from "./copy/streams";
import { messages as updater } from "./copy/updater";
import type { Locale } from "@/types/domain";

export const uiMessages = { ...presentationLabels, ...presentationMessages, ...account, ...application, ...archive, ...audit, ...auth, ...common, ...metrics, ...monitoring, ...nodes, ...permissions, ...resource_display, ...resource_feedback, ...resource_forms, ...resources, ...settings, ...streams, ...updater } as const;
export type UICopyKey = keyof typeof uiMessages;
export type UICopy = (key: UICopyKey, ...values: readonly (string | number | null | undefined)[]) => string;

export function createUICopy(locale: Locale): UICopy {
  return (key, ...values) => {
    const template = locale === "ja" ? key : uiMessages[key];
    return template.replace(/\{(\d+)\}/g, (_, index: string) => String(values[Number(index)] ?? ""));
  };
}
export const japaneseCopy = createUICopy("ja");
