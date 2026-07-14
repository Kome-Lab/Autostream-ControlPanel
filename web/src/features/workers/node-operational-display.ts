type HeartbeatSource = {
  heartbeat_age_sec?: number;
  last_heartbeat_at?: string;
};

type NodeMetrics = Record<string, number | string> | undefined;
type PercentMetric = "cpu" | "memory";

const PERCENT_METRIC_KEYS: Record<PercentMetric, readonly string[]> = {
  cpu: ["node.cpu.used_percent", "host.cpu_percent", "worker.cpu_percent", "process.cpu_percent", "cpu_percent", "cpuUsage"],
  memory: ["node.memory.used_percent", "host.memory_percent", "worker.memory_percent", "process.memory_percent", "memory_percent", "memoryUsage"],
};

export function formatWorkerHeartbeat(node: HeartbeatSource, nowMs = Date.now()) {
  const reportedAge = finiteNumber(node.heartbeat_age_sec);
  if (reportedAge !== undefined && reportedAge >= 0) {
    return `${Math.floor(reportedAge)} sec`;
  }

  const heartbeatAt = node.last_heartbeat_at ? Date.parse(node.last_heartbeat_at) : Number.NaN;
  if (!Number.isFinite(heartbeatAt) || !Number.isFinite(nowMs)) return "未取得";

  const ageSeconds = Math.max(0, Math.floor((nowMs - heartbeatAt) / 1000));
  return `${ageSeconds} sec`;
}

export function formatNodeMetricPercent(metrics: NodeMetrics, metric: PercentMetric) {
  const value = metricValue(metrics, PERCENT_METRIC_KEYS[metric]);
  if (value === undefined) return "未報告";
  return `${Math.round(value * 10) / 10}%`;
}

function metricValue(metrics: NodeMetrics, keys: readonly string[]) {
  if (!metrics) return undefined;
  for (const key of keys) {
    const value = finiteNumber(metrics[key]);
    if (value !== undefined) return value;
  }
  return undefined;
}

function finiteNumber(value: unknown) {
  if (typeof value === "number" && Number.isFinite(value)) return value;
  if (typeof value === "string" && value.trim() !== "" && Number.isFinite(Number(value))) return Number(value);
  return undefined;
}
