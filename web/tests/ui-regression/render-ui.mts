import "./component-loader.mts";
import { createElement, type ReactElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createUIFixture } from "./route-fixture.mts";
import { conditions } from "./matrix.mts";
const { I18nProvider } = await import("../../src/components/admin/i18n-provider.tsx");
const { AppRouterContext } = await import("next/dist/shared/lib/app-router-context.shared-runtime.js");
const { SearchParamsContext, PathnameContext } = await import("next/dist/shared/lib/hooks-client-context.shared-runtime.js");
const { ThemeProvider } = await import("../../src/components/admin/theme-provider.tsx");
const { TooltipProvider } = await import("../../src/components/ui/tooltip.tsx");
const { normalizeSystemUpdatesResponse } = await import("../../src/lib/system-updates.ts");
const { resourcePages } = await import("../../src/features/resources/resource-config.ts");
const paths=["/auth/me","/settings/app","/version","/streams","/service-health","/nodes","/workers","/system-updates","/archive/streams","/archive/processing-streams","/observability/metrics","/audit-logs","/auth/mfa/status","/auth/passkeys","/auth/oauth-links","/auth/oauth/providers","/setup/status","/archive-shares/ui-synthetic-share",...Object.values(resourcePages).flatMap(page=>page.resources.map(resource=>resource.path))];
export function renderUI(element: ReactElement, locale: "ja"|"en", route="/admin/", configure?: (client:QueryClient)=>void) {
  const client=new QueryClient({defaultOptions:{queries:{staleTime:Infinity,gcTime:Infinity,retry:false,retryOnMount:false}}});
  const fixture=createUIFixture("http://ui.test");
  fixture.reset({...conditions[0],family:"dashboard",state:"ready",locale},"/streams");
  const keys: Record<string, unknown[]>={
    "/auth/me":["auth","me"],"/settings/app":["settings","app"],"/version":["version"],
    "/streams":["streams"],"/service-health":["service-health"],"/nodes":["nodes"],"/workers":["workers"],"/system-updates":["system-updates"],
    "/archive/streams":["archive-streams"],"/archive/processing-streams":["archive-processing-streams"],"/observability/metrics":["observability","metrics",10800],
    "/audit-logs":["audit-logs",{from:"",to:"",result:"all",q:"",excludeActionGroup:"node_activity"}],
    "/auth/mfa/status":["auth","mfa","status"],"/auth/passkeys":["auth","passkeys"],"/auth/oauth-links":["auth","oauth-links"],"/auth/oauth/providers":["auth","oauth","providers"],
    "/setup/status":["setup","status"],"/archive-shares/ui-synthetic-share":["archive-share","ui-synthetic-share"],
  };
  for(const path of new Set(paths)) {
    const response=fixture.resolver({method:"GET",url:"http://ui.test"+path});
    if(response && typeof response==="object" && "body" in response) {
      const data=path==="/system-updates"?normalizeSystemUpdatesResponse(response.body):response.body;
      client.setQueryData(keys[path]||["resource",path],data);
      client.setQueryData(["resource",path],data);
    }
  }
  client.setQueryData(["auth","oauth","providers","login"],client.getQueryData(["auth","oauth","providers"]));
  configure?.(client);
  const dateNowBefore=Date.now;Date.now=()=>Date.parse("2026-09-01T01:00:00Z");
  const windowBefore=Object.getOwnPropertyDescriptor(globalThis,"window");
  Object.defineProperty(globalThis,"window",{configurable:true,value:{location:new URL(route,"http://ui.test"),localStorage:{getItem:()=>locale},sessionStorage:{getItem:()=>null},matchMedia:()=>({matches:false})}});
  try {
    const router={bfcacheId:"ui-synthetic-render",back(){},forward(){},refresh(){},push(){},replace(){},prefetch(){}};
    const tree=createElement(QueryClientProvider,{client},createElement(AppRouterContext.Provider,{value:router},
      createElement(SearchParamsContext.Provider,{value:new URL(route,"http://ui.test").searchParams},
      createElement(PathnameContext.Provider,{value:new URL(route,"http://ui.test").pathname},
      createElement(I18nProvider,null,createElement(ThemeProvider,null,createElement(TooltipProvider,null,element)))))));
    return renderToStaticMarkup(tree);
  } finally {Date.now=dateNowBefore;if(windowBefore)Object.defineProperty(globalThis,"window",windowBefore);else Reflect.deleteProperty(globalThis,"window");client.clear();}
}
