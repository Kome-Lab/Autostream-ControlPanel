"use client";

import { useState } from "react";
import { DraftExitContext, useDraftExit } from "@/components/forms/draft-exit";
import { resourceCopy } from "@/lib/i18n/ui-v2/resource-copy";
import { PageHeader } from "@/components/shell/page-header";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { useI18n } from "@/components/admin/i18n-provider";
import { useCurrentUser } from "@/features/queries";
import { resourcePages, type ResourceDefinition, type ResourcePageId } from "@/features/resources/resource-config";
import { hasPermission } from "@/lib/auth/permissions";
import { resourceAccess } from "./resource-permissions";
import { ServiceHealthResourcePanel } from "./service-health-resource-panel";
import { GenericResourcePanel } from "./generic-resource-panel";

export function ResourcePage({ pageId }: { pageId: ResourcePageId }) {
  const { t, locale } = useI18n();
  const currentUser = useCurrentUser();
  const page = resourcePages[pageId];
  const defaultTab = page.resources[0]?.path || "";
  const [tab, setTab] = useState(defaultTab);
  const draftExit = useDraftExit({ enabled: Boolean(currentUser.data?.user), pending: false });

  return (
    <DraftExitContext.Provider value={draftExit}><div className="space-y-5" data-screen-family={pageId}>
      <PageHeader title={t(page.titleKey)} description={locale === "ja" ? page.description : page.resources.map((resource) => resourceCopy(resource, locale).description).join(" ")} breadcrumbs={[{ label: t("dashboard"), href: "/admin/" }, { label: t(page.titleKey) }]} />

      {page.resources.length === 1 ? (
        <ResourcePanel resource={page.resources[0]} currentUser={currentUser.data} />
      ) : (
        <Tabs value={tab} onValueChange={(value) => draftExit.request(() => setTab(value))} className="space-y-4">
          <TabsList className="h-auto min-w-0 max-w-full flex-wrap justify-start">
            {page.resources.map((resource) => (
              <TabsTrigger key={resource.path} value={resource.path} className="h-auto min-w-0 max-w-full whitespace-normal [overflow-wrap:anywhere]">
                {resourceCopy(resource, locale).title}
              </TabsTrigger>
            ))}
          </TabsList>
          {page.resources.map((resource) => (
            <TabsContent key={resource.path} value={resource.path}>
                <ResourcePanel resource={resource} currentUser={currentUser.data} />
            </TabsContent>
          ))}
        </Tabs>
      )}
    </div></DraftExitContext.Provider>
  );
}

export function ResourcePanel({ resource, currentUser }: { resource: ResourceDefinition; currentUser: Parameters<typeof hasPermission>[0] }) {
  const access = resourceAccess(resource, currentUser);
  if (resource.path === "/service-health") {
    return <ServiceHealthResourcePanel resource={resource} access={access} />;
  }

  return <GenericResourcePanel resource={resource} access={access} currentUser={currentUser} />;
}
