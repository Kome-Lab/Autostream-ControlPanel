import type { ReactNode } from "react";

export function GlobalCommandBoundary({ children }: { children?: ReactNode }) {
  return children ? <div data-slot="global-command-boundary">{children}</div> : null;
}
