"use client";

import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { useI18n } from "@/components/admin/i18n-provider";
import { useCurrentUser } from "@/features/queries";
import { resourcePages, type ResourceDefinition, type ResourcePageId } from "@/features/resources/resource-config";
import { hasPermission } from "@/lib/auth/permissions";
import { resourceAccess } from "./resource-permissions";
import { ServiceHealthResourcePanel } from "./service-health-resource-panel";
import { GenericResourcePanel } from "./generic-resource-panel";

export function ResourcePage({ pageId }: { pageId: ResourcePageId }) {
  const { t } = useI18n();
  const currentUser = useCurrentUser();
  const page = resourcePages[pageId];
  const defaultTab = page.resources[0]?.path || "";

  return (
    <div className="space-y-6">
      <section>
        <h1 className="text-2xl font-semibold tracking-normal">{t(page.titleKey)}</h1>
        <p className="mt-2 max-w-3xl text-sm text-muted-foreground">{page.description}</p>
      </section>

      {page.resources.length === 1 ? (
        <ResourcePanel resource={page.resources[0]} currentUser={currentUser.data} />
      ) : (
        <Tabs defaultValue={defaultTab} className="space-y-4">
          <TabsList className="max-w-full flex-wrap justify-start">
            {page.resources.map((resource) => (
              <TabsTrigger key={resource.path} value={resource.path}>
                {resource.title}
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
    </div>
  );
}

export function ResourcePanel({ resource, currentUser }: { resource: ResourceDefinition; currentUser: Parameters<typeof hasPermission>[0] }) {
  const access = resourceAccess(resource, currentUser);
  if (resource.path === "/service-health") {
    return <ServiceHealthResourcePanel resource={resource} access={access} />;
  }

  return <GenericResourcePanel resource={resource} access={access} currentUser={currentUser} />;
}
