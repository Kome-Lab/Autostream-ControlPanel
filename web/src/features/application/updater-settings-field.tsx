import { Children, cloneElement, isValidElement, type ReactNode } from "react";


export function Field({ label, htmlFor, hint, children }: { label: string; htmlFor: string; hint?: string; children: ReactNode }) {
  return (
    <div className="space-y-1.5">
      <label htmlFor={htmlFor} className="text-sm font-medium">{label}</label>
      {Children.map(children, (child) => isValidElement<{ id?: string; "aria-describedby"?: string }>(child) && child.props.id === htmlFor
        ? cloneElement(child, { "aria-describedby": [child.props["aria-describedby"], hint ? htmlFor + "-hint" : ""].filter(Boolean).join(" ") || undefined })
        : child)}
      {hint ? <p id={htmlFor + "-hint"} className="text-sm leading-6 text-muted-foreground">{hint}</p> : null}
    </div>
  );
}
