"use client";

import { cloneElement, useId, type ReactElement, type ReactNode } from "react";
import { cn } from "@/lib/utils";

type FieldControl = { id?: string; "aria-describedby"?: string; "aria-invalid"?: boolean | "true" | "false" };

export function Field({ label, description, error, disabledReason, required, children, className }: {
  label: string; description?: ReactNode; error?: ReactNode; disabledReason?: ReactNode;
  required?: boolean; children: ReactElement<FieldControl>; className?: string;
}) {
  const generated = useId();
  const id = children.props.id || generated;
  const describedBy = [children.props["aria-describedby"], description ? `${id}-description` : "", error ? `${id}-error` : "", disabledReason ? `${id}-disabled` : ""].filter(Boolean).join(" ");
  return <div data-slot="field" className={cn("min-w-0 space-y-2", className)}>
    <label htmlFor={id} className="text-sm font-medium leading-6">{label}{required ? <span aria-hidden="true"> *</span> : null}</label>
    {description ? <div id={`${id}-description`} className="text-sm leading-6 text-muted-foreground">{description}</div> : null}
    {cloneElement(children, { id, "aria-describedby": describedBy || undefined, "aria-invalid": error ? true : children.props["aria-invalid"] })}
    {error ? <div id={`${id}-error`} role="alert" className="text-sm text-status-critical">{error}</div> : null}
    {disabledReason ? <div id={`${id}-disabled`} className="text-sm text-muted-foreground">{disabledReason}</div> : null}
  </div>;
}

export function FieldGroup({ title, description, children }: { title: string; description?: ReactNode; children: ReactNode }) {
  return <fieldset data-slot="field-group" className="min-w-0 space-y-4 border-t py-5">
    <legend className="pr-3 text-base font-semibold">{title}</legend>
    {description ? <p className="max-w-3xl text-sm leading-6 text-muted-foreground">{description}</p> : null}
    <div className="grid min-w-0 gap-4 sm:grid-cols-2">{children}</div>
  </fieldset>;
}
