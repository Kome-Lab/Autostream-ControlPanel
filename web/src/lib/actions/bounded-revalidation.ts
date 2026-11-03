// A save intent may wait for one bounded authority read. Polling remains active;
// the successful result belongs to that intent rather than a cache fetch flag.
const authorityReadTimeoutMs = 10_000;

export async function withBoundedActionRevalidation<T>(
  read: (signal: AbortSignal) => Promise<T>,
  signal?: AbortSignal,
): Promise<T> {
  const deadline = new AbortController();
  const combined = signal ? AbortSignal.any([signal, deadline.signal]) : deadline.signal;
  const timer = setTimeout(() => deadline.abort(new DOMException("Authority read timed out", "TimeoutError")), authorityReadTimeoutMs);
  try {
    if (combined.aborted) throw combined.reason;
    return await new Promise<T>((resolve, reject) => {
      const aborted = () => reject(combined.reason);
      combined.addEventListener("abort", aborted, { once: true });
      void Promise.resolve().then(() => {
        if (combined.aborted) throw combined.reason;
        return read(combined);
      }).then(resolve, reject).finally(() => combined.removeEventListener("abort", aborted));
    });
  } finally {
    clearTimeout(timer);
  }
}
