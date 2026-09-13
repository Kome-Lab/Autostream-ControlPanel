import type { MetricSnapshot } from "@/types/domain";


export function mockWorkerMetrics(): MetricSnapshot[] {
  const offsets = [-35, -30, -25, -20, -15, -10, -5, 0];
  const nodes = [
    { id: "worker-main", type: "worker", status: "online", cpu: 34, memory: 42, disk: 58, rx: 4200, tx: 2100, heap: 156 * 1024 * 1024, uptime: 212400, workload: { name: "worker.active_jobs", value: 2 } },
    { id: "encoder-main", type: "encoder_recorder", status: "online", cpu: 48, memory: 55, disk: 64, rx: 6200, tx: 9100, heap: 248 * 1024 * 1024, uptime: 188000, workload: { name: "encoder.output_bitrate_kbps", value: 7800 } },
    { id: "worker-standby", type: "worker", status: "online", cpu: 18, memory: 31, disk: 44, rx: 900, tx: 420, heap: 118 * 1024 * 1024, uptime: 167200, workload: { name: "worker.active_jobs", value: 0 } },
    { id: "encoder-field", type: "encoder_recorder", status: "degraded", cpu: 72, memory: 68, disk: 79, rx: 3100, tx: 120, heap: 302 * 1024 * 1024, uptime: 94500, workload: { name: "encoder.output_bitrate_kbps", value: 0 } },
    { id: "discord-main", type: "discord_bot", status: "online", cpu: 14, memory: 26, disk: 37, rx: 1600, tx: 1400, heap: 86 * 1024 * 1024, uptime: 198400, workload: { name: "discord.audio_forward_active", value: 1 } },
  ];
  return nodes.flatMap((node, nodeIndex) =>
    offsets.flatMap((minutesAgo, pointIndex) => {
      const updatedAt = new Date(Date.now() + minutesAgo * 60 * 1000).toISOString();
      const phase = pointIndex * 0.75 + nodeIndex;
      const cpu = clampMetric(node.cpu + Math.sin(phase) * 5, 0, 100);
      const memory = clampMetric(node.memory + Math.cos(phase) * 3, 0, 100);
      const disk = clampMetric(node.disk + Math.sin(pointIndex / 3) * 0.6, 0, 100);
      const rx = Math.max(0, Math.round(node.rx + Math.sin(phase) * 460));
      const tx = Math.max(0, Math.round(node.tx + Math.cos(phase) * 520));
      const heap = Math.max(0, Math.round(node.heap + Math.sin(phase) * 6 * 1024 * 1024));
      return [
        metricAt("node.cpu.used_percent", node.id, node.type, node.status, cpu, updatedAt),
        metricAt("node.load1", node.id, node.type, node.status, Number((cpu / 38).toFixed(2)), updatedAt),
        metricAt("node.memory.used_percent", node.id, node.type, node.status, memory, updatedAt),
        metricAt("node.memory.used_bytes", node.id, node.type, node.status, Math.round((memory / 100) * 16 * 1024 * 1024 * 1024), updatedAt),
        metricAt("node.filesystem.root.used_percent", node.id, node.type, node.status, disk, updatedAt),
        metricAt("node.network.rx_kbps", node.id, node.type, node.status, rx, updatedAt),
        metricAt("node.network.tx_kbps", node.id, node.type, node.status, tx, updatedAt),
        metricAt("process.goroutines", node.id, node.type, node.status, Math.round(24 + nodeIndex * 7 + Math.sin(phase) * 3), updatedAt),
        metricAt("process.heap_alloc_bytes", node.id, node.type, node.status, heap, updatedAt),
        metricAt("process.uptime_seconds", node.id, node.type, node.status, node.uptime + (35 + minutesAgo) * 60, updatedAt),
        metricAt(node.workload.name, node.id, node.type, node.status, node.workload.value, updatedAt),
      ];
    }),
  );
}

function metricAt(name: string, serviceID: string, serviceType: string, status: string, value: number, updatedAt: string): MetricSnapshot {
  return { name, service_id: serviceID, service_type: serviceType, status, value, updated_at: updatedAt };
}

function clampMetric(value: number, min: number, max: number) {
  return Number(Math.min(max, Math.max(min, value)).toFixed(1));
}
