// Confirmation owns a detached snapshot. A caller editing a form/cache while a
// fresh authority read is pending cannot change the request being confirmed.
export function snapshotStreamIntent<T>(value: T): T {
  const copy = structuredClone(value);
  const seen = new WeakSet<object>();
  const freeze = (candidate: unknown) => {
    if (!candidate || typeof candidate !== "object" || seen.has(candidate)) return;
    seen.add(candidate);
    for (const child of Object.values(candidate)) freeze(child);
    Object.freeze(candidate);
  };
  freeze(copy);
  return copy;
}
