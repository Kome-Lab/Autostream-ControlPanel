import type { CurrentUser } from "@/types/domain";

export function hasPermission(currentUser: CurrentUser | undefined, permission: string) {
  if (!currentUser) return false;
  if (currentUser.permissions.includes("*")) return true;
  return currentUser.permissions.includes(permission);
}

export function hasAnyPermission(currentUser: CurrentUser | undefined, permissions: string[]) {
  return permissions.some((permission) => hasPermission(currentUser, permission));
}
