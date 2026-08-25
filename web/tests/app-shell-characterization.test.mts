import assert from "node:assert/strict";
import { existsSync, readFileSync, readdirSync } from "node:fs";
import { dirname, join, relative } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";

const webRoot = fileURLToPath(new URL("..", import.meta.url));
const sourceRoot = join(webRoot, "src");
const appRoot = join(sourceRoot, "app");
const legacyShellPath = join(sourceRoot, "components", "admin", "admin-shell.tsx");
const navigationModelPath = join(sourceRoot, "lib", "navigation.ts");
const navigationSource = readFileSync(existsSync(navigationModelPath) ? navigationModelPath : legacyShellPath, "utf8");
const shellSource = shellSources().map((path) => readFileSync(path, "utf8")).join("\n");

const expectedNavigation = [
  ["/admin/", "dashboard", []],
  ["/admin/streams/", "streams", ["streams.read"]],
  ["/admin/service-health/", "serviceHealth", ["service_health.read"]],
  ["/admin/incidents/", "incidents", ["incidents.read"]],
  ["/admin/archive/", "archive", ["archives.read", "archive_profiles.read", "integrations.read"]],
  ["/admin/logs/", "logs", ["logs.read"]],
  ["/admin/workers/", "workers", ["workers.read", "service_health.read", "api_tokens.create"]],
  ["/admin/encoder/", "encoder", ["encoder_profiles.read"]],
  ["/admin/discord/", "discord", ["discord_configs.read"]],
  ["/admin/youtube/", "youtube", ["youtube_outputs.read"]],
  ["/admin/caption/", "caption", ["caption_profiles.read"]],
  ["/admin/overlay/", "overlay", ["overlay_profiles.read"]],
  ["/admin/monitoring/", "monitoring", ["incidents.read", "service_health.read"]],
  ["/admin/diagnostics/", "diagnostics", ["diagnostics.read"]],
  ["/admin/remediation/", "remediation", ["remediation.read"]],
  ["/admin/notifications/", "notifications", ["notification_channels.read"]],
  ["/admin/metrics/", "metrics", ["metrics.read"]],
  ["/admin/audit-logs/", "auditLogs", ["audit_logs.read"]],
  ["/admin/users/", "users", ["users.read"]],
  ["/admin/roles/", "roles", ["roles.read"]],
  ["/admin/integrations/", "integrations", ["integrations.read"]],
  ["/admin/security/", "security", ["secrets.read_status", "system_settings.read"]],
  ["/admin/nodes/", "nodeRegistration", ["api_tokens.create"]],
  ["/admin/registered-nodes/", "registeredNodes", ["api_tokens.create"]],
  ["/admin/application/", "applicationInfo", ["system_settings.read", "system_updates.read"]],
  ["/admin/settings/", "settings", ["system_settings.read"]],
] as const;

test("admin navigation keeps four groups and the established 26 entries", () => {
  const sectionKeys = [...navigationSource.matchAll(/key:\s*"(nav(?:Operations|Profiles|Monitoring|Administration))"/g)].map((match) => match[1]);
  assert.deepEqual(sectionKeys, ["navOperations", "navProfiles", "navMonitoring", "navAdministration"]);

  const entries = [...navigationSource.matchAll(/navItem\(\s*"([^"]+)",\s*"([^"]+)",\s*[A-Za-z0-9]+,\s*\[([^\]]*)\]/g)].map((match) => [
    match[1],
    match[2],
    [...match[3].matchAll(/"([^"]+)"/g)].map((permission) => permission[1]),
  ]);
  assert.deepEqual(entries, expectedNavigation);
});

test("navigation visibility remains permission filtered with a super-admin override", () => {
  assert.match(shellSource, /hasAnyPermission\(currentUser,\s*item\.permissions\)/);
  assert.match(shellSource, /roles\?\.includes\("super_admin"\)/);
  assert.match(shellSource, /items\.filter\(\(item\) => canSeeNavItem\(item,\s*currentUser\)\)/);
});

test("desktop and mobile navigation keep the same lifted section state", () => {
  assert.match(shellSource, /max-lg:hidden/);
  assert.match(shellSource, /SheetContent[\s\S]*side="left"/);
  assert.match(shellSource, /SheetClose asChild/);
  assert.match(shellSource, /sectionState=\{synchronizedNavigationSectionsState\}/);
  assert.match(shellSource, /aria-label="ナビゲーションを開く"/);
});

test("shell controls and stream-create permission remain present", () => {
  assert.match(shellSource, /hasPermission\(currentUser\.data,\s*"streams\.create"\)/);
  assert.match(shellSource, /href="\/admin\/streams\/#create-stream"/);
  assert.match(shellSource, /setLocale/);
  assert.match(shellSource, /toggleTheme/);
  assert.match(shellSource, /aria-label="アカウントメニュー"/);
  assert.match(shellSource, /\/auth\/logout/);
  assert.match(shellSource, /\/auth\/session\/refresh/);
  assert.match(shellSource, /\/setup\/status/);
  assert.match(shellSource, /loginPathForLocation/);
});

test("only authenticated admin routes receive the app shell", () => {
  const shellUsers = sourceFiles(appRoot)
    .filter((path) => /AdminShell/.test(readFileSync(path, "utf8")))
    .map((path) => relative(appRoot, path).replaceAll("\\", "/"));
  assert.deepEqual(shellUsers, ["admin/layout.tsx"]);

  for (const publicPage of ["login/page.tsx", "setup/page.tsx", "auth/email/confirm/page.tsx", "archive/share/page.tsx"]) {
    assert.equal(/AdminShell/.test(readFileSync(join(appRoot, ...publicPage.split("/")), "utf8")), false, publicPage);
  }
});

function shellSources() {
  const paths = [legacyShellPath];
  const shellDirectory = join(sourceRoot, "components", "shell");
  if (existsSync(shellDirectory)) paths.push(...sourceFiles(shellDirectory));
  if (existsSync(navigationModelPath)) paths.push(navigationModelPath);
  return [...new Set(paths)];
}

function sourceFiles(directory: string): string[] {
  return readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
    const path = join(directory, entry.name);
    if (entry.isDirectory()) return sourceFiles(path);
    return /\.(?:ts|tsx)$/.test(entry.name) ? [path] : [];
  }).sort((left, right) => relative(dirname(directory), left).localeCompare(relative(dirname(directory), right)));
}
