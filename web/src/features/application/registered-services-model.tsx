"use client";

import type { WorkerNode } from "@/types/domain";
import { serviceTypeLabel } from "./service-update-presentation";

export function mergeRegisteredNodeRows(registeredNodes: WorkerNode[], serviceHealthRows: WorkerNode[]) {
  const merged = new Map<string, WorkerNode>();
  for (const node of registeredNodes) { const key = nodeIdentity(node); if (key) merged.set(key, node); }
  for (const health of serviceHealthRows) { const key = nodeIdentity(health); if (!key) continue; const current = merged.get(key); merged.set(key, current ? mergeNodeRow(current, health) : health); }
  return Array.from(merged.values());
}

function mergeNodeRow(registered: WorkerNode, health: WorkerNode): WorkerNode {
  return { ...registered, ...health, service_id: registered.service_id || health.service_id, id: registered.id || health.id, service_type: registered.service_type || health.service_type, service_name: registered.service_name || health.service_name, description: registered.description || health.description, reported_version: health.reported_version || registered.reported_version, reported_commit: health.reported_commit || registered.reported_commit, reported_build_date: health.reported_build_date || registered.reported_build_date, version: health.version || registered.version, status: health.status || registered.status, health_status: health.health_status || registered.health_status };
}

export function nodeIdentity(node: WorkerNode) { return node.service_id || node.id || ""; }

export function compareServiceRows(a: WorkerNode, b: WorkerNode) { const type = serviceTypeLabel(a.service_type).localeCompare(serviceTypeLabel(b.service_type), "ja"); return type !== 0 ? type : (a.service_name || a.service_id || "").localeCompare(b.service_name || b.service_id || "", "ja"); }
