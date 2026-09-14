import type { ReactNode } from "react";

export function FormFooter({ children, feedback, pending = false }: { children: ReactNode; feedback?: ReactNode; pending?: boolean }) {
  return <footer data-slot="form-footer" aria-busy={pending} className="flex min-w-0 flex-wrap items-center justify-between gap-3 border-t pt-4">
    <div role="status" className="min-w-0 text-sm leading-6">{feedback}</div>
    <div className="flex flex-wrap items-center gap-2">{children}</div>
  </footer>;
}
