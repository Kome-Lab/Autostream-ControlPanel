"use client";

import type { WorkerNode } from "@/types/domain";

export function mergeServiceHealthRows(registeredNodes: WorkerNode[], serviceHealthRows: WorkerNode[]) {
  const merged = new Map<string, WorkerNode>();
  for (const node of registeredNodes) {
    const key = serviceNodeIdentity(node);
    if (key) merged.set(key, node);
  }
  for (const health of serviceHealthRows) {
    const key = serviceNodeIdentity(health);
    if (!key) continue;
    const current = merged.get(key);
    merged.set(key, current ? mergeServiceHealthRow(current, health) : health);
  }
  return Array.from(merged.values()).sort(compareServiceHealthRows);
}

function mergeServiceHealthRow(registered: WorkerNode, health: WorkerNode): WorkerNode {
  return {
    ...registered,
    ...health,
    id: registered.id || health.id,
    service_id: registered.service_id || health.service_id,
    service_type: registered.service_type || health.service_type,
    service_name: registered.service_name || health.service_name,
    description: registered.description || health.description,
    public_url: health.public_url || registered.public_url,
    status: health.status || registered.status,
    health_status: health.health_status || registered.health_status,
    last_heartbeat_at: health.last_heartbeat_at || registered.last_heartbeat_at,
  };
}

function serviceNodeIdentity(node: WorkerNode) {
  return node.service_id || node.id || "";
}

function compareServiceHealthRows(a: WorkerNode, b: WorkerNode) {
  const type = String(a.service_type || "").localeCompare(String(b.service_type || ""), "ja");
  if (type !== 0) return type;
  return String(a.service_name || a.service_id || a.id || "").localeCompare(String(b.service_name || b.service_id || b.id || ""), "ja");
}
