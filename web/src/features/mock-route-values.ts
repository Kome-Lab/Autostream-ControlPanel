import type { OAuthLoginProvider } from "@/types/domain";
import { mockResourceData } from "./mock-state";


export function mockRoleNames(roleIDs: string[]) {
  const roles = mockResourceData["/roles"] as Array<Record<string, unknown>>;
  return roleIDs
    .map((roleID) => roles.find((role) => role.id === roleID)?.name)
    .filter((name): name is string => typeof name === "string" && name !== "");
}

export function mockLoginOAuthProviders(): OAuthLoginProvider[] {
  const providers = mockResourceData["/integrations/oauth-providers"] as OAuthLoginProvider[];
  return providers.filter((provider) => provider.enabled);
}

export function mockDeleteCollectionPath(path: string) {
  return Object.keys(mockResourceData)
    .filter((collectionPath) => path.startsWith(`${collectionPath}/`))
    .sort((a, b) => b.length - a.length)[0];
}

export function deleteFromArray(rows: Record<string, unknown>[], id: string) {
  const index = rows.findIndex((row) => {
    for (const key of ["id", "service_id", "name"]) {
      const value = row[key];
      if (typeof value === "string" && value === id) return true;
    }
    return false;
  });
  if (index >= 0) rows.splice(index, 1);
}

export function stripQuery(path: string) {
  return String(path || "").split("?")[0];
}

export function maskMockEmail(value: string) {
  const [local, domain] = value.split("@");
  if (!local || !domain) return "masked";
  return `${local.slice(0, 1)}***@${domain}`;
}
