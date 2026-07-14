export type MetricUnit = "percent" | "kbps" | "bytes" | "seconds" | "count" | "flag" | "number";

export type MetricGroup = "cpu" | "memory" | "disk" | "network" | "heap" | "workload" | "runtime";

export function metricGroup(name: string, unit: MetricUnit): MetricGroup {
  const lower = name.toLowerCase();
  if (unit === "percent" && lower.includes("cpu")) return "cpu";
  if (unit === "percent" && lower.includes("filesystem")) return "disk";
  if (lower.includes("network") || lower.includes("net.") || lower.includes("interface") || lower.includes("rx_") || lower.includes("tx_")) return "network";
  if (unit === "percent" && (lower.includes("memory") || lower.includes("mem"))) return "memory";
  if (unit === "bytes" && lower.includes("heap")) return "heap";
  if (lower.includes("bitrate") || lower.includes("fps") || lower.includes("active") || lower.includes("process_alive") || lower.includes("audio")) return "workload";
  return "runtime";
}
