import type { ResourceDefinition } from "@/features/resources/resource-config";

const resources: Readonly<Record<string, { title: string; description: string }>> = {
  "/profiles/encoder": { title: "Encoder profiles", description: "Reusable video conversion settings read by the Worker and Encoder when a stream starts." },
  "/discord/configs": { title: "Discord bots", description: "Manage bot registration, credentials, audio forwarding and reconnection policy." },
  "/discord/target-presets": { title: "Discord target presets", description: "Reuse Guild, Chat and Voice selections. Streams retain their saved snapshot when a preset changes." },
  "/youtube/outputs": { title: "YouTube outputs", description: "Manage RTMP or YouTube Live API outputs used when a stream starts." },
  "/profiles/caption": { title: "Caption profiles", description: "Manage language, segmentation, interim results and timing. Saving and live application have separate outcomes." },
  "/profiles/overlay": { title: "Watermark profiles", description: "Manage the fixed 1920 × 1080 watermark composited over the stream." },
  "/profiles/archive": { title: "Recording profiles", description: "Configure recording format, retention and upload behavior." },
  "/archive/destinations": { title: "Drive destinations", description: "Manage archive destinations and their selected root folder." },
  "/integrations/oauth-providers": { title: "OAuth login providers", description: "Manage login providers. Scopes remain restricted to login." },
  "/integrations/oauth-accounts": { title: "YouTube and Drive connections", description: "Manage connected accounts. Relink an account without replacing its existing references." },
  "/stream-logs": { title: "Stream logs", description: "Chronological logs retained after a stream slot is deleted." },
  "/users": { title: "Users", description: "Manage login accounts, state and roles. The server enforces administrator and self-operation restrictions." },
  "/roles": { title: "Roles", description: "Manage permission sets assigned to users." },
  "/permissions": { title: "Permission catalog", description: "Review the permissions available to roles." },
  "/security/settings": { title: "Security settings", description: "Manage password, lockout, session and MFA policies." },
  "/secrets/status": { title: "Configured credentials", description: "Only registration status is displayed. Stored secret values are never returned here." },
  "/service-health": { title: "Service health", description: "Review connected services, health, heartbeat and stream assignment." },
  "/observability/incidents": { title: "Incidents", description: "Review severity, occurrence, evidence and resolution state. Diagnostics and remediation remain separate actions." },
  "/observability/diagnostics": { title: "Diagnostics", description: "Review hypotheses, confidence, evidence and checks before choosing the next operation." },
  "/observability/remediation-actions": { title: "Remediation", description: "Review each recovery proposal and its current authority before confirming execution." },
  "/observability/notification-deliveries": { title: "Delivery history", description: "Review notification delivery outcomes for incidents, remediation and administrative actions." },
  "/observability/notification-channels": { title: "Notification channels", description: "Manage destinations and events. Email uses the shared SMTP relay; a blank credential preserves the saved value." },
  "/observability/metrics": { title: "Metrics", description: "Review metrics received from the monitoring system." },
};

export function resourceCopy(resource: ResourceDefinition, locale: "ja" | "en") {
  return locale === "en" ? resources[resource.path] || { title: resource.title, description: resource.description }
    : { title: resource.title, description: resource.description };
}
